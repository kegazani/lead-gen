// Package researchacquisition задаёт технический контракт получения поисковых
// результатов и страниц. Сырые query/URL/contents передаются только в клиентском
// контуре; наличие результата ещё не подтверждает компанию или её контакт.
package researchacquisition

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

var (
	ErrAttemptLimit    = errors.New("researchacquisition: исчерпан технический предел попыток")
	ErrUnavailable     = errors.New("researchacquisition: источник недоступен")
	ErrInvalid         = errors.New("researchacquisition: некорректный запрос")
	ErrUnsupported     = errors.New("researchacquisition: требуемая возможность недоступна")
	ErrBudgetExhausted = errors.New("researchacquisition: исчерпан лимит Firecrawl")
	ErrUnknown         = errors.New("researchacquisition: внешний исход неизвестен")
	ErrInFlight        = errors.New("researchacquisition: действие уже выполняется")
	ErrConflict        = errors.New("researchacquisition: действие изменено при повторе")
	ErrThrottled       = errors.New("researchacquisition: технический лимит источника")
	ErrNotFound        = errors.New("researchacquisition: запись не найдена")
)

type Scope struct {
	TenantID  string `json:"tenant_id"`
	PurposeID string `json:"purpose_id"`
}

func (s Scope) Validate() error {
	if strings.TrimSpace(s.TenantID) == "" || strings.TrimSpace(s.PurposeID) == "" {
		return ErrInvalid
	}
	return nil
}

// Capabilities различает регион запроса и фактически наблюдённую географию.
// В частности Firecrawl v1 не обещает HTML, DOM, country или cursor.
type Capabilities struct {
	Provider        string   `json:"provider"`
	Revision        string   `json:"revision"`
	Search          bool     `json:"search"`
	Fetch           bool     `json:"fetch"`
	Representations []string `json:"representations"`
	SearchCountry   bool     `json:"search_country"`
	SearchLanguage  bool     `json:"search_language"`
	ObservedEgress  bool     `json:"observed_egress"`
	Cursor          bool     `json:"cursor"`
}

type Limits struct {
	TimeoutSeconds int   `json:"timeout_seconds"`
	MaxBytes       int64 `json:"max_bytes"`
}

func (l Limits) Validate() error {
	if l.TimeoutSeconds < 1 || l.TimeoutSeconds > 300 || l.MaxBytes < 1 || l.MaxBytes > 16<<20 {
		return ErrInvalid
	}
	return nil
}

type Action struct {
	Scope           Scope  `json:"scope"`
	ActionID        string `json:"action_id"`
	Attempt         int    `json:"attempt"`
	GrantID         string `json:"grant_id"`
	ProfileRevision string `json:"profile_revision"`
	Limits          Limits `json:"limits"`
}

func (a Action) Validate() error {
	if a.Scope.Validate() != nil || a.ActionID == "" || a.Attempt < 1 || a.ProfileRevision == "" || a.Limits.Validate() != nil {
		return ErrInvalid
	}
	return nil
}

type Geography struct {
	TargetCountry                string `json:"target_country,omitempty"`
	RequestedCountry             string `json:"requested_country,omitempty"`
	Language                     string `json:"language,omitempty"`
	RequireCountry               bool   `json:"require_country,omitempty"`
	RequireLanguage              bool   `json:"require_language,omitempty"`
	AppliedCountry               string `json:"applied_country,omitempty"`
	AppliedLanguage              string `json:"applied_language,omitempty"`
	RequestedEgressCountry       string `json:"requested_egress_country,omitempty"`
	RequireEgress                bool   `json:"require_egress,omitempty"`
	BrowserLocale                string `json:"browser_locale,omitempty"`
	RequireBrowserLocale         bool   `json:"require_browser_locale,omitempty"`
	ProviderClaimedEgressCountry string `json:"provider_claimed_egress_country,omitempty"`
	ObservedEgressCountry        string `json:"observed_egress_country,omitempty"`
}

type SearchRequest struct {
	Action     Action    `json:"action"`
	Query      string    `json:"query"`
	MaxResults int       `json:"max_results"`
	Geography  Geography `json:"geography"`
	Cursor     string    `json:"cursor,omitempty"`
}

func (r SearchRequest) Validate() error {
	if r.Action.Validate() != nil || strings.TrimSpace(r.Query) == "" || len(r.Query) > 8192 || r.MaxResults < 1 || r.MaxResults > 100 {
		return ErrInvalid
	}
	return nil
}

type FetchRequest struct {
	Action         Action    `json:"action"`
	URL            string    `json:"url"`
	Representation string    `json:"representation"`
	Geography      Geography `json:"geography"`
}

func (r FetchRequest) Validate() error {
	if r.Action.Validate() != nil || r.Representation == "" {
		return ErrInvalid
	}
	u, e := url.Parse(r.URL)
	if e != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
		return ErrInvalid
	}
	return nil
}

type Hit struct {
	Engines []string `json:"engines,omitempty"`
	URL     string   `json:"url"`
	Title   string   `json:"title"`
	Snippet string   `json:"snippet"`
	Rank    int      `json:"rank"`
}

// Artifact.URL — адрес источника, сообщённый provider (либо RequestedURL,
// если upstream его не вернул); это не доказательство конечного redirect URL.
// Redirect trace и DOM текущий Firecrawl v1 адаптер не объявляет.
type Artifact struct {
	RequestedURL   string            `json:"requested_url"`
	URL            string            `json:"url"`
	Representation string            `json:"representation"`
	Content        string            `json:"content"`
	SHA256         string            `json:"sha256"`
	ObservedAt     time.Time         `json:"observed_at"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

// EngineFailure — диагностический исход upstream engine, а не оценка компании.
type EngineFailure struct {
	Engine string `json:"engine"`
	Reason string `json:"reason"`
}
type Receipt struct {
	// Persistence описывает подтверждённую фиксацию результата/исхода в owner
	// store: persisted либо unknown. Поле не входит в immutable payload SHA256.
	Persistence               string          `json:"persistence,omitempty"`
	EngineFailures            []EngineFailure `json:"engine_failures,omitempty"`
	Scope                     Scope           `json:"scope"`
	OperationID               string          `json:"operation_id"`
	ActionID                  string          `json:"action_id"`
	Attempt                   int             `json:"attempt"`
	Provider                  string          `json:"provider"`
	ProviderRevision          string          `json:"provider_revision"`
	ProfileRevision           string          `json:"profile_revision"`
	Status                    string          `json:"status"`
	ErrorCode                 string          `json:"error_code,omitempty"`
	ProviderHTTPStatus        int             `json:"provider_http_status,omitempty"`
	ProviderRemainingCredits  *int            `json:"provider_remaining_credits,omitempty"`
	ProviderBalanceReceivedAt *time.Time      `json:"provider_balance_received_at,omitempty"`
	Hits                      []Hit           `json:"hits,omitempty"`
	Artifacts                 []Artifact      `json:"artifacts,omitempty"`
	Geography                 Geography       `json:"geography"`
	ObservedAt                time.Time       `json:"observed_at"`
	CreditsReserved           int             `json:"credits_reserved"`
	CreditsCharged            int             `json:"credits_charged"`
	CostState                 string          `json:"cost_state"`
	CostBasis                 string          `json:"cost_basis,omitempty"`
	RetryAfterSeconds         int             `json:"retry_after_seconds,omitempty"`
	SHA256                    string          `json:"sha256"`
}

type Port interface {
	Capabilities() Capabilities
	Search(context.Context, SearchRequest) (Receipt, error)
	Fetch(context.Context, FetchRequest) (Receipt, error)
}

// AccountPolicy — только Firecrawl. Нулевой cap означает отсутствие явно
// заданного денежного потолка; никакой скрытый лимит не подставляется.
type AccountPolicy struct {
	AccountID  string `json:"account_id"`
	MaxCredits int    `json:"max_credits"`
}
type Grant struct {
	Scope      Scope  `json:"scope"`
	ID         string `json:"id"`
	AccountID  string `json:"account_id"`
	IntentID   string `json:"intent_id"`
	IntentKind string `json:"intent_kind"`
	MaxCredits int    `json:"max_credits"`
}

func (g Grant) Validate() error {
	if g.Scope.Validate() != nil || g.ID == "" || g.AccountID == "" || g.IntentID == "" || (g.IntentKind != "mission" && g.IntentKind != "market_study") || g.MaxCredits < 0 {
		return fmt.Errorf("%w: grant", ErrInvalid)
	}
	return nil
}

type Balance struct {
	MaxCredits      int `json:"max_credits"`
	SpentCredits    int `json:"spent_credits"`
	ReservedCredits int `json:"reserved_credits"`
	UnknownCredits  int `json:"unknown_credits"`
}
type GrantSnapshot struct {
	Grant      Grant   `json:"grant"`
	Account    Balance `json:"account"`
	Allocation Balance `json:"allocation"`
}
type Reconciliation struct {
	Scope         Scope
	GrantID       string
	OperationID   string
	ActualCredits int
	EvidenceRef   string
	Actor         string
}
type Ledger interface {
	EnsureAccount(context.Context, AccountPolicy) error
	EnsureGrant(context.Context, Grant) error
	Snapshot(context.Context, Scope, string) (GrantSnapshot, error)
	Reconcile(context.Context, Reconciliation) error
}

// ReceiptReader позволяет восстановить результат после сбоя worker без нового
// внешнего вызова. ErrNotFound означает отсутствие committed attempt, а не
// отсутствие компании в интернете.
type ReceiptReader interface {
	Lookup(context.Context, Scope, string, int) (Receipt, error)
}
