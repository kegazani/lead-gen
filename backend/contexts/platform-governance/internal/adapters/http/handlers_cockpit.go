package httpapi

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	app "lead-gen/backend/contexts/platform-governance/internal/app"
	"lead-gen/backend/platform/graph"
	"lead-gen/backend/platform/observability/prometheus"
	"lead-gen/backend/platform/panels"
	"lead-gen/backend/platform/workflow"
	"lead-gen/frontend/components"
)

// Экран операций администратора платформы (capability operations-cockpit): цифры
// процесса, живая лента агентов, подробности агента/шага, граф процессов
// Temporal, полноэкранный дашборд по клиентам и расширяемые панели. Доступ и
// данные проверяются на сервере по снимку access-decision (раздел operations);
// изменяющие формы защищены токеном CSRF.

// Источники данных панелей экрана.
const (
	panelSourceMetrics   panels.Source = "metrics"
	panelSourceProcess   panels.Source = "process"
	panelSourceAggregate panels.Source = "aggregate"
)

// Способы обновления панели (псевдонимы примитива реестра).
const (
	panelRefreshPoll   = panels.RefreshPoll
	panelRefreshStatic = panels.RefreshStatic
)

// Идентификатор встроенной панели сводки процессов.
const panelProcessOverviewID = "process_overview"

// htmxScriptPath — адрес вендоренного скрипта HTMX в этом сервисе: живая лента и
// граф обновляются без ручной перезагрузки, а раскрытие строк сохраняется
// (hx-preserve). Скрипт раздаётся тем же сервисом, что и экран.
const htmxScriptPath = "/static/htmx.min.js"

// Пределы экрана: окно агрегатов, число строк ленты, число исполнений и таймаут
// панели. Экран не должен становиться источником нагрузки на Temporal и Prometheus.
const (
	cockpitWindow          = 24 * time.Hour
	cockpitFeedLimit       = 200
	cockpitProcessLimit    = 100
	cockpitPanelTimeout    = 5 * time.Second
	cockpitDefaultMinGroup = 5
)

// errPanelUnavailable — источник панели недоступен: реестр пометит только эту
// панель, остальные продолжат работать.
var errPanelUnavailable = errors.New("cockpit: источник панели недоступен")

// CockpitPanel — объявление расширяемой панели экрана: объявление и функция
// рендеринга, которую задаёт владелец данных панели.
type CockpitPanel struct {
	Definition panels.Definition
	Render     panels.Renderer
}

// handleOperations обслуживает GET /platform/operations: цифры процесса (6.1) и
// расширяемые панели (5.4).
func (s *Server) handleOperations(w http.ResponseWriter, r *http.Request) {
	actor := consoleActor(r)
	tenant, ok := s.requireTenant(w, r, actor)
	if !ok {
		return
	}
	if status, message := s.decideSection(r, tenant, app.SectionOperations, app.ActionView); status != 0 {
		s.renderConsoleDenied(w, status, message)
		return
	}
	ctx := r.Context()
	numbers := cockpitNumbersView{
		Actor:    actor,
		TenantID: tenant,
		Notice:   consoleNotice(r.URL.Query().Get("notice")),
	}
	if s.deps.Operations == nil {
		numbers.Unavailable = true
	} else {
		result, err := s.deps.Operations.Numbers(ctx, cockpitWindow)
		switch {
		case err != nil:
			numbers.Unavailable = true
		case len(result.Clients) == 0 && len(result.Periods) == 0:
			numbers.Empty = true
		default:
			clients := result.Clients
			sort.SliceStable(clients, func(i, j int) bool { return clients[i].ClientID < clients[j].ClientID })
			if len(clients) > 0 {
				numbers.Total = s.buildNumberCells(totalNumbers(clients), tenant, actor, "")
				numbers.HasNumbers = true
				rows := make([]clientNumbersRow, 0, len(clients))
				for _, client := range clients {
					rows = append(rows, clientNumbersRow{ClientID: client.ClientID, Cells: s.buildNumberCells(client, tenant, actor, client.ClientID)})
				}
				numbers.Clients = rows
			}
			numbers.Periods = buildPeriodRows(result.Periods)
			numbers.HasPeriods = len(numbers.Periods) > 0
		}
	}

	hidden := map[string]bool{}
	if s.deps.CockpitSettings != nil {
		if values, err := s.deps.CockpitSettings.HiddenPanels(ctx, actor); err == nil {
			hidden = values
		}
	}
	view := cockpitOperationsView{
		Numbers:  numbers,
		Requests: s.cockpitRequestRows(ctx, tenant),
		Panels:   cockpitPanelsView{Panels: s.cockpitPanels(ctx, tenant, actor, hidden)},
		CSRF:     s.csrf.EnsureToken(w, r),
		Actor:    actor,
		TenantID: tenant,
	}
	s.render(w, http.StatusOK, pageCockpit, components.Page{
		Layout:  s.cockpitLayout(s.messages.Text(msgCockpitTitleOperations)),
		Sidebar: s.consoleNav("/platform/operations", tenant, actor),
		View:    view,
	})
}

// cockpitRequestRows отображает список заявок владельца и отдельное чтение
// исполнения по точному requestID. Ошибка одной workflow-записи локальна для
// строки; ошибка списка — для всего блока. Пустой ответ остаётся пустым успехом.
func (s *Server) cockpitRequestRows(ctx context.Context, tenantID string) cockpitRequestsView {
	view := cockpitRequestsView{}
	if s.deps.RequestExecutions == nil {
		view.Unavailable = true
		return view
	}
	requests, err := s.deps.RequestExecutions.ListRequests(ctx, tenantID)
	if err != nil {
		view.Unavailable = true
		return view
	}
	if len(requests) == 0 {
		view.Empty = true
		return view
	}
	view.Rows = make([]cockpitRequestRow, 0, len(requests))
	for _, request := range requests {
		row := cockpitRequestRow{RequestID: request.RequestID, DomainStatus: s.requestDomainStatusLabel(request.DomainStatus)}
		execution, err := s.deps.RequestExecutions.Workflow(ctx, tenantID, request.RequestID)
		if err != nil {
			row.Phase = "unavailable"
		} else {
			row.WorkflowID = execution.WorkflowID
			row.RunID = execution.RunID
			row.Phase = execution.Phase
			row.ExecutionFound = execution.ExecutionFound
			row.Found = execution.Found
			row.Target = execution.Target
		}
		view.Rows = append(view.Rows, row)
	}
	return view
}

// handleAgents обслуживает GET /platform/operations/agents: живая лента агентов
// (6.2). HTMX-запрос получает только фрагмент ленты для авто-обновления.
func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	tenant := s.activeTenant(r)
	actor := consoleActor(r)
	if status, message := s.decideSection(r, tenant, app.SectionOperations, app.ActionView); status != 0 {
		s.renderConsoleDenied(w, status, message)
		return
	}
	view := cockpitAgentsView{Actor: actor, TenantID: tenant, Live: components.Live{ID: "cockpit-agents-live"}}
	view.FeedURL = agentsURL(tenant, actor, r.URL.Query().Get("client_id"), r.URL.Query().Get("category"), 0)
	if s.deps.Operations == nil {
		view.Unavailable = true
	} else {
		filter := AgentFilter{
			TenantID: tenant,
			ClientID: r.URL.Query().Get("client_id"),
			Category: r.URL.Query().Get("category"),
			Limit:    cockpitFeedLimit,
		}
		rows, err := s.deps.Operations.Agents(r.Context(), filter)
		switch {
		case err != nil:
			view.Unavailable = true
		case len(rows) == 0:
			view.Empty = true
		default:
			view.Rows = s.buildAgentRows(rows, tenant, actor)
		}
	}
	if r.Header.Get("HX-Request") == "true" {
		s.renderFragment(w, http.StatusOK, pageCockpitAgents, "agents-feed", view)
		return
	}
	s.render(w, http.StatusOK, pageCockpitAgents, components.Page{
		Layout:  s.cockpitLayout(s.messages.Text(msgCockpitTitleAgents)),
		Sidebar: s.consoleNav("/platform/operations", tenant, actor),
		View:    view,
	})
}

// handleAgent обслуживает GET /platform/operations/agents/{runID}: подробности
// агента/шага (6.3). Отказ отображается отказом, а не успешным исходом.
func (s *Server) handleAgent(w http.ResponseWriter, r *http.Request) {
	tenant := s.activeTenant(r)
	actor := consoleActor(r)
	if status, message := s.decideSection(r, tenant, app.SectionOperations, app.ActionView); status != 0 {
		s.renderConsoleDenied(w, status, message)
		return
	}
	view := cockpitAgentView{
		Actor:    actor,
		TenantID: tenant,
		BackURL:  agentsURL(tenant, actor, "", "", 0),
	}
	if s.deps.Operations == nil {
		view.Unavailable = true
	} else {
		detail, err := s.deps.Operations.AgentDetail(r.Context(), tenant, r.PathValue("runID"))
		if err != nil {
			view.Unavailable = true
		} else {
			view.Found = true
			view.Detail = s.buildAgentDetail(detail, tenant, actor)
		}
	}
	if r.Header.Get("HX-Request") == "true" {
		s.renderFragment(w, http.StatusOK, pageCockpitAgent, "agent-detail", view)
		return
	}
	s.render(w, http.StatusOK, pageCockpitAgent, components.Page{
		Layout:  s.cockpitLayout(s.messages.Text(msgCockpitTitleAgent)),
		Sidebar: s.consoleNav("/platform/operations", tenant, actor),
		View:    view,
	})
}

// handleProcess обслуживает GET /platform/operations/process: граф процессов
// Temporal (6.4). Недоступность рантайма — явное «недоступно», а не пустой граф.
func (s *Server) handleProcess(w http.ResponseWriter, r *http.Request) {
	tenant := s.activeTenant(r)
	actor := consoleActor(r)
	if status, message := s.decideSection(r, tenant, app.SectionOperations, app.ActionView); status != 0 {
		s.renderConsoleDenied(w, status, message)
		return
	}
	view := cockpitProcessView{Actor: actor, TenantID: tenant}
	view.FeedURL = processURL(tenant, actor, "", "")
	if s.deps.WorkflowReader == nil {
		view.Unavailable = true
	} else {
		executions, err := s.deps.WorkflowReader.List(r.Context(), "", cockpitProcessLimit)
		switch {
		case err != nil:
			view.Unavailable = true
		case len(executions) == 0:
			view.HasNodes = false
		default:
			selectedWorkflow := strings.TrimSpace(r.URL.Query().Get("workflow"))
			selectedRun := strings.TrimSpace(r.URL.Query().Get("run"))
			view = s.buildProcessView(executions, tenant, actor, selectedWorkflow, selectedRun)
			view.Actor = actor
			view.TenantID = tenant
			view.FeedURL = processURL(tenant, actor, view.SelectedID, selectedRun)
			if view.SelectedID != "" {
				process, err := s.deps.WorkflowReader.Timeline(r.Context(), view.SelectedID, selectedRun)
				if err == nil {
					s.applyTimeline(&view, process)
				}
			}
		}
	}
	if r.Header.Get("HX-Request") == "true" {
		s.renderFragment(w, http.StatusOK, pageCockpitProcess, "process-graph", view)
		return
	}
	s.render(w, http.StatusOK, pageCockpitProcess, components.Page{
		Layout:  s.cockpitLayout(s.messages.Text(msgCockpitTitleProcess)),
		Sidebar: s.consoleNav("/platform/operations", tenant, actor),
		View:    view,
	})
}

// handleDashboard обслуживает GET /platform/operations/dashboard: полноэкранный
// дашборд по всем клиентам (6.5) с порогом минимальной группы.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	tenant := s.activeTenant(r)
	actor := consoleActor(r)
	if status, message := s.decideSection(r, tenant, app.SectionOperations, app.ActionView); status != 0 {
		s.renderConsoleDenied(w, status, message)
		return
	}
	threshold := s.cockpitMinGroup()
	view := cockpitDashboardView{Actor: actor, TenantID: tenant, Threshold: threshold, BackURL: operationsURL(tenant, actor), Notice: consoleNotice(r.URL.Query().Get("notice"))}
	for _, category := range processCategories {
		view.Headers = append(view.Headers, s.categoryLabel(category))
	}
	if s.deps.Operations == nil {
		view.Unavailable = true
	} else {
		clients, err := s.deps.Operations.Clients(r.Context(), cockpitWindow)
		if err != nil {
			view.Unavailable = true
		} else {
			filtered := groupClientsAggregate(clients, threshold)
			if len(filtered) == 0 {
				view.Empty = true
			} else {
				view.Rows = s.buildDashboardRows(filtered, tenant, actor)
			}
		}
	}
	s.render(w, http.StatusOK, pageCockpitDashboard, components.Page{
		Layout: s.cockpitLayout(s.messages.Text(msgCockpitTitleDashboard)),
		View:   view,
	})
}

// handlePanelsToggle обслуживает POST /platform/operations/panels/toggle:
// скрывает или показывает панель экрана для субъекта. Изменяющая форма защищена
// токеном CSRF; доступ проверяется на сервере.
func (s *Server) handlePanelsToggle(w http.ResponseWriter, r *http.Request) {
	tenant := s.activeTenant(r)
	actor := consoleActor(r)
	if status, message := s.decideSection(r, tenant, app.SectionOperations, app.ActionView); status != 0 {
		s.renderConsoleDenied(w, status, message)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderConsoleDenied(w, http.StatusBadRequest, s.messages.Text(msgCockpitNoticeBadRequest))
		return
	}
	if !s.csrf.Verify(r) {
		s.renderConsoleDenied(w, http.StatusForbidden, s.messages.Text(msgCockpitNoticeCSRF))
		return
	}
	panelID := strings.TrimSpace(r.PostFormValue("panel"))
	if !s.knownPanel(panelID) {
		s.renderConsoleDenied(w, http.StatusBadRequest, s.messages.Text(msgCockpitNoticeBadPanel))
		return
	}
	hidden := strings.TrimSpace(r.PostFormValue("hidden")) == "true"
	notice := "panel_shown"
	if hidden {
		notice = "panel_hidden"
	}
	if s.deps.CockpitSettings != nil {
		if err := s.deps.CockpitSettings.SetPanelHidden(r.Context(), actor, panelID, hidden); err != nil {
			s.renderConsoleDenied(w, http.StatusInternalServerError, s.messages.Text(msgCockpitNoticePanelsUnavailable))
			return
		}
	}
	http.Redirect(w, r, consoleURL("/platform/operations", tenant, actor, notice), http.StatusSeeOther)
}

// cockpitLayout собирает каркас экранов операций: общее оформление и скрипт
// HTMX для авто-обновления фрагментов без ручной перезагрузки.
func (s *Server) cockpitLayout(title string) components.Layout {
	scripts := append([]string(nil), components.ConsoleScripts...)
	scripts = append(scripts, htmxScriptPath)
	return components.Layout{Title: title, Scripts: scripts}
}

// cockpitMinGroup возвращает порог минимальной группы дашборда.
func (s *Server) cockpitMinGroup() int {
	if s.deps.CockpitMinGroup > 0 {
		return s.deps.CockpitMinGroup
	}
	return cockpitDefaultMinGroup
}

// cockpitPanels собирает панели экрана: объявленные, расширяемые из зависимостей,
// с учётом скрытых субъектом. Отказ источника локализуется панелью.
func (s *Server) cockpitPanels(ctx context.Context, tenant, actor string, hidden map[string]bool) []panelView {
	definitions := s.cockpitPanelDefinitions()
	registry := panels.NewRegistry(cockpitPanelTimeout)
	for _, def := range definitions {
		if hidden[def.ID] {
			continue
		}
		if render := s.cockpitPanelRenderer(def.ID); render != nil {
			_ = registry.Register(def, render)
		}
	}
	rendered := make(map[string]panels.State)
	for _, state := range registry.Render(ctx) {
		rendered[state.ID] = state
	}

	views := make([]panelView, 0, len(definitions))
	for _, def := range definitions {
		view := panelView{
			ID:           def.ID,
			Title:        def.Title,
			SourceLabel:  s.panelSourceLabel(string(def.Source)),
			RefreshLabel: s.panelRefreshLabel(string(def.Refresh)),
			ToggleURL:    panelsToggleURL(tenant, actor),
		}
		if hidden[def.ID] {
			view.Hidden = true
			view.ReasonLabel = s.messages.Text(msgCockpitPanelHidden)
			view.ToggleLabel = s.messages.Text(msgCockpitPanelShow)
			views = append(views, view)
			continue
		}
		state := rendered[def.ID]
		view.Available = state.Available
		view.Body = state.Body
		view.ToggleLabel = s.messages.Text(msgCockpitPanelHide)
		if !state.Available {
			view.ReasonLabel = s.messages.Text(msgCockpitPanelUnavailable)
		}
		views = append(views, view)
	}
	return views
}

// cockpitPanelDefinitions возвращает объявления панелей в порядке вывода.
func (s *Server) cockpitPanelDefinitions() []panels.Definition {
	definitions := make([]panels.Definition, 0, len(s.metricPanels())+1+len(s.deps.CockpitPanels))
	for _, panel := range s.metricPanels() {
		definitions = append(definitions, panels.Definition{
			ID:      panel.Key,
			Title:   s.metricPanelTitle(panel.Key),
			Source:  panelSourceMetrics,
			Refresh: panels.RefreshPoll,
			Order:   10,
		})
	}
	definitions = append(definitions, panels.Definition{
		ID:      panelProcessOverviewID,
		Title:   s.messages.Text(msgCockpitPanelProcess),
		Source:  panelSourceProcess,
		Refresh: panels.RefreshPoll,
		Order:   20,
	})
	for _, custom := range s.deps.CockpitPanels {
		definitions = append(definitions, custom.Definition)
	}
	return definitions
}

// cockpitPanelRenderer возвращает функцию рендеринга панели по идентификатору.
func (s *Server) cockpitPanelRenderer(id string) panels.Renderer {
	for _, panel := range s.metricPanels() {
		panel := panel
		if panel.Key == id {
			return func(ctx context.Context) (template.HTML, error) { return s.renderMetricPanel(ctx, panel) }
		}
	}
	if id == panelProcessOverviewID {
		return s.renderProcessPanel
	}
	for _, custom := range s.deps.CockpitPanels {
		if custom.Definition.ID == id {
			return custom.Render
		}
	}
	return nil
}

// knownPanel сообщает, объявлена ли панель с таким идентификатором.
func (s *Server) knownPanel(id string) bool {
	if id == "" {
		return false
	}
	for _, def := range s.cockpitPanelDefinitions() {
		if def.ID == id {
			return true
		}
	}
	return false
}

// renderMetricPanel читает метрик-панель через серверный прокси Prometheus.
// Недоступность источника — ошибка панели (её отказ локализуется), а не пустая
// серия и не ноль.
func (s *Server) renderMetricPanel(ctx context.Context, panel prometheus.Panel) (template.HTML, error) {
	if s.deps.CockpitMetrics == nil {
		return "", errPanelUnavailable
	}
	limits := prometheus.DefaultLimits()
	window, step, _, err := prometheus.ResolveWindow(panel, "", "", limits)
	if err != nil {
		return "", err
	}
	at := time.Now().UTC()
	result := prometheus.ReadPanel(ctx, s.deps.CockpitMetrics, panel, panel.Query, at.Add(-window), at, window, step, limits)
	if !result.Available {
		return "", errPanelUnavailable
	}
	return s.metricPanelBody(result, panel), nil
}

// metricPanelBody рисует значения панели. Пустые серии — честное «нет данных».
func (s *Server) metricPanelBody(result prometheus.PanelResult, panel prometheus.Panel) template.HTML {
	var builder strings.Builder
	var numbers []string
	for _, series := range result.Series {
		if len(series.Points) == 0 {
			continue
		}
		numbers = append(numbers, strconv.FormatFloat(series.Points[len(series.Points)-1].Value, 'f', 2, 64))
	}
	builder.WriteString(`<p class="cockpit-panel-value">`)
	if len(numbers) == 0 {
		builder.WriteString(`<span class="cockpit-panel-empty">`)
		builder.WriteString(template.HTMLEscapeString(s.messages.Text(msgCockpitPanelNoData)))
		builder.WriteString(`</span>`)
	} else {
		for index, number := range numbers {
			if index > 0 {
				builder.WriteString(", ")
			}
			builder.WriteString(`<span class="cockpit-panel-number">`)
			builder.WriteString(template.HTMLEscapeString(number))
			builder.WriteString(`</span>`)
		}
		if panel.Unit != "" {
			builder.WriteString(` <span class="cockpit-panel-unit">`)
			builder.WriteString(template.HTMLEscapeString(panel.Unit))
			builder.WriteString(`</span>`)
		}
	}
	builder.WriteString(`</p>`)
	return template.HTML(builder.String())
}

// renderProcessPanel читает сводку процессов из рантайма. Отсутствие рантайма —
// отказ панели, а не нули.
func (s *Server) renderProcessPanel(ctx context.Context) (template.HTML, error) {
	if s.deps.WorkflowReader == nil {
		return "", errPanelUnavailable
	}
	executions, err := s.deps.WorkflowReader.List(ctx, "", cockpitProcessLimit)
	if err != nil {
		return "", err
	}
	counts := make(map[string]int)
	for _, execution := range executions {
		counts[execution.Status]++
	}
	order := []string{workflow.ProcessRunning, workflow.ProcessCompleted, workflow.ProcessFailed}
	var builder strings.Builder
	builder.WriteString(`<ul class="cockpit-panel-list">`)
	for _, status := range order {
		builder.WriteString(`<li><span class="cockpit-panel-marker" aria-hidden="true">`)
		builder.WriteString(template.HTMLEscapeString(s.processStatusIcon(status)))
		builder.WriteString(`</span> `)
		builder.WriteString(template.HTMLEscapeString(s.processStatusLabel(status)))
		builder.WriteString(`: <span class="cockpit-panel-number">`)
		builder.WriteString(template.HTMLEscapeString(strconv.Itoa(counts[status])))
		builder.WriteString(`</span></li>`)
	}
	builder.WriteString(`</ul>`)
	return template.HTML(builder.String()), nil
}

// processGraph строит граф исполнений: узлы — исполнения, колонка — статус.
// Рёбра не выдумываются: исполнения независимы.
func (s *Server) processGraph(executions []workflow.ProcessExecution) graph.Graph {
	nodes := make([]graph.Node, 0, len(executions))
	for _, execution := range executions {
		id := execution.WorkflowID
		if id == "" {
			id = execution.RunID
		}
		nodes = append(nodes, graph.Node{
			ID:     id,
			Label:  execution.Type,
			Column: execution.Status,
			Status: execution.Status,
		})
	}
	return graph.Graph{Nodes: nodes, GeneratedAt: time.Now().UTC()}.Layout()
}

// buildProcessView собирает модель экрана процессов из графа и списка исполнений.
func (s *Server) buildProcessView(executions []workflow.ProcessExecution, tenant, actor, selectedWorkflow, selectedRun string) cockpitProcessView {
	view := cockpitProcessView{Actor: actor, TenantID: tenant}
	graphModel := s.processGraph(executions)
	view.GeneratedAt = graphModel.GeneratedAt
	view.HasNodes = len(graphModel.Nodes) > 0
	view.Nodes = append(view.Nodes, s.buildGraphNodeViews(graphModel)...)
	view.Edges = buildGraphEdgeViews(graphModel)
	view.ViewBox = graphViewBox(graphModel)

	selected := selectedWorkflow
	if selected == "" {
		for _, execution := range executions {
			if execution.Running {
				selected = execution.WorkflowID
				break
			}
		}
	}
	if selected == "" && len(executions) > 0 {
		selected = executions[0].WorkflowID
	}
	view.SelectedID = selected

	for _, execution := range executions {
		view.Executions = append(view.Executions, processExecutionView{
			WorkflowID:  execution.WorkflowID,
			RunID:       execution.RunID,
			Type:        execution.Type,
			StatusLabel: s.processStatusLabel(execution.Status),
			StatusClass: agentStateClass(execution.Status),
			StatusIcon:  s.processStatusIcon(execution.Status),
			StartLabel:  formatTime(execution.StartTime),
			URL:         processURL(tenant, actor, execution.WorkflowID, execution.RunID),
			Selected:    execution.WorkflowID == selected,
		})
	}
	return view
}

// applyTimeline добавляет к модели ход выбранного исполнения: шаги и ожидания
// (таймеры, сигналы) как цепочку переходов.
func (s *Server) applyTimeline(view *cockpitProcessView, process workflow.Process) {
	view.HasTimeline = len(process.Steps) > 0 || len(process.Waits) > 0
	for _, step := range process.Steps {
		view.Timeline = append(view.Timeline, timelineItemView{
			At:         step.At,
			Name:       step.Name,
			KindLabel:  s.stepKindLabel(step.Kind),
			StateLabel: s.outcomeLabel(step.Outcome),
			StateClass: outcomeClass(step.Outcome),
			StateIcon:  s.outcomeIcon(step.Outcome),
		})
	}
	for _, wait := range process.Waits {
		view.Timeline = append(view.Timeline, timelineItemView{
			At:         wait.At,
			Name:       wait.Name,
			KindLabel:  s.stepKindLabel(wait.Kind),
			StateLabel: s.outcomeLabel(wait.State),
			StateClass: outcomeClass(wait.State),
			StateIcon:  s.outcomeIcon(wait.State),
		})
	}
	chain := timelineGraph(process)
	view.TimelineNodes = s.buildGraphNodeViews(chain)
	view.TimelineEdges = buildGraphEdgeViews(chain)
	view.TimelineBox = graphViewBox(chain)
}

// timelineGraph строит цепочку хода исполнения: шаги и ожидания по порядку с
// переходами между соседними узлами.
func timelineGraph(process workflow.Process) graph.Graph {
	nodes := make([]graph.Node, 0, len(process.Steps)+len(process.Waits))
	row := 0
	for index, step := range process.Steps {
		nodes = append(nodes, graph.Node{ID: fmt.Sprintf("step-%d", index), Label: step.Name, Row: row, Status: step.Outcome})
		row++
	}
	for index, wait := range process.Waits {
		nodes = append(nodes, graph.Node{ID: fmt.Sprintf("wait-%d", index), Label: wait.Name, Row: row, Status: wait.State})
		row++
	}
	edges := make([]graph.Edge, 0, len(nodes))
	for index := 0; index+1 < len(nodes); index++ {
		edges = append(edges, graph.Edge{ID: fmt.Sprintf("t-%d", index), From: nodes[index].ID, To: nodes[index+1].ID})
	}
	return graph.Graph{Nodes: nodes, Edges: edges}.Layout()
}

// buildGraphNodeViews переводит узлы графа в разметочные представления.
func (s *Server) buildGraphNodeViews(model graph.Graph) []processNodeView {
	views := make([]processNodeView, 0, len(model.Nodes))
	for _, node := range model.Nodes {
		boxX := node.X + topologyMargin
		boxY := node.Y + topologyMargin
		label := node.Label
		if label == "" {
			label = node.ID
		}
		views = append(views, processNodeView{
			ID:         node.ID,
			Label:      label,
			Detail:     node.Detail,
			StateLabel: s.processStatusLabel(node.Status),
			StateClass: agentStateClass(node.Status),
			StateIcon:  s.processStatusIcon(node.Status),
			BoxX:       boxX,
			BoxY:       boxY,
			LabelX:     boxX + 10,
			LabelY:     boxY + 26,
			StateY:     boxY + 48,
		})
	}
	return views
}

// buildGraphEdgeViews строит рёбра по центрам узлов.
func buildGraphEdgeViews(model graph.Graph) []processEdgeView {
	centers := make(map[string][2]float64, len(model.Nodes))
	for _, node := range model.Nodes {
		centers[node.ID] = [2]float64{node.X + topologyBoxWidth/2 + topologyMargin, node.Y + topologyBoxHeight/2 + topologyMargin}
	}
	views := make([]processEdgeView, 0, len(model.Edges))
	for _, edge := range model.Edges {
		from, fromOK := centers[edge.From]
		to, toOK := centers[edge.To]
		if !fromOK || !toOK {
			continue
		}
		views = append(views, processEdgeView{ID: edge.ID, X1: from[0], Y1: from[1], X2: to[0], Y2: to[1]})
	}
	return views
}

// graphViewBox вычисляет viewBox серверного SVG по размерам узлов.
func graphViewBox(model graph.Graph) string {
	var maxX, maxY float64
	for _, node := range model.Nodes {
		if x := node.X + topologyMargin*2 + topologyBoxWidth; x > maxX {
			maxX = x
		}
		if y := node.Y + topologyMargin*2 + topologyBoxHeight; y > maxY {
			maxY = y
		}
	}
	if maxX == 0 || maxY == 0 {
		return "0 0 1 1"
	}
	return fmt.Sprintf("0 0 %.0f %.0f", maxX, maxY)
}

// buildDashboardRows собирает строки дашборда. Только агрегаты и обезличенные
// коды аномалий: персональных данных и красной зоны здесь нет.
func (s *Server) buildDashboardRows(rows []ClientAggregate, tenant, actor string) []dashboardRowView {
	out := make([]dashboardRowView, 0, len(rows))
	for _, row := range rows {
		view := dashboardRowView{
			ClientID: row.ClientID,
			Cells:    s.buildNumberCells(ClientNumbers{Found: row.Found, Processed: row.Processed, Rejected: row.Rejected, InProgress: row.InProgress, Result: row.Result}, tenant, actor, row.ClientID),
		}
		for _, code := range row.Anomalies {
			view.AnomalyLabels = append(view.AnomalyLabels, s.anomalyLabel(code))
		}
		out = append(out, view)
	}
	return out
}

// formatTime форматирует время перехода без внешнего форматера: список исполнений
// показывает дату и время в UTC.
func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format("2006-01-02 15:04")
}

// panelsToggleURL — адрес изменяющей формы скрытия панели. Тенант и субъект
// передаются в запросе: серверная проверка доступа решает по ним, а не по форме.
func panelsToggleURL(tenant, actor string) string {
	values := url.Values{}
	if tenant = strings.TrimSpace(tenant); tenant != "" {
		values.Set("tenant_id", tenant)
	}
	if actor = strings.TrimSpace(actor); actor != "" {
		values.Set("actor", actor)
	}
	return withValues("/platform/operations/panels/toggle", values)
}
