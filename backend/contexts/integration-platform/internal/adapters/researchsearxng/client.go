package researchsearxng

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	contract "lead-gen/contracts/integration-platform/researchacquisition"
)

type Config struct {
	BaseURL    string
	HTTPClient *http.Client
	Engines    []string
}
type Client struct {
	store   *Store
	baseURL string
	http    *http.Client
	engines []string
}

func NewClient(cfg Config, store *Store) (*Client, error) {
	base, err := url.Parse(strings.TrimRight(cfg.BaseURL, "/"))
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") || base.User != nil || base.RawQuery != "" || base.Fragment != "" || store == nil {
		return nil, contract.ErrInvalid
	}
	if len(cfg.Engines) == 0 {
		return nil, fmt.Errorf("%w: engines необходимо выбрать явно", contract.ErrInvalid)
	}
	for _, engine := range cfg.Engines {
		if !regexp.MustCompile(`^[a-zA-Z0-9_ -]{1,80}$`).MatchString(engine) {
			return nil, contract.ErrInvalid
		}
	}
	hc := http.Client{Timeout: 30 * time.Second}
	if cfg.HTTPClient != nil {
		hc = *cfg.HTTPClient
	}
	// Endpoint задаёт deployment, пользователь передаёт только q. Даже ответ
	// trusted endpoint не может перенаправить запрос/offer в другой origin.
	hc.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 || r.URL.Scheme != base.Scheme || r.URL.Host != base.Host {
			return http.ErrUseLastResponse
		}
		return nil
	}
	return &Client{store: store, baseURL: base.String(), http: &hc, engines: append([]string(nil), cfg.Engines...)}, nil
}
func (c *Client) Capabilities() contract.Capabilities {
	return contract.Capabilities{Provider: "searxng", Revision: "searxng-search-api-v1-research-1", Search: true, SearchLanguage: true}
}
func (c *Client) Fetch(context.Context, contract.FetchRequest) (contract.Receipt, error) {
	return contract.Receipt{}, contract.ErrUnsupported
}

var _ contract.Port = (*Client)(nil)
var _ contract.ReceiptReader = (*Client)(nil)

func (c *Client) Lookup(ctx context.Context, scope contract.Scope, id string, attempt int) (contract.Receipt, error) {
	return c.store.Lookup(ctx, scope, id, attempt)
}

func (c *Client) Search(ctx context.Context, r contract.SearchRequest) (contract.Receipt, error) {
	if err := r.Validate(); err != nil {
		return contract.Receipt{}, err
	}
	if r.Cursor != "" || r.Geography.RequireCountry || r.Geography.RequireEgress || r.Geography.RequireBrowserLocale {
		return contract.Receipt{}, contract.ErrUnsupported
	}
	if r.Geography.Language != "" && !regexp.MustCompile(`^[a-z]{2,3}(?:-[A-Za-z]{2,8}){0,2}$`).MatchString(r.Geography.Language) {
		return contract.Receipt{}, contract.ErrInvalid
	}
	if r.Geography.RequireLanguage && r.Geography.Language == "" {
		return contract.Receipt{}, contract.ErrInvalid
	}
	r.Geography.AppliedCountry = ""
	r.Geography.AppliedLanguage = ""
	r.Geography.ProviderClaimedEgressCountry = ""
	r.Geography.ObservedEgressCountry = ""
	r.Action.GrantID = "" // Никакой monetary grant не применяется к этому provider.
	lease, err := c.store.begin(ctx, r.Action, hashValue(r))
	if err != nil {
		return contract.Receipt{}, err
	}
	if lease.replay != nil {
		return *lease.replay, receiptError(*lease.replay)
	}
	out := contract.Receipt{Scope: r.Action.Scope, OperationID: lease.id, ActionID: r.Action.ActionID, Attempt: r.Action.Attempt, Provider: "searxng", ProviderRevision: c.Capabilities().Revision, ProfileRevision: r.Action.ProfileRevision, Status: "success", CostState: "not_reported", Geography: r.Geography}
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(r.Action.Limits.TimeoutSeconds)*time.Second)
	defer cancel()
	c.search(callCtx, r, &out)
	out.ObservedAt = time.Now().UTC()
	out.Persistence = "persisted"
	out.SHA256 = receiptHash(out)
	persistCtx, persistCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer persistCancel()
	if err = c.store.finish(persistCtx, r.Action, lease, out); err != nil {
		out.Persistence = "unknown"
		return out, fmt.Errorf("%w: receipt не сохранён: %v", contract.ErrUnknown, err)
	}
	return out, receiptError(out)
}

func receiptHash(r contract.Receipt) string {
	r.Persistence = ""
	r.SHA256 = ""
	return hashValue(r)
}

type resultJSON struct {
	URL     string   `json:"url"`
	Title   string   `json:"title"`
	Content string   `json:"content"`
	Engine  string   `json:"engine"`
	Engines []string `json:"engines"`
}
type searchJSON struct {
	Results      *[]resultJSON       `json:"results"`
	Unresponsive [][]json.RawMessage `json:"unresponsive_engines"`
}

func (c *Client) search(ctx context.Context, r contract.SearchRequest, out *contract.Receipt) {
	params := url.Values{"q": {r.Query}, "format": {"json"}, "categories": {"general"}, "engines": {strings.Join(c.engines, ",")}, "pageno": {"1"}}
	if r.Geography.Language != "" {
		params.Set("language", r.Geography.Language)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/search", strings.NewReader(params.Encode()))
	if err != nil {
		out.Status = "unavailable"
		out.ErrorCode = "request_invalid"
		return
	}
	// POST Search API — чтение, без отправки коммуникаций или внешней mutation.
	// Сырые query не попадают в access-log через URL query string.
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		out.Status = "unavailable"
		out.ErrorCode = "search_transport_error"
		if errors.Is(err, context.Canceled) {
			out.ErrorCode = "cancelled"
		}
		if errors.Is(err, context.DeadlineExceeded) {
			out.ErrorCode = "timeout"
		}
		return
	}
	defer resp.Body.Close()
	out.ProviderHTTPStatus = resp.StatusCode
	if resp.StatusCode == http.StatusTooManyRequests {
		out.Status = "throttled"
		out.ErrorCode = "provider_throttled"
		out.RetryAfterSeconds = retryAfter(resp.Header.Get("Retry-After"))
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		out.Status = "unavailable"
		out.ErrorCode = fmt.Sprintf("provider_http_%d", resp.StatusCode)
		if resp.StatusCode == 403 {
			out.ErrorCode = "json_disabled_or_access_denied"
		}
		return
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, r.Action.Limits.MaxBytes+1))
	if err != nil {
		out.Status = "unavailable"
		out.ErrorCode = "response_read_failed"
		return
	}
	if int64(len(raw)) > r.Action.Limits.MaxBytes {
		out.Status = "unavailable"
		out.ErrorCode = "response_limit"
		return
	}
	var decoded searchJSON
	if json.Unmarshal(raw, &decoded) != nil || decoded.Results == nil {
		out.Status = "unavailable"
		out.ErrorCode = "invalid_json_response"
		return
	}
	// AppliedLanguage означает применённый параметр нашего backend. Семантика
	// конкретного upstream engine и страна выдачи из этого не выводятся.
	out.Geography.AppliedLanguage = r.Geography.Language
	for _, failure := range decoded.Unresponsive {
		if len(failure) < 1 {
			continue
		}
		var engine, reason string
		_ = json.Unmarshal(failure[0], &engine)
		if len(failure) > 1 {
			_ = json.Unmarshal(failure[1], &reason)
		}
		if engine != "" {
			out.EngineFailures = append(out.EngineFailures, contract.EngineFailure{Engine: clip(engine, 80), Reason: clip(reason, 512)})
		}
	}
	seen := map[string]bool{}
	invalid := false
	for i, item := range *decoded.Results {
		u, e := url.Parse(item.URL)
		if e != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
			invalid = true
			continue
		}
		if seen[item.URL] {
			continue
		}
		seen[item.URL] = true
		if len(out.Hits) >= r.MaxResults {
			break
		}
		engines := append([]string(nil), item.Engines...)
		if item.Engine != "" {
			engines = append(engines, item.Engine)
		}
		sort.Strings(engines)
		dedup := engines[:0]
		for _, e := range engines {
			if e != "" && (len(dedup) == 0 || dedup[len(dedup)-1] != e) {
				dedup = append(dedup, e)
			}
		}
		out.Hits = append(out.Hits, contract.Hit{URL: item.URL, Title: plain(item.Title), Snippet: plain(item.Content), Rank: i + 1, Engines: dedup})
	}
	if len(out.EngineFailures) > 0 || invalid {
		out.Status = "partial"
		out.ErrorCode = "engine_partial_failure"
		if invalid {
			out.ErrorCode = "invalid_result_urls"
		}
		if len(out.Hits) == 0 {
			out.Status = "unavailable"
			out.ErrorCode = "no_engine_results"
		}
		return
	}
	if len(out.Hits) == 0 {
		out.Status = "empty"
	}
}
func receiptError(r contract.Receipt) error {
	switch r.Status {
	case "unavailable":
		return contract.ErrUnavailable
	case "throttled":
		return contract.ErrThrottled
	case "unknown":
		return contract.ErrUnknown
	}
	return nil
}

var tags = regexp.MustCompile(`<[^>]*>`)

func plain(s string) string {
	return strings.TrimSpace(html.UnescapeString(tags.ReplaceAllString(s, "")))
}
func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
func retryAfter(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		if at, e := http.ParseTime(s); e == nil {
			n = int(time.Until(at).Seconds())
		}
	}
	if n < 1 {
		return 1
	}
	if n > 300 {
		return 300
	}
	return n
}
