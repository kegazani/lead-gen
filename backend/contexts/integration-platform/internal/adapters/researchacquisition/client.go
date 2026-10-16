package researchacquisition

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"lead-gen/backend/contexts/integration-platform/internal/adapters/firecrawl"
	contract "lead-gen/contracts/integration-platform/researchacquisition"
	"lead-gen/contracts/integration-platform/search"
)

// Client использует реальный прежний Firecrawl HTTP adapter, его тарифы и
// классификацию ошибок. Process-local Guard намеренно не оборачивается:
// единственное списание нового пути находится в durable Store.
type Client struct {
	provider  *firecrawl.Client
	store     *Store
	accountID string
}

func NewClient(provider *firecrawl.Client, store *Store, accountID string) *Client {
	return &Client{provider: provider, store: store, accountID: accountID}
}

var _ contract.Port = (*Client)(nil)

func (c *Client) Capabilities() contract.Capabilities {
	return contract.Capabilities{Provider: "firecrawl", Revision: "firecrawl-v1-research-1", Search: true, Fetch: true, Representations: []string{"markdown"}}
}
func (c *Client) Search(ctx context.Context, r contract.SearchRequest) (contract.Receipt, error) {
	if err := r.Validate(); err != nil {
		return contract.Receipt{}, err
	}
	if r.Cursor != "" || r.Geography.RequireCountry || r.Geography.RequireLanguage || r.Geography.RequireEgress || r.Geography.RequireBrowserLocale {
		return contract.Receipt{}, contract.ErrUnsupported
	}
	r.Geography = unapplied(r.Geography)
	return c.run(ctx, r.Action, "search", commandHash(r), firecrawl.CostSearch(r.MaxResults), r.Geography, func(ctx context.Context, out *contract.Receipt) (int, error) {
		hits, err := c.provider.Search(ctx, search.Query{Text: r.Query, MaxResults: r.MaxResults})
		for i, h := range hits {
			if i >= r.MaxResults {
				out.Status = "partial"
				out.ErrorCode = "result_limit"
				break
			}
			out.Hits = append(out.Hits, contract.Hit{URL: h.URL, Title: h.Title, Snippet: h.Snippet, Rank: i + 1})
		}
		// Это модель тарифа существующего адаптера, а не выдуманный provider usage.
		return firecrawl.CostSearch(len(hits)), err
	})
}
func (c *Client) Fetch(ctx context.Context, r contract.FetchRequest) (contract.Receipt, error) {
	if err := r.Validate(); err != nil {
		return contract.Receipt{}, err
	}
	if r.Representation != "markdown" || r.Geography.RequireCountry || r.Geography.RequireLanguage || r.Geography.RequireEgress || r.Geography.RequireBrowserLocale {
		return contract.Receipt{}, contract.ErrUnsupported
	}
	if !publicTarget(r.URL) {
		return contract.Receipt{}, fmt.Errorf("%w: непубличный стартовый URL", contract.ErrInvalid)
	}
	r.Geography = unapplied(r.Geography)
	return c.run(ctx, r.Action, "fetch", commandHash(r), firecrawl.CostScrape(), r.Geography, func(ctx context.Context, out *contract.Receipt) (int, error) {
		page, err := c.provider.Scrape(ctx, r.URL)
		if err == nil {
			u := page.URL
			if u == "" {
				u = r.URL
			}
			out.Artifacts = []contract.Artifact{{RequestedURL: r.URL, URL: u, Representation: "markdown", Content: page.Markdown, SHA256: contentHash(page.Markdown), ObservedAt: time.Now().UTC(), Metadata: page.Metadata}}
		}
		return firecrawl.CostScrape(), err
	})
}
func (c *Client) run(ctx context.Context, a contract.Action, kind, hash string, cost int, geo contract.Geography, call func(context.Context, *contract.Receipt) (int, error)) (contract.Receipt, error) {
	if err := ctx.Err(); err != nil {
		return contract.Receipt{}, err
	}
	if a.GrantID == "" {
		return contract.Receipt{}, contract.ErrInvalid
	}
	snapshot, err := c.store.Snapshot(ctx, a.Scope, a.GrantID)
	if err != nil {
		return contract.Receipt{}, err
	}
	if snapshot.Grant.AccountID != c.accountID {
		return contract.Receipt{}, contract.ErrConflict
	}
	res, err := c.store.begin(ctx, a, kind, hash, cost)
	if err != nil {
		return contract.Receipt{}, err
	}
	if res.replay != nil {
		return *res.replay, receiptError(*res.replay)
	}
	out := contract.Receipt{Scope: a.Scope, OperationID: res.id, ActionID: a.ActionID, Attempt: a.Attempt, Provider: "firecrawl", ProviderRevision: c.Capabilities().Revision, ProfileRevision: a.ProfileRevision, Status: "success", Geography: geo, CreditsReserved: cost, CostState: "known", CostBasis: "existing-firecrawl-tariff-v1"}
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(a.Limits.TimeoutSeconds)*time.Second)
	defer cancel()
	unknown := false
	stopAccount := false
	// Probe только читает баланс. Его ошибка не могла вызвать платный эффект;
	// поэтому reserve можно освободить без MarkUnknown.
	beforeProbe, probeErr := c.store.Snapshot(callCtx, a.Scope, a.GrantID)
	var usage firecrawl.CreditUsage
	if probeErr == nil {
		usage, probeErr = c.provider.CreditUsage(firecrawl.WithResponseLimit(callCtx, 64<<10))
	}
	if probeErr == nil && usage.RemainingCredits < 0 {
		probeErr = contract.ErrUnavailable
	}
	if probeErr != nil {
		out.Status = "unavailable"
		out.ErrorCode = "provider_balance_unavailable"
		out.CostBasis = "no_paid_dispatch"
	} else {
		received := time.Now().UTC()
		out.ProviderBalanceReceivedAt = &received
		remaining := usage.RemainingCredits
		out.ProviderRemainingCredits = &remaining
		admissionErr := c.store.admitProvider(callCtx, a, res, remaining, beforeProbe.Account.SpentCredits)
		if errors.Is(admissionErr, contract.ErrBudgetExhausted) {
			out.Status = "budget_exhausted"
			out.ErrorCode = "provider_balance_insufficient"
			out.CostBasis = "no_paid_dispatch"
		} else if errors.Is(admissionErr, contract.ErrThrottled) {
			out.Status = "throttled"
			out.ErrorCode = "provider_balance_reserved"
			out.RetryAfterSeconds = 1
			out.CostBasis = "no_paid_dispatch"
		} else if admissionErr != nil {
			out.Status = "unavailable"
			out.ErrorCode = "provider_admission_unavailable"
			out.CostBasis = "no_paid_dispatch"
		} else if callCtx.Err() != nil {
			out.Status = "unavailable"
			out.ErrorCode = "cancelled_before_dispatch"
			out.CostBasis = "no_paid_dispatch"
		} else {
			callCtx = firecrawl.WithResponseLimit(callCtx, a.Limits.MaxBytes)
			actual, callErr := call(callCtx, &out)
			out.ProviderHTTPStatus, _ = firecrawl.StatusCode(callErr)
			if callErr == nil {
				out.CreditsCharged = actual
				if len(out.Hits) == 0 && len(out.Artifacts) == 0 {
					out.Status = "empty"
				}
			} else if firecrawl.IsRateLimited(callErr) {
				out.Status = "throttled"
				out.ErrorCode = "provider_throttled"
				out.RetryAfterSeconds = int((firecrawl.RetryAfter(callErr) + time.Second - 1) / time.Second)
				if out.RetryAfterSeconds < 1 {
					out.RetryAfterSeconds = 1
				}
				if out.RetryAfterSeconds > 300 {
					out.RetryAfterSeconds = 300
				}
			} else if firecrawl.IsPaymentRequired(callErr) {
				out.Status = "budget_exhausted"
				out.ErrorCode = "provider_payment_required"
				stopAccount = true
			} else {
				// В частности timeout/5xx/ошибка декодирования не доказывают, что provider
				// не выполнил запрос. Никакого повторного POST внутри адаптера нет.
				out.Status = "unknown"
				out.ErrorCode = "provider_outcome_unknown"
				if out.ProviderHTTPStatus > 0 {
					out.ErrorCode = fmt.Sprintf("provider_http_%d", out.ProviderHTTPStatus)
				}
				out.CostState = "unknown"
				out.CostBasis = ""
				unknown = true
				if errors.Is(callErr, firecrawl.ErrResponseTooLarge) {
					out.ErrorCode = "response_limit"
				}
				if len(out.Hits) > 0 || len(out.Artifacts) > 0 {
					out.Status = "partial"
				}
			}

		}
	}
	out.ObservedAt = time.Now().UTC()
	out.Persistence = "persisted"
	out.SHA256 = receiptHash(out)
	// Пользовательская отмена не отменяет фиксацию уже понесённого расхода.
	persistCtx, persistCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer persistCancel()
	if err = c.store.finish(persistCtx, a, res, out, unknown, stopAccount); err != nil {
		out.Persistence = "unknown"
		return out, fmt.Errorf("%w: receipt не сохранён: %v", contract.ErrUnknown, err)
	}
	return out, receiptError(out)
}

func receiptHash(r contract.Receipt) string {
	r.Persistence = ""
	r.SHA256 = ""
	return commandHash(r)
}

func receiptError(r contract.Receipt) error {
	if r.CostState == "unknown" {
		return contract.ErrUnknown
	}
	switch r.Status {
	case "unavailable":
		return contract.ErrUnavailable
	case "throttled":
		return contract.ErrThrottled
	case "budget_exhausted":
		return contract.ErrBudgetExhausted
	case "unknown":
		return contract.ErrUnknown
	}
	return nil
}
func unapplied(g contract.Geography) contract.Geography {
	g.AppliedCountry = ""
	g.AppliedLanguage = ""
	g.ObservedEgressCountry = ""
	g.ProviderClaimedEgressCountry = ""
	return g
}

// Это проверка стартового адреса, а не обещание контролировать соединения
// upstream crawler. Redirect/egress trace Firecrawl v1 не предоставляет.
func publicTarget(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return strings.Contains(host, ".")
	}
	return !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsUnspecified() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !ip.IsMulticast()
}

func contentHash(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }

func (c *Client) Lookup(ctx context.Context, scope contract.Scope, actionID string, attempt int) (contract.Receipt, error) {
	return c.store.Lookup(ctx, scope, actionID, attempt)
}

var _ contract.ReceiptReader = (*Client)(nil)
