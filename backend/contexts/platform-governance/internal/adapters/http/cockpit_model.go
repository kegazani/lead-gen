package httpapi

import (
	"html/template"
	"net/url"
	"sort"
	"strings"
	"time"

	"lead-gen/backend/platform/observability/prometheus"
	"lead-gen/backend/platform/workflow"
	"lead-gen/frontend/components"
)

// numberCell — цифра процесса: подпись, значение, маркер и переход в срез. Смысл
// цифры не передаётся только цветом: у клетки есть текстовая подпись и форма
// маркера (спека operations-cockpit, «Доступность и предпочтения движения»).
type numberCell struct {
	Key   string
	Label string
	Count int
	Class string
	Icon  string
	URL   string
}

// clientNumbersRow — разбивка цифр процесса по клиенту.
type clientNumbersRow struct {
	ClientID string
	Cells    []numberCell
}

// periodRowView — строка разбивки по времени: день и цифры категорий с временным
// измерением (найдено/обработано/отклонено). Категории без временного ряда
// (в работе, результат) в разбивку не входят по построению владельца.
type periodRowView struct {
	Date      string
	Found     int
	Processed int
	Rejected  int
}

type cockpitNumbersView struct {
	Unavailable bool
	Notice      string
	Total       []numberCell
	HasNumbers  bool
	Clients     []clientNumbersRow
	Periods     []periodRowView
	HasPeriods  bool
	Empty       bool
	Stale       bool
	Actor       string
	TenantID    string
}

// agentRowView — строка живой ленты агентов.
type agentRowView struct {
	RunID      string
	ClientID   string
	Company    string
	StateLabel string
	StateClass string
	StateIcon  string
	Step       string
	UpdatedAt  time.Time
	DetailURL  string
}

// cockpitAgentsView — модель живой ленты агентов (6.2).
type cockpitAgentsView struct {
	Unavailable bool
	Notice      string
	Rows        []agentRowView
	Empty       bool
	Live        components.Live
	FeedURL     string
	Actor       string
	TenantID    string
}

// agentEventView — событие агента.
type agentEventView struct {
	At           time.Time
	Step         string
	KindLabel    string
	OutcomeLabel string
	OutcomeClass string
	OutcomeIcon  string
	ReasonLabel  string
}

// agentReasonView — нормализованная причина отказа.
type agentReasonView struct {
	Code  string
	Label string
	Count int
}

// agentJobView — связанное задание.
type agentJobView struct {
	ID         string
	KindLabel  string
	StateLabel string
	StateClass string
}

// agentDetailView — подробности агента/шага.
type agentDetailView struct {
	RunID      string
	ClientID   string
	Company    string
	StateLabel string
	StateClass string
	StateIcon  string
	Events     []agentEventView
	Reasons    []agentReasonView
	Jobs       []agentJobView
}

// cockpitAgentView — модель экрана подробностей (6.3).
type cockpitAgentView struct {
	Unavailable bool
	Found       bool
	Missing     string
	Detail      agentDetailView
	BackURL     string
	Stale       bool
	Actor       string
	TenantID    string
}

// processNodeView — узел графа процессов.
type processNodeView struct {
	ID         string
	Label      string
	Detail     string
	StateLabel string
	StateClass string
	StateIcon  string
	BoxX       float64
	BoxY       float64
	LabelX     float64
	LabelY     float64
	StateY     float64
}

// processEdgeView — ребро графа процессов.
type processEdgeView struct {
	ID string
	X1 float64
	Y1 float64
	X2 float64
	Y2 float64
}

// processExecutionView — строка списка исполнений.
type processExecutionView struct {
	WorkflowID  string
	RunID       string
	Type        string
	StatusLabel string
	StatusClass string
	StatusIcon  string
	StartLabel  string
	URL         string
	Selected    bool
}

// timelineItemView — шаг или ожидание выбранного исполнения.
type timelineItemView struct {
	At         time.Time
	Name       string
	KindLabel  string
	StateLabel string
	StateClass string
	StateIcon  string
}

// cockpitProcessView — модель экрана графа процессов Temporal (6.4).
type cockpitProcessView struct {
	Unavailable   bool
	Notice        string
	HasNodes      bool
	GeneratedAt   time.Time
	ViewBox       string
	Nodes         []processNodeView
	Edges         []processEdgeView
	Executions    []processExecutionView
	Timeline      []timelineItemView
	HasTimeline   bool
	SelectedID    string
	TimelineBox   string
	TimelineEdges []processEdgeView
	TimelineNodes []processNodeView
	FeedURL       string
	Actor         string
	TenantID      string
}

// dashboardRowView — строка дашборда по клиенту.
type dashboardRowView struct {
	ClientID      string
	Cells         []numberCell
	AnomalyLabels []string
}

// cockpitDashboardView — модель полноэкранного дашборда по всем клиентам (6.5).
type cockpitDashboardView struct {
	Unavailable bool
	Notice      string
	Threshold   int
	Headers     []string
	Rows        []dashboardRowView
	Empty       bool
	BackURL     string
	Stale       bool
	Actor       string
	TenantID    string
}

// panelView — панель экрана: объявление, тело и управление видимостью.
type panelView struct {
	ID           string
	Title        string
	SourceLabel  string
	RefreshLabel string
	Available    bool
	Hidden       bool
	ReasonLabel  string
	Body         template.HTML
	ToggleURL    string
	ToggleLabel  string
}

// cockpitPanelsView — расширяемые панели операционного экрана (5.4).
type cockpitPanelsView struct {
	Panels []panelView
}

// cockpitOperationsView — модель экрана операций: цифры процесса (6.1) и
// расширяемые панели (5.4) с токеном CSRF для изменяющих форм.
type cockpitOperationsView struct {
	Numbers  cockpitNumbersView
	Requests cockpitRequestsView
	Panels   cockpitPanelsView
	CSRF     string
	Actor    string
	TenantID string
}

type cockpitRequestRow struct {
	RequestID      string
	DomainStatus   string
	WorkflowID     string
	RunID          string
	Phase          string
	ExecutionFound bool
	Found          int
	Target         int
}

type cockpitRequestsView struct {
	Unavailable bool
	Empty       bool
	Rows        []cockpitRequestRow
}

// categoryLabel возвращает подпись цифры процесса.
func (s *Server) categoryLabel(category string) string {
	switch category {
	case CategoryFound:
		return s.messages.Text(msgCockpitCategoryFound)
	case CategoryProcessed:
		return s.messages.Text(msgCockpitCategoryProcessed)
	case CategoryRejected:
		return s.messages.Text(msgCockpitCategoryRejected)
	case CategoryInProgress:
		return s.messages.Text(msgCockpitCategoryInProgress)
	case CategoryResult:
		return s.messages.Text(msgCockpitCategoryResult)
	default:
		return category
	}
}

// categoryStateClass сопоставляет категории класс общего бейджа.
func categoryStateClass(category string) string {
	switch category {
	case CategoryFound:
		return "queued"
	case CategoryProcessed:
		return "done"
	case CategoryRejected:
		return "failed"
	case CategoryInProgress:
		return "running"
	case CategoryResult:
		return "active"
	default:
		return "queued"
	}
}

// categoryIcon возвращает маркер категории: смысл не только цветом.
func (s *Server) categoryIcon(category string) string {
	switch category {
	case CategoryFound:
		return s.messages.Text(msgCockpitCategoryIconFound)
	case CategoryProcessed:
		return s.messages.Text(msgCockpitCategoryIconProcessed)
	case CategoryRejected:
		return s.messages.Text(msgCockpitCategoryIconRejected)
	case CategoryInProgress:
		return s.messages.Text(msgCockpitCategoryIconInProgress)
	case CategoryResult:
		return s.messages.Text(msgCockpitCategoryIconResult)
	default:
		return ""
	}
}

// agentStateLabel возвращает подпись состояния агента. Неизвестное состояние
// показывается как есть: экран не выдумывает смысл.
func (s *Server) agentStateLabel(state string) string {
	switch state {
	case "running", "in_progress":
		return s.messages.Text(msgCockpitStateRunning)
	case "completed", "done", "success":
		return s.messages.Text(msgCockpitStateCompleted)
	case "failed", "error":
		return s.messages.Text(msgCockpitStateFailed)
	case "queued", "pending":
		return s.messages.Text(msgCockpitStateQueued)
	case "waiting", "awaiting":
		return s.messages.Text(msgCockpitStateWaiting)
	case "awaiting-approval":
		return s.messages.Text(msgCockpitStateAwaitingApproval)
	case "blocked":
		return s.messages.Text(msgCockpitStateBlocked)
	case "needs-attention":
		return s.messages.Text(msgCockpitStateNeedsAttention)
	case "stopped":
		return s.messages.Text(msgCockpitStateStopped)
	case "":
		return s.messages.Text(msgCockpitStateUnknown)
	default:
		return state
	}
}

// agentStateClass сопоставляет состояние агента классу бейджа.
func agentStateClass(state string) string {
	switch state {
	case "running", "in_progress":
		return "running"
	case "completed", "done", "success":
		return "done"
	case "failed", "error", "blocked", "needs-attention":
		return "failed"
	case "queued", "pending", "waiting", "awaiting", "awaiting-approval", "stopped":
		return "queued"
	default:
		return "queued"
	}
}

// agentStateIcon возвращает маркер состояния: смысл не только цветом.
func (s *Server) agentStateIcon(state string) string {
	switch state {
	case "running", "in_progress":
		return s.messages.Text(msgCockpitStateIconRunning)
	case "completed", "done", "success":
		return s.messages.Text(msgCockpitStateIconCompleted)
	case "failed", "error":
		return s.messages.Text(msgCockpitStateIconFailed)
	case "blocked", "needs-attention":
		return s.messages.Text(msgCockpitStateIconFailed)
	case "queued", "pending", "waiting", "awaiting":
		return s.messages.Text(msgCockpitStateIconQueued)
	case "awaiting-approval":
		return s.messages.Text(msgCockpitStateIconAwaitingApproval)
	case "stopped":
		return s.messages.Text(msgCockpitStateIconStopped)
	default:
		return s.messages.Text(msgCockpitStateIconUnknown)
	}
}

// outcomeLabel возвращает подпись исхода шага. Отказ не маскируется под успех.
func (s *Server) outcomeLabel(outcome string) string {
	switch outcome {
	case workflow.StepScheduled:
		return s.messages.Text(msgCockpitOutcomeScheduled)
	case workflow.StepCompleted:
		return s.messages.Text(msgCockpitOutcomeCompleted)
	case workflow.StepFailed:
		return s.messages.Text(msgCockpitOutcomeFailed)
	case workflow.StepTimedOut:
		return s.messages.Text(msgCockpitOutcomeTimedOut)
	case workflow.StepCanceled:
		return s.messages.Text(msgCockpitOutcomeCanceled)
	case workflow.WaitStarted:
		return s.messages.Text(msgCockpitStateWaitStarted)
	case workflow.WaitFired:
		return s.messages.Text(msgCockpitStateWaitFired)
	case workflow.WaitReceived:
		return s.messages.Text(msgCockpitStateWaitReceived)
	case "unavailable":
		return s.messages.Text(msgCockpitOutcomeUnavailable)
	case "":
		return s.messages.Text(msgCockpitStateUnknown)
	default:
		return outcome
	}
}

// outcomeClass сопоставляет исход классу бейджа.
func outcomeClass(outcome string) string {
	switch outcome {
	case workflow.StepCompleted, workflow.WaitFired, workflow.WaitReceived:
		return "done"
	case workflow.StepFailed, workflow.StepTimedOut:
		return "failed"
	case workflow.StepScheduled, workflow.WaitStarted:
		return "running"
	case workflow.StepCanceled:
		return "queued"
	case "unavailable":
		return "queued"
	default:
		return "queued"
	}
}

// kindLabel возвращает подпись вида шага или ожидания.
func (s *Server) stepKindLabel(kind string) string {
	switch kind {
	case workflow.StepKindActivity:
		return s.messages.Text(msgCockpitKindActivity)
	case workflow.WaitKindTimer:
		return s.messages.Text(msgCockpitKindTimer)
	case workflow.WaitKindSignal:
		return s.messages.Text(msgCockpitKindSignal)
	case "":
		return s.messages.Text(msgCockpitStateUnknown)
	default:
		return kind
	}
}

// reasonLabel переводит нормализованный код причины в подпись. Свободный текст
// владельца в подпись не переносится.
func (s *Server) reasonLabel(code string) string {
	switch code {
	case "":
		return s.messages.Text(msgCockpitReasonNone)
	case "not_relevant", "irrelevant":
		return s.messages.Text(msgCockpitReasonNotRelevant)
	case "no_contact":
		return s.messages.Text(msgCockpitReasonNoContact)
	case "extraction_error":
		return s.messages.Text(msgCockpitReasonExtractionError)
	case "timeout":
		return s.messages.Text(msgCockpitReasonTimeout)
	case "provider_unavailable":
		return s.messages.Text(msgCockpitReasonProviderUnavailable)
	default:
		return code
	}
}

// processStatusLabel возвращает подпись статуса исполнения.
func (s *Server) processStatusLabel(status string) string {
	switch status {
	case workflow.ProcessRunning:
		return s.messages.Text(msgCockpitStateRunning)
	case workflow.ProcessCompleted:
		return s.messages.Text(msgCockpitStateCompleted)
	case workflow.ProcessFailed:
		return s.messages.Text(msgCockpitStateFailed)
	case workflow.ProcessCanceled:
		return s.messages.Text(msgCockpitProcessCanceled)
	case workflow.ProcessTerminated:
		return s.messages.Text(msgCockpitProcessTerminated)
	case workflow.ProcessContinued:
		return s.messages.Text(msgCockpitProcessContinued)
	case workflow.ProcessTimedOut:
		return s.messages.Text(msgCockpitProcessTimedOut)
	default:
		return s.messages.Text(msgCockpitStateUnknown)
	}
}

// processStatusIcon возвращает маркер статуса исполнения.
func (s *Server) processStatusIcon(status string) string {
	switch status {
	case workflow.ProcessRunning:
		return s.messages.Text(msgCockpitStateIconRunning)
	case workflow.ProcessCompleted:
		return s.messages.Text(msgCockpitStateIconCompleted)
	case workflow.ProcessFailed, workflow.ProcessTimedOut:
		return s.messages.Text(msgCockpitStateIconFailed)
	default:
		return s.messages.Text(msgCockpitStateIconQueued)
	}
}

// requestDomainStatusLabel сохраняет смысл доменного статуса заявки. Статусы
// исполнения workflow сюда не передаются и не могут обозначить «Выполнена».
func (s *Server) requestDomainStatusLabel(status string) string {
	switch status {
	case "submitted", "validating", "validated":
		return s.messages.Text(msgCockpitRequestCreated)
	case "processing":
		return s.messages.Text(msgCockpitRequestProcessing)
	case "cancelled":
		return s.messages.Text(msgCockpitRequestCancelled)
	case "fulfilled":
		return s.messages.Text(msgCockpitRequestFulfilled)
	case "rejected":
		return s.messages.Text(msgCockpitRequestRejected)
	case "needs-revision":
		return s.messages.Text(msgCockpitRequestNeedsRevision)
	case "failed":
		return s.messages.Text(msgCockpitRequestFailed)
	default:
		return status
	}
}

// buildNumberCells собирает клетки цифр процесса с переходами в срез.
func (s *Server) buildNumberCells(numbers ClientNumbers, tenant, actor, clientID string) []numberCell {
	cells := make([]numberCell, 0, len(processCategories))
	for _, category := range processCategories {
		cells = append(cells, numberCell{
			Key:   category,
			Label: s.categoryLabel(category),
			Count: numbers.value(category),
			Class: categoryStateClass(category),
			Icon:  s.categoryIcon(category),
			URL:   agentsURL(tenant, actor, clientID, category, 0),
		})
	}
	return cells
}

// buildAgentRows собирает строки ленты из данных провайдера.
func (s *Server) buildAgentRows(rows []AgentRow, tenant, actor string) []agentRowView {
	out := make([]agentRowView, 0, len(rows))
	for _, row := range rows {
		out = append(out, agentRowView{
			RunID:      row.RunID,
			ClientID:   row.ClientID,
			Company:    row.Company,
			StateLabel: s.agentStateLabel(row.State),
			StateClass: agentStateClass(row.State),
			StateIcon:  s.agentStateIcon(row.State),
			Step:       row.Step,
			UpdatedAt:  row.UpdatedAt,
			DetailURL:  agentDetailURL(row.RunID, tenant, actor),
		})
	}
	return out
}

// buildAgentDetail собирает модель подробностей. В нормализованные причины
// попадает только код и число — свободный текст владельца не переносится.
func (s *Server) buildAgentDetail(detail AgentDetail, tenant, actor string) agentDetailView {
	view := agentDetailView{
		RunID:      detail.RunID,
		ClientID:   detail.ClientID,
		Company:    detail.Company,
		StateLabel: s.agentStateLabel(detail.State),
		StateClass: agentStateClass(detail.State),
		StateIcon:  s.agentStateIcon(detail.State),
	}
	view.Events = make([]agentEventView, 0, len(detail.Events))
	for _, event := range detail.Events {
		view.Events = append(view.Events, agentEventView{
			At:           event.At,
			Step:         event.Step,
			KindLabel:    s.stepKindLabel(event.Kind),
			OutcomeLabel: s.outcomeLabel(event.Outcome),
			OutcomeClass: outcomeClass(event.Outcome),
			OutcomeIcon:  s.outcomeIcon(event.Outcome),
			ReasonLabel:  s.reasonLabel(event.Reason),
		})
	}
	view.Reasons = make([]agentReasonView, 0, len(detail.Reasons))
	for _, reason := range detail.Reasons {
		view.Reasons = append(view.Reasons, agentReasonView{Code: reason.Code, Label: s.reasonLabel(reason.Code), Count: reason.Count})
	}
	view.Jobs = make([]agentJobView, 0, len(detail.Jobs))
	for _, job := range detail.Jobs {
		view.Jobs = append(view.Jobs, agentJobView{
			ID:         job.ID,
			KindLabel:  s.stepKindLabel(job.Kind),
			StateLabel: s.agentStateLabel(job.State),
			StateClass: agentStateClass(job.State),
		})
	}
	return view
}

// outcomeIcon возвращает маркер исхода: отказ отличается формой, не только цветом.
func (s *Server) outcomeIcon(outcome string) string {
	switch outcome {
	case workflow.StepCompleted, workflow.WaitFired, workflow.WaitReceived:
		return s.messages.Text(msgCockpitStateIconCompleted)
	case workflow.StepFailed, workflow.StepTimedOut:
		return s.messages.Text(msgCockpitStateIconFailed)
	case workflow.StepScheduled, workflow.WaitStarted:
		return s.messages.Text(msgCockpitStateIconRunning)
	case "unavailable":
		return s.messages.Text(msgCockpitStateIconUnknown)
	default:
		return s.messages.Text(msgCockpitStateIconQueued)
	}
}

// buildPeriodRows переводит разбивку по времени в представление экрана.
func buildPeriodRows(periods []NumberPeriod) []periodRowView {
	out := make([]periodRowView, 0, len(periods))
	for _, period := range periods {
		out = append(out, periodRowView{
			Date:      period.Date,
			Found:     period.Found,
			Processed: period.Processed,
			Rejected:  period.Rejected,
		})
	}
	return out
}

// groupClientsAggregate применяет порог минимальной группы: срез с числом ниже
// порога не показывается (спека operations-cockpit). Возвращает отфильтрованные
// строки; клиенты без показателей отбрасываются.
func groupClientsAggregate(rows []ClientAggregate, threshold int) []ClientAggregate {
	if threshold < 1 {
		threshold = 1
	}
	out := make([]ClientAggregate, 0, len(rows))
	for _, row := range rows {
		sum := row.Found + row.Processed + row.Rejected + row.InProgress + row.Result
		if sum < threshold {
			continue
		}
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ClientID < out[j].ClientID })
	return out
}

// panelSourceLabel переводит источник панели в подпись.
func (s *Server) panelSourceLabel(source string) string {
	switch source {
	case string(panelSourceMetrics):
		return s.messages.Text(msgCockpitSourceMetrics)
	case string(panelSourceProcess):
		return s.messages.Text(msgCockpitSourceProcess)
	case string(panelSourceAggregate):
		return s.messages.Text(msgCockpitSourceAggregate)
	case "":
		return s.messages.Text(msgCockpitStateUnknown)
	default:
		return source
	}
}

// panelRefreshLabel переводит способ обновления панели в подпись.
func (s *Server) panelRefreshLabel(refresh string) string {
	switch refresh {
	case string(panelRefreshPoll):
		return s.messages.Text(msgCockpitRefreshPoll)
	case string(panelRefreshStatic):
		return s.messages.Text(msgCockpitRefreshStatic)
	case "":
		return s.messages.Text(msgCockpitStateUnknown)
	default:
		return refresh
	}
}

// anomalyLabel переводит машинный код аномалии в подпись. Неизвестный код
// показывается общей подписью, а не свободным текстом.
func (s *Server) anomalyLabel(code string) string {
	switch code {
	case "rejection_spike":
		return s.messages.Text(msgCockpitAnomalyRejectionSpike)
	case "stalled":
		return s.messages.Text(msgCockpitAnomalyStalled)
	default:
		return s.messages.Text(msgCockpitAnomalyUnknown)
	}
}

// operationsURL собирает адрес экрана цифр процесса.
func operationsURL(tenant, actor string) string {
	return consoleURL("/platform/operations", tenant, actor, "")
}

// agentsURL собирает адрес ленты агентов с фильтром.
func agentsURL(tenant, actor, clientID, category string, _ int) string {
	values := url.Values{}
	if tenant = strings.TrimSpace(tenant); tenant != "" {
		values.Set("tenant_id", tenant)
	}
	if actor = strings.TrimSpace(actor); actor != "" {
		values.Set("actor", actor)
	}
	if clientID = strings.TrimSpace(clientID); clientID != "" {
		values.Set("client_id", clientID)
	}
	if category = strings.TrimSpace(category); category != "" {
		values.Set("category", category)
	}
	return withValues("/platform/operations/agents", values)
}

// agentDetailURL собирает адрес подробностей агента.
func agentDetailURL(runID, tenant, actor string) string {
	base := "/platform/operations/agents/" + url.PathEscape(runID)
	values := url.Values{}
	if tenant = strings.TrimSpace(tenant); tenant != "" {
		values.Set("tenant_id", tenant)
	}
	if actor = strings.TrimSpace(actor); actor != "" {
		values.Set("actor", actor)
	}
	return withValues(base, values)
}

// processURL собирает адрес экрана процессов.
func processURL(tenant, actor, workflowID, runID string) string {
	values := url.Values{}
	if tenant = strings.TrimSpace(tenant); tenant != "" {
		values.Set("tenant_id", tenant)
	}
	if actor = strings.TrimSpace(actor); actor != "" {
		values.Set("actor", actor)
	}
	if workflowID = strings.TrimSpace(workflowID); workflowID != "" {
		values.Set("workflow", workflowID)
	}
	if runID = strings.TrimSpace(runID); runID != "" {
		values.Set("run", runID)
	}
	return withValues("/platform/operations/process", values)
}

// dashboardURL собирает адрес полноэкранного дашборда.
func dashboardURL(tenant, actor string, fullscreen bool) string {
	values := url.Values{}
	if tenant = strings.TrimSpace(tenant); tenant != "" {
		values.Set("tenant_id", tenant)
	}
	if actor = strings.TrimSpace(actor); actor != "" {
		values.Set("actor", actor)
	}
	if fullscreen {
		values.Set("fullscreen", "1")
	}
	return withValues("/platform/operations/dashboard", values)
}

// withValues присоединяет параметры к базовому пути.
func withValues(base string, values url.Values) string {
	if encoded := values.Encode(); encoded != "" {
		return base + "?" + encoded
	}
	return base
}

// metricPanelTitle выбирает подпись метрик-панели по ключу.
func (s *Server) metricPanelTitle(key string) string {
	switch key {
	case "operations_workflow_started":
		return s.messages.Text(msgCockpitPanelWorkflowStarted)
	case "operations_workflow_errors":
		return s.messages.Text(msgCockpitPanelWorkflowErrors)
	case "operations_http_error_ratio":
		return s.messages.Text(msgCockpitPanelHTTPErrorRatio)
	default:
		return key
	}
}

// metricPanels возвращает объявленные метрик-панели (встроенный набор).
func (s *Server) metricPanels() []prometheus.Panel {
	if len(s.deps.CockpitMetricPanels) > 0 {
		return s.deps.CockpitMetricPanels
	}
	return OperationsMetricPanels()
}
