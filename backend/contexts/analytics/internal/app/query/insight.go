package query

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"lead-gen/backend/contexts/analytics/internal/ports"
	"lead-gen/backend/contexts/analytics/internal/readmodel"
)

// ErrInsightSourceUnavailable — источник инсайтов недоступен для расчёта. Это
// честная недоступность, а не пустой срез: метрики не подменяются нулём.
var ErrInsightSourceUnavailable = errors.New("query: источник инсайтов недоступен")

// InsightSource — источник агрегатов качества поиска. В рабочем контуре это
// агрегация по проекции инсайтов; порт позволяет подставить удалённую
// реализацию, когда расчёт вынесен за пределы процесса.
type InsightSource interface {
	// Insight считает агрегаты по фильтру. Ошибка означает недоступность
	// источника, а не пустой срез.
	Insight(tenantID string, filter readmodel.InsightFilter) (readmodel.InsightData, error)
}

// ModelInsightSource реализует InsightSource поверх проекции качества поиска.
type ModelInsightSource struct {
	Model *readmodel.InsightModel
}

// Insight реализует InsightSource. Nil-модель означает, что контур не собран, —
// это честная недоступность, а не пустой срез.
func (s ModelInsightSource) Insight(tenantID string, filter readmodel.InsightFilter) (readmodel.InsightData, error) {
	if s.Model == nil {
		return readmodel.InsightData{}, ErrInsightSourceUnavailable
	}
	return s.Model.Insight(tenantID, filter), nil
}

// InsightFilters — активные фильтры среза в пригодном для формы виде.
type InsightFilters struct {
	JobID    string
	Source   string
	Industry string
	From     string
	To       string
}

// InsightQuery — запрос среза качества поиска.
type InsightQuery struct {
	TenantID string
	Role     string
	Filter   readmodel.InsightFilter
	Filters  InsightFilters
}

// InsightView — модель среза качества поиска для экрана. Available == false
// означает, что источник недоступен и метрик нет (нулём не подменяются);
// Visible == false — срез скрыт порогом минимальной группы либо нет наблюдений.
type InsightView struct {
	TenantID string
	Role     string

	Available   bool
	Visible     bool
	HasData     bool
	Stale       bool
	StaleReason string
	// Reason — сообщаемая причина: недоступность, скрытие или отсутствие данных.
	Reason string

	MinGroup    int
	PeriodLabel string
	Filters     InsightFilters

	UpdatedAt      time.Time
	UpdatedAtLabel string

	Data readmodel.InsightData
}

// InsightHandler — use case сборки среза качества поиска с кэшем последнего
// успешного среза: при недоступности источника отдаётся последний срез с явным
// временем, а не нули.
type InsightHandler struct {
	source InsightSource
	clock  ports.Clock

	mu    sync.Mutex
	cache map[string]InsightView
}

// NewInsightHandler собирает use case среза качества поиска.
func NewInsightHandler(source InsightSource, clock ports.Clock) *InsightHandler {
	return &InsightHandler{source: source, clock: clock, cache: make(map[string]InsightView)}
}

// Handle собирает срез. При недоступности источника отдаётся последний успешный
// срез (Stale) либо честный отказ Available == false.
func (h *InsightHandler) Handle(_ context.Context, q InsightQuery) InsightView {
	view := InsightView{
		TenantID:    strings.TrimSpace(q.TenantID),
		Role:        q.Role,
		MinGroup:    readmodel.InsightMinGroup,
		PeriodLabel: insightPeriodLabel(q.Filter),
		Filters:     q.Filters,
	}
	if h == nil || h.source == nil {
		view.Stale = true
		view.StaleReason = insightDegradeReason
		view.Reason = insightDegradeReason
		return view
	}

	data, err := h.source.Insight(view.TenantID, q.Filter)
	if err != nil {
		if cached, ok := h.cached(view.TenantID, q); ok {
			cached.Stale = true
			cached.StaleReason = insightDegradeReason
			cached.Reason = insightDegradeReason
			return cached
		}
		view.Stale = true
		view.StaleReason = insightDegradeReason
		view.Reason = insightDegradeReason
		return view
	}

	now := h.now()
	view.Available = true
	view.Visible = data.Visible
	view.HasData = data.HasData
	view.Data = data
	if !data.Visible {
		view.Reason = data.HiddenReason
	}
	view.UpdatedAt = now
	view.UpdatedAtLabel = insightUpdatedAtLabel(now)
	h.store(view.TenantID, q, view)
	return view
}

const insightDegradeReason = "источник инсайтов недоступен: показан последний рассчитанный срез"

func (h *InsightHandler) now() time.Time {
	if h.clock == nil {
		return time.Now()
	}
	return h.clock.Now()
}

func insightCacheKey(tenantID string, q InsightQuery) string {
	return strings.Join([]string{
		tenantID,
		strings.TrimSpace(q.Filters.JobID),
		strings.ToLower(strings.TrimSpace(q.Filters.Source)),
		strings.ToLower(strings.TrimSpace(q.Filters.Industry)),
		q.Filters.From,
		q.Filters.To,
	}, "|")
}

func (h *InsightHandler) cached(tenantID string, q InsightQuery) (InsightView, bool) {
	if h == nil {
		return InsightView{}, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	view, ok := h.cache[insightCacheKey(tenantID, q)]
	return view, ok
}

func (h *InsightHandler) store(tenantID string, q InsightQuery, view InsightView) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cache[insightCacheKey(tenantID, q)] = view
}

// insightPeriodLabel описывает период словами.
func insightPeriodLabel(filter readmodel.InsightFilter) string {
	switch {
	case filter.From.IsZero() && filter.To.IsZero():
		return "весь период"
	case filter.From.IsZero():
		return "по " + filter.To.UTC().Format("2006-01-02")
	case filter.To.IsZero():
		return "с " + filter.From.UTC().Format("2006-01-02")
	default:
		return filter.From.UTC().Format("2006-01-02") + " — " + filter.To.UTC().Format("2006-01-02")
	}
}

// insightUpdatedAtLabel форматирует время последнего обновления среза.
func insightUpdatedAtLabel(at time.Time) string {
	if at.IsZero() {
		return "нет данных"
	}
	return at.UTC().Format("2006-01-02 15:04:05") + " UTC"
}

// ApplicationQuery — запрос сравнения применённых рекомендаций.
type ApplicationQuery struct {
	TenantID string
	// ScopeKind, ScopeKey — необязательное сужение до одного среза.
	ScopeKind string
	ScopeKey  string
}

// ApplicationComparisonView — сравнение показателей по применённым рекомендациям.
type ApplicationComparisonView struct {
	TenantID string
	// Available — хранилище применений доступно; при недоступности сравнение не
	// строится, а не отдаётся пустым.
	Available bool
	Reason    string

	Comparisons []readmodel.ApplicationComparison
}

// ApplicationsHandler — use case чтения применённых рекомендаций со сравнением
// «до/после» по текущему срезу.
type ApplicationsHandler struct {
	store  ports.InsightStore
	source InsightSource
}

// NewApplicationsHandler собирает use case сравнения применений.
func NewApplicationsHandler(store ports.InsightStore, source InsightSource) *ApplicationsHandler {
	return &ApplicationsHandler{store: store, source: source}
}

// Handle возвращает сравнения применённых рекомендаций тенанта. Отсутствие
// хранилища — честная недоступность (`AGENTS.md` §3), а не пустой список.
func (h *ApplicationsHandler) Handle(ctx context.Context, q ApplicationQuery) ApplicationComparisonView {
	view := ApplicationComparisonView{TenantID: strings.TrimSpace(q.TenantID)}
	if h == nil || h.store == nil || h.source == nil {
		view.Reason = "сравнение применений недоступно: хранилище применений не подключено"
		return view
	}
	applications, err := h.store.LoadApplications(ctx)
	if err != nil {
		view.Reason = "сравнение применений недоступно: " + err.Error()
		return view
	}
	view.Available = true

	for _, application := range applications {
		if application.TenantID != view.TenantID {
			continue
		}
		if q.ScopeKind != "" && application.ScopeKind != q.ScopeKind {
			continue
		}
		if q.ScopeKey != "" && application.ScopeKey != q.ScopeKey {
			continue
		}
		current, err := h.source.Insight(application.TenantID, scopeFilter(application.ScopeKind, application.ScopeKey))
		if err != nil {
			// Источник недоступен: сравнение не выдумывается.
			view.Available = false
			view.Reason = "сравнение применений недоступно: " + err.Error()
			return view
		}
		view.Comparisons = append(view.Comparisons, readmodel.CompareApplication(application, current))
	}
	return view
}

// scopeFilter переводит измерение применения в фильтр среза.
func scopeFilter(scopeKind, scopeKey string) readmodel.InsightFilter {
	switch scopeKind {
	case "job":
		return readmodel.InsightFilter{JobID: scopeKey}
	case "source":
		return readmodel.InsightFilter{Source: scopeKey}
	case "industry":
		return readmodel.InsightFilter{Industry: scopeKey}
	default:
		return readmodel.InsightFilter{}
	}
}
