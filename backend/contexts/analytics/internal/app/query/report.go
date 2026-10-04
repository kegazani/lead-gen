package query

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"lead-gen/backend/contexts/analytics/internal/ports"
	"lead-gen/backend/contexts/analytics/internal/readmodel"
)

// ErrReportSourceUnavailable — контур клиента недоступен для расчёта. Это
// честный отказ, а не пустой отчёт: метрики не подменяются нулём.
var ErrReportSourceUnavailable = errors.New("query: контур клиента недоступен")

// ReportSource — источник рассчитанных агрегатов отчёта. В рабочем контуре это
// агрегация в контуре клиента (read-модель витрины); порт позволяет подставить
// удалённую реализацию, когда расчёт вынесен за пределы процесса.
type ReportSource interface {
	// Report считает агрегаты по фильтру. Ошибка означает недоступность
	// источника, а не пустой срез.
	Report(tenantID string, filter readmodel.ReportFilter) (readmodel.ReportData, error)
}

// ModelReportSource реализует ReportSource поверх read-модели витрины: агрегаты
// считаются в том же контуре, где живёт проекция. Записи пула наружу не отдаются.
type ModelReportSource struct {
	Model *readmodel.ReadModel
}

// Report реализует ReportSource. Nil-модель означает, что контур не собран, — это
// честная недоступность, а не пустой срез.
func (s ModelReportSource) Report(tenantID string, filter readmodel.ReportFilter) (readmodel.ReportData, error) {
	if s.Model == nil {
		return readmodel.ReportData{}, ErrReportSourceUnavailable
	}
	return s.Model.Report(tenantID, filter), nil
}

// ReportPeriod — разобранный период отчёта.
type ReportPeriod struct {
	From time.Time
	To   time.Time
}

// ReportQuery — запрос отчёта: тенант, роль и фильтры среза.
type ReportQuery struct {
	TenantID   string
	Role       string
	Period     ReportPeriod
	Segment    string
	Source     string
	CampaignID string
}

// ReportFilters — активные фильтры в пригодном для формы виде.
type ReportFilters struct {
	From       string
	To         string
	Segment    string
	Source     string
	CampaignID string
}

// ReportView — модель отчёта для экрана. Available == false означает, что
// источник недоступен и метрик нет; Stale == true — показаны последние
// рассчитанные метрики с явным временем.
type ReportView struct {
	TenantID string
	Role     string

	Available   bool
	Visible     bool
	Stale       bool
	StaleReason string

	MinGroup    int
	Records     int
	PeriodLabel string
	Filters     ReportFilters

	UpdatedAt      time.Time
	UpdatedAtLabel string

	// Data — агрегаты отчёта. Отдаются только счётчики и доли.
	Data readmodel.ReportData
}

// ReportHandler — use case сборки отчёта по read-модели с кэшем последнего
// успешного среза: при недоступности контура клиента возвращаются последние
// метрики с явным временем, а не нули.
type ReportHandler struct {
	source ReportSource
	clock  ports.Clock

	mu    sync.Mutex
	cache map[string]ReportView
}

// NewReportHandler собирает use case отчёта из источника агрегатов и часов.
func NewReportHandler(source ReportSource, clock ports.Clock) *ReportHandler {
	return &ReportHandler{source: source, clock: clock, cache: make(map[string]ReportView)}
}

// Handle собирает отчёт. При недоступности источника отдаётся последний
// успешный срез (Stale) либо честный отказ Available == false.
func (h *ReportHandler) Handle(_ context.Context, q ReportQuery) ReportView {
	view := ReportView{
		TenantID:    strings.TrimSpace(q.TenantID),
		Role:        q.Role,
		MinGroup:    readmodel.MinGroup,
		PeriodLabel: periodLabel(q.Period),
		Filters:     reportFilters(q),
	}
	if h == nil || h.source == nil {
		view.Stale = true
		view.StaleReason = degradeReason
		return view
	}

	filter := readmodel.ReportFilter{
		From:       q.Period.From,
		To:         q.Period.To,
		Segment:    q.Segment,
		Source:     q.Source,
		CampaignID: q.CampaignID,
	}
	data, err := h.source.Report(view.TenantID, filter)
	if err != nil {
		if cached, ok := h.cached(view.TenantID, q); ok {
			cached.Stale = true
			cached.StaleReason = degradeReason
			return cached
		}
		view.Stale = true
		view.StaleReason = degradeReason
		return view
	}

	now := h.now()
	view.Available = true
	view.Visible = data.Visible
	view.Records = data.Records
	view.Data = data
	view.UpdatedAt = now
	view.UpdatedAtLabel = formatUpdatedAt(now)
	h.store(view.TenantID, q, view)
	return view
}

// ExportRows собирает таблицу отчёта для выгрузки в CSV и XLSX. Срез ниже порога
// минимальной группы в выгрузку не попадает: вместо метрик записывается причина.
func ExportRows(view ReportView) [][]string {
	if !view.Available {
		return [][]string{
			{"Отчёт", "недоступен"},
			{"Причина", nonEmpty(view.StaleReason, "контур клиента недоступен")},
		}
	}
	if !view.Visible {
		return [][]string{
			{"Срез", "не показан"},
			{"Причина", nonEmpty(view.Data.HiddenReason, readmodel.HiddenReason)},
		}
	}

	rows := [][]string{
		{"Отчёт за период", view.PeriodLabel},
		{"Обновлено", view.UpdatedAtLabel},
	}
	data := view.Data

	rows = append(rows, []string{}, []string{"Кампании"},
		[]string{"Кампания", "Записей", "Отправлено", "Доставлено", "Переходы", "Ответы", "Отписки", "Отказы", "Неутв. черновики"})
	for _, campaign := range data.Campaigns {
		rows = append(rows, []string{
			campaign.CampaignID,
			strconv.Itoa(campaign.Records),
			strconv.Itoa(campaign.Sent),
			strconv.Itoa(campaign.Delivered),
			strconv.Itoa(campaign.Transitions),
			strconv.Itoa(campaign.Replies),
			strconv.Itoa(campaign.Unsubscribes),
			strconv.Itoa(campaign.Bounces),
			strconv.Itoa(campaign.Unapproved),
		})
	}

	rows = append(rows, []string{}, []string{"Лидогенерация по источникам"}, []string{"Источник", "Лидов"})
	for _, source := range data.Sources {
		rows = append(rows, []string{source.Label, strconv.Itoa(source.Count)})
	}
	rows = append(rows, []string{}, []string{"Лидогенерация по отраслям"}, []string{"Отрасль", "Лидов"})
	for _, industry := range data.Industries {
		rows = append(rows, []string{industry.Label, strconv.Itoa(industry.Count)})
	}
	rows = append(rows, []string{}, []string{"Лидогенерация по периодам"}, []string{"Дата", "Лидов"})
	for _, period := range data.Periods {
		rows = append(rows, []string{period.Date, strconv.Itoa(period.Count)})
	}

	rows = append(rows, []string{}, []string{"Воронка"}, []string{"Этап", "Количество", "Конверсия", "Доля"})
	for _, stage := range data.Funnel {
		rows = append(rows, []string{stage.Label, strconv.Itoa(stage.Count), stage.Conversion, strconv.Itoa(stage.Share) + "%"})
	}

	quality := data.Quality
	rows = append(rows, []string{}, []string{"Качество базы"}, []string{"Показатель", "Значение"},
		[]string{"Всего", strconv.Itoa(quality.Total)},
		[]string{"Валидные", quality.ValidShare},
		[]string{"Отказы", quality.BounceShare},
		[]string{"Дубли", quality.DuplicateShare})

	rows = append(rows, []string{}, []string{"Менеджеры"}, []string{"Менеджер", "Активность", "Объём", "Ответы", "Сделки", "Конверсия"})
	for _, manager := range data.Managers {
		rows = append(rows, []string{
			manager.Label,
			strconv.Itoa(manager.Activity),
			strconv.Itoa(manager.Volume),
			strconv.Itoa(manager.Replies),
			strconv.Itoa(manager.Deals),
			manager.Conversion,
		})
	}
	return rows
}

// degradeReason — сообщение честной деградации: показаны последние метрики с
// явным временем, отсутствующие значения нулём не подменяются.
const degradeReason = "контур клиента недоступен: показаны последние рассчитанные метрики"

func (h *ReportHandler) now() time.Time {
	if h.clock == nil {
		return time.Now()
	}
	return h.clock.Now()
}

// cacheKey различает срезы одного тенанта: разные фильтры хранят разные
// последние успешные метрики.
func cacheKey(tenantID string, q ReportQuery) string {
	parts := []string{
		tenantID,
		q.Period.From.UTC().Format(time.RFC3339),
		q.Period.To.UTC().Format(time.RFC3339),
		strings.ToLower(strings.TrimSpace(q.Segment)),
		strings.ToLower(strings.TrimSpace(q.Source)),
		strings.TrimSpace(q.CampaignID),
	}
	return strings.Join(parts, "|")
}

func (h *ReportHandler) cached(tenantID string, q ReportQuery) (ReportView, bool) {
	if h == nil {
		return ReportView{}, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	view, ok := h.cache[cacheKey(tenantID, q)]
	return view, ok
}

func (h *ReportHandler) store(tenantID string, q ReportQuery, view ReportView) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cache[cacheKey(tenantID, q)] = view
}

// periodLabel описывает период словами: пустой период — «весь период».
func periodLabel(period ReportPeriod) string {
	from := period.From
	to := period.To
	switch {
	case from.IsZero() && to.IsZero():
		return "весь период"
	case from.IsZero():
		return "по " + to.UTC().Format("2006-01-02")
	case to.IsZero():
		return "с " + from.UTC().Format("2006-01-02")
	default:
		return from.UTC().Format("2006-01-02") + " — " + to.UTC().Format("2006-01-02")
	}
}

// formatUpdatedAt форматирует время последнего обновления метрик.
func formatUpdatedAt(at time.Time) string {
	if at.IsZero() {
		return "нет данных"
	}
	return at.UTC().Format("2006-01-02 15:04:05") + " UTC"
}

// reportFilters собирает активные фильтры в пригодном для формы виде.
func reportFilters(q ReportQuery) ReportFilters {
	filters := ReportFilters{
		Segment:    strings.TrimSpace(q.Segment),
		Source:     strings.TrimSpace(q.Source),
		CampaignID: strings.TrimSpace(q.CampaignID),
	}
	if !q.Period.From.IsZero() {
		filters.From = q.Period.From.UTC().Format("2006-01-02")
	}
	if !q.Period.To.IsZero() {
		filters.To = q.Period.To.UTC().Format("2006-01-02")
	}
	return filters
}

// nonEmpty возвращает value, если оно непустое, иначе fallback.
func nonEmpty(value, fallback string) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return trimmed
	}
	return fallback
}
