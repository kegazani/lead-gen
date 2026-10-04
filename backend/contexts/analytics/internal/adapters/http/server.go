// Package httpapi реализует HTTP-адаптер роли api контекста analytics: дашборд
// бизнес-метрик клиента.
//
// Это эксплуатационная проекция роли api, а не второй домен: адаптер только
// читает срез у use case, транслирует дельты счётчиков потоком серверных событий
// и принимает нормализованные события dev-контуром. Решений о допустимости он не
// принимает — инварианты витрины обеспечивает read-модель, а доступ решает порт
// access-decision. Клиентский read-путь несёт только агрегаты и счётчики.
package httpapi

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"lead-gen/backend/contexts/analytics/internal/app/command"
	"lead-gen/backend/contexts/analytics/internal/app/query"
	"lead-gen/backend/contexts/analytics/internal/ports"
	"lead-gen/frontend/components"
)

const (
	// contentTypeHTML — тип ответа экрана дашборда.
	contentTypeHTML = "text/html; charset=utf-8"
	// contentTypeJSON — тип ответа write-путей.
	contentTypeJSON = "application/json; charset=utf-8"
	// maxBodyBytes ограничивает размер тела запроса приёма событий.
	maxBodyBytes = 1 << 20
	// streamHeartbeat — период сердцебиения потока, чтобы соединение не
	// закрывалось промежуточными прокси на простое.
	streamHeartbeat = 15 * time.Second
)

// templateFS — шаблоны дашборда, встроенные в бинарник: разметка экрана не может
// разойтись с кодом обработчиков.
//
//go:embed templates/*.html
var templateFS embed.FS

// pageDashboard — имя страницы набора шаблонов.
const pageDashboard = "templates/dashboard.html"

// defaultRole — роль клиента дашборда, если она не передана (dev-контур без
// аутентификации). Настоящее решение о доступе — за platform-governance.
const defaultRole = "client"

// Dependencies — внешние зависимости HTTP-адаптера: прикладные сценарии и порт
// решения о доступе.
type Dependencies struct {
	// Dashboard — чтение среза дашборда.
	Dashboard *query.DashboardHandler

	// Reports — сборка среза отчётности.
	Reports *query.ReportHandler

	// Events — применение нормализованного события к read-модели.
	Events *command.ApplyEventHandler

	// Access — решение о доступе (роль → ресурс). nil означает, что владелец
	// политики не подключён: дашборд честно отвечает 503, а не открывает доступ.
	Access ports.AccessDecider

	// Console — базовые адреса сервисов консоли для общей навигации по разделам
	// (Заявки, Исполнение, Лиды, Отчёты). Пустые адреса дают относительные пути.
	Console components.ConsoleBases
}

// Server — HTTP-сервер среза. Нулевое значение непригодно: используйте
// NewServer.
type Server struct {
	deps    Dependencies
	handler http.Handler
	pages   *components.Set
	console components.ConsoleBases
}

// NewServer собирает маршруты среза из переданных сценариев. Шаблоны дашборда
// разбираются сразу: ошибка разметки видна при сборке сервиса. Если набор не
// собрался, экран честно отвечает 500.
func NewServer(deps Dependencies) *Server {
	server := &Server{deps: deps, console: deps.Console}
	server.pages, _ = newPages()
	server.handler = server.routes()
	return server
}

// ServeHTTP реализует http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// routes объявляет маршруты дашборда: экран, поток серверных событий, приём
// событий dev-контуром и проверку живости.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	// Корень сервиса ведёт на дашборд, а не отвечает 404: адрес сервиса —
	// точка входа в его экран.
	mux.HandleFunc("GET /{$}", s.handleRoot)
	mux.HandleFunc("GET /dashboard", s.handleDashboard)
	mux.HandleFunc("GET /dashboard/stream", s.handleStream)
	// Отчётность: экран, JSON-срез и выгрузки CSV/XLSX с тем же фильтром.
	mux.HandleFunc("GET /reports", s.handleReports)
	mux.HandleFunc("GET /reports.json", s.handleReportsJSON)
	mux.HandleFunc("GET /reports/export.csv", s.handleReportsCSV)
	mux.HandleFunc("GET /reports/export.xlsx", s.handleReportsXLSX)
	// Приём событий — write-путь dev- и тест-контура, а не клиентский read-путь:
	// в рабочем контуре события приходят воркером из брокера.
	mux.HandleFunc("POST /dashboard/events", s.handleEvents)
	// Общее оформление экранов раздаётся из frontend/components.
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(components.StaticFS())))
	mux.HandleFunc("GET /healthz", s.handleHealth)

	return mux
}

// newPages собирает набор шаблонов дашборда и отчётности: каркас и общие
// фрагменты из frontend/components плюс страницы экранов.
func newPages() (*components.Set, error) {
	return components.New(templateFS, pageDashboard, pageReports)
}

// handleRoot обслуживает GET /{$}: корень сервиса ведёт на дашборд, а не
// отвечает 404.
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

// handleHealth обслуживает GET /healthz: сервис запущен и принимает запросы.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// handleDashboard обслуживает GET /dashboard: HTML-дашборд двух слоёв.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	role := roleOf(r)
	if !s.requireAccess(w, r, role, ports.ResourceDashboard) {
		return
	}
	view := s.deps.Dashboard.Handle(r.Context(), query.DashboardQuery{
		TenantID:  r.URL.Query().Get("tenant_id"),
		Role:      role,
		CompanyID: r.URL.Query().Get("company_id"),
	})
	s.render(w, http.StatusOK, pageDashboard, components.Page{
		Layout:  components.Layout{Title: "Дашборд метрик"},
		Sidebar: components.ConsoleNav(s.console, components.ConsoleSectionReports),
		View:    view,
	})
}

// handleStream обслуживает GET /dashboard/stream: поток серверных событий (SSE)
// с дельта-обновлением счётчиков. Подписчик получает текущий срез сразу и свежий
// при каждом изменении — без ручного обновления страницы. Сырые события клиенту
// не транслируются: только агрегированные счётчики.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	role := roleOf(r)
	if !s.requireAccess(w, r, role, ports.ResourceDashboard) {
		return
	}
	tenant := strings.TrimSpace(r.URL.Query().Get("tenant_id"))

	// Заголовки потока: без кэша и без буферизации обратным прокси.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	rc := http.NewResponseController(w)
	send := func(event string, payload any) bool {
		data, err := json.Marshal(payload)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
			return false
		}
		return rc.Flush() == nil
	}

	notify, cancel := s.deps.Dashboard.Watch(tenant)
	defer cancel()

	// Начальный кадр: подписчик сразу видит текущий срез.
	if !send("metrics", s.streamPayload(r, tenant, role)) {
		return
	}

	ticker := time.NewTicker(streamHeartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-notify:
			if !send("metrics", s.streamPayload(r, tenant, role)) {
				return
			}
		case <-ticker.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			if rc.Flush() != nil {
				return
			}
		}
	}
}

// streamPayload собирает тело кадра потока: только счётчики и их производные.
func (s *Server) streamPayload(r *http.Request, tenant, role string) streamPayload {
	view := s.deps.Dashboard.Handle(r.Context(), query.DashboardQuery{TenantID: tenant, Role: role})
	return streamPayload{
		Visible:       view.Visible,
		Found:         view.Progress.Found,
		Communicated:  view.Progress.Communicated,
		Replied:       view.Progress.Replied,
		RepliedShare:  view.Progress.RepliedShare,
		Warmed:        view.Progress.Warmed,
		InProgress:    view.Operations.InProgress,
		Waiting:       view.Operations.Waiting,
		WaitingMaxAge: view.Operations.WaitingMaxAge,
		AverageWait:   view.Operations.AverageWait,
		Cold:          view.Operations.Cold,
		NotResponding: view.Operations.NotResponding,
		Queue:         view.Operations.Queue,
	}
}

// streamPayload — дельта счётчиков, отдаваемая кадром потока. Метрик открытий
// здесь нет по построению.
type streamPayload struct {
	Visible       bool   `json:"visible"`
	Found         int    `json:"found"`
	Communicated  int    `json:"communicated"`
	Replied       int    `json:"replied"`
	RepliedShare  string `json:"replied_share"`
	Warmed        int    `json:"warmed"`
	InProgress    int    `json:"in_progress"`
	Waiting       int    `json:"waiting"`
	WaitingMaxAge string `json:"waiting_max_age"`
	AverageWait   string `json:"average_wait"`
	Cold          int    `json:"cold"`
	NotResponding int    `json:"not_responding"`
	Queue         int    `json:"queue"`
}

// handleEvents обслуживает POST /dashboard/events: приём нормализованного
// события для тестов и dev-контура. Это write-путь, а не клиентский read-путь:
// рабочий контур питает проекцию воркером из брокера.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if s.deps.Events == nil {
		http.Error(w, "приём событий не подключён", http.StatusServiceUnavailable)
		return
	}
	var dto eventDTO
	if err := decodeJSON(w, r, &dto); err != nil {
		http.Error(w, "разбор события: "+err.Error(), http.StatusBadRequest)
		return
	}
	occurred, err := parseOccurred(dto.OccurredAt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.deps.Events.Handle(r.Context(), command.ApplyEvent{
		TenantID:   dto.TenantID,
		Kind:       dto.Kind,
		CompanyID:  dto.CompanyID,
		Status:     dto.Status,
		Outcome:    dto.Outcome,
		OccurredAt: occurred,
		ReplyText:  dto.ReplyText,

		Source:     dto.Source,
		Industry:   dto.Industry,
		Segment:    dto.Segment,
		CampaignID: dto.CampaignID,
		ManagerID:  dto.ManagerID,
	}); err != nil {
		// Событие принято проекцией, но снимок не записан на носитель: честный
		// отказ, а не успех без эффекта.
		http.Error(w, "применение события: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}

// eventDTO — тело запроса приёма события. Поле reply_text принимается для
// совместимости с источником, но в проекции не сохраняется.
type eventDTO struct {
	TenantID   string `json:"tenant_id"`
	Kind       string `json:"kind"`
	CompanyID  string `json:"company_id"`
	Status     string `json:"status"`
	Outcome    string `json:"outcome"`
	OccurredAt string `json:"occurred_at"`
	ReplyText  string `json:"reply_text"`

	Source     string `json:"source"`
	Industry   string `json:"industry"`
	Segment    string `json:"segment"`
	CampaignID string `json:"campaign_id"`
	ManagerID  string `json:"manager_id"`
}

// requireAccess проверяет доступ роли к ресурсу через порт решения. Отказ — 403,
// недоступность владельца политики — 503; открытого доступа по умолчанию нет.
func (s *Server) requireAccess(w http.ResponseWriter, r *http.Request, role, resource string) bool {
	if s.deps.Access == nil {
		http.Error(w, "решение о доступе не подключено", http.StatusServiceUnavailable)
		return false
	}
	allowed, err := s.deps.Access.Allow(r.Context(), role, resource)
	if err != nil {
		http.Error(w, "решение о доступе недоступно", http.StatusServiceUnavailable)
		return false
	}
	if !allowed {
		http.Error(w, "доступ запрещён", http.StatusForbidden)
		return false
	}
	return true
}

// render рисует страницу в буфер: частично отданный HTML выглядел бы сломанной
// страницей вместо честного отказа.
func (s *Server) render(w http.ResponseWriter, status int, page string, model components.Page) {
	if s.pages == nil {
		http.Error(w, "шаблоны дашборда не собраны", http.StatusInternalServerError)
		return
	}
	// Экран без собственных таблиц стилей получает общее оформление компонентов:
	// внешний вид дашборда не расходится с другими серверными экранами.
	if len(model.Layout.Stylesheets) == 0 {
		model.Layout.Stylesheets = components.ConsoleStylesheets
	}
	if len(model.Layout.BootstrapScripts) == 0 {
		model.Layout.BootstrapScripts = components.ConsoleBootstrapScripts
	}
	var buf bytes.Buffer
	if err := s.pages.Render(&buf, page, model); err != nil {
		http.Error(w, "ошибка рендеринга дашборда", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentTypeHTML)
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// roleOf возвращает роль запроса из параметра или заголовка, подставляя
// значение по умолчанию. Контур без аутентификации: настоящие роли —
// platform-governance.
func roleOf(r *http.Request) string {
	for _, value := range []string{r.URL.Query().Get("role"), r.Header.Get("X-Role")} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return defaultRole
}

// parseOccurred разбирает время события: пустая строка — нулевое время (проекция
// отнесёт компанию к дню без точки роста), иначе — RFC3339.
func parseOccurred(value string) (time.Time, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return time.Time{}, errors.New("поле occurred_at должно быть в формате RFC3339")
	}
	return parsed, nil
}

// decodeJSON читает тело запроса в dst, отвергая неизвестные поля.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	if r.Body == nil {
		return errors.New("тело запроса отсутствует")
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("разбор JSON: %w", err)
	}
	return nil
}

// writeJSON отправляет полезную нагрузку с заданным статусом.
func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	if payload == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(payload)
}
