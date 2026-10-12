package httpapi

import (
	"context"
	"encoding/csv"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"lead-gen/backend/contexts/platform-governance/internal/app"
	"lead-gen/backend/contexts/platform-governance/internal/app/command"
	"lead-gen/backend/contexts/platform-governance/internal/app/query"
	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/backend/contexts/platform-governance/internal/ports"
	"lead-gen/backend/platform/consoleauth"
	"lead-gen/backend/platform/consolescope"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/frontend/components"
)

// consoleServiceName — имя сервиса в структурированных логах консоли. Совпадает
// с идентификатором сервиса развёртывания.
const consoleServiceName = "platform-governance"

// Экранное управление доступом (срез 5 web-frontend-architecture): пользователи
// и роли, интеграции, журнал аудита с выгрузкой и переключение тенанта.
//
// Каждый экран и каждое действие проходят серверную проверку по матрице
// app.AuthorizeSection: роли актора приходят из снимка access-decision владельца,
// а не из шаблона. Недоступное действие отвечает отказом на сервере независимо от
// того, показан ли пункт навигации.

// roleOption — вариант роли для формы назначения: значение и подпись.
type roleOption struct {
	Value string
	Label string
}

// roleOptions — роли, объявленные доменом, как варианты формы. Их перечисляет
// owning context: консоль не заводит собственного списка ролей. Подписи берутся
// из каталога сообщений (срез 7, 7.1).
func (s *Server) roleOptions() []roleOption {
	return []roleOption{
		{Value: domain.RoleAdmin.String(), Label: s.messages.Text(msgConsoleRoleAdmin)},
		{Value: domain.RoleClient.String(), Label: s.messages.Text(msgConsoleRoleClient)},
	}
}

// consoleRoleRow — роль пользователя в таблице.
type consoleRoleRow struct {
	Value string
	Label string
	Class string
}

// consoleRevokeForm — форма отзыва одной роли: уникальный ключ команды на строку.
type consoleRevokeForm struct {
	UserID    string
	Role      string
	CommandID string
}

// consoleUserRow — строка состава пользователей тенанта.
type consoleUserRow struct {
	ID     string
	Login  string
	Roles  []consoleRoleRow
	Revoke []consoleRevokeForm
}

// consoleUsersView — модель экрана «Пользователи и роли».
type consoleUsersView struct {
	TenantID        string
	Actor           string
	CSRF            string
	Notice          string
	Users           []consoleUserRow
	UserField       components.Field
	RoleOptions     []roleOption
	AssignCommandID string
}

// consoleAdapterRow — строка раздела «Интеграции»: только нормализованные
// состояние и причина. Свободный текст владельца адаптеров сюда не попадает.
type consoleAdapterRow struct {
	Adapter     string
	StateLabel  string
	Class       string
	ReasonLabel string
}

// consoleIntegrationsView — модель раздела «Интеграции и адаптеры».
type consoleIntegrationsView struct {
	Unavailable bool
	Adapters    []consoleAdapterRow
}

// consoleAuditRow — строка журнала аудита, время уже отформатировано.
type consoleAuditRow struct {
	At           string
	Actor        string
	Action       string
	Object       string
	Outcome      string
	ID           string
	CorrectionOf string
}

// consoleAuditView — модель экрана «Журнал аудита».
type consoleAuditView struct {
	TenantID  string
	Notice    string
	Entries   []consoleAuditRow
	ExportURL string
}

// consoleTenantRow — строка списка тенантов: платформенные атрибуты и форма
// выбора. Логины и контакты пользователей сюда не попадают — источник
// (query.TenantView) их не отдаёт; идентифицирующие данные компании используются
// только разделом «Регистрации».
type consoleTenantRow struct {
	ID          string
	CompanyName string
	StateLabel  string
	Class       string
	Active      bool
}

// consoleTenantView — модель экрана «Выбор тенанта».
type consoleTenantView struct {
	TenantID string
	Actor    string
	CSRF     string
	Notice   string
	Tenants  []consoleTenantRow
}

// deniedView — модель экрана отказа в доступе.
type deniedView struct {
	Heading string
	Message string
	Status  int
}

// consoleActor извлекает субъекта действия: из сессии оператора консоли (когда
// вход включён), затем из формы (POST), запроса или заголовка X-Actor. Пустой
// субъект операцией не считается: серверный страж отвергает его до принятия
// решения.
func consoleActor(r *http.Request) string {
	if session := consoleauth.Current(r.Context()); session != nil {
		if actor := strings.TrimSpace(session.Username()); actor != "" {
			return actor
		}
	}
	if r.PostForm != nil {
		if actor := strings.TrimSpace(r.PostFormValue("actor")); actor != "" {
			return actor
		}
	}
	if actor := strings.TrimSpace(r.URL.Query().Get("actor")); actor != "" {
		return actor
	}
	return strings.TrimSpace(r.Header.Get(headerActor))
}

// sessionCSRF возвращает CSRF-токен сессии оператора консоли для формы. Пусто,
// если вход консоли не включён (?actor=, dev): тогда сессионной защиты нет.
func sessionCSRF(r *http.Request) string {
	if session := consoleauth.Current(r.Context()); session != nil {
		return session.CSRF()
	}
	return ""
}

// activeTenant определяет активный тенант экранного запроса: `tenant_id` из
// строки запроса, затем из формы, затем — выбранный оператором тенант из cookie
// консоли (`consolescope`). Единая точка определения для всех консольных
// экранов: выбранный на экране выбора тенант переносится между разделами без
// ручного ввода. Пусто — тенант не выбран.
func (s *Server) activeTenant(r *http.Request) string {
	if tenant := strings.TrimSpace(r.URL.Query().Get("tenant_id")); tenant != "" {
		return tenant
	}
	if r.PostForm != nil {
		if tenant := strings.TrimSpace(r.PostFormValue("tenant_id")); tenant != "" {
			return tenant
		}
	}
	return consolescope.ActiveTenant(r)
}

// requireTenant возвращает активный тенант или, если он не выбран, перенаправляет
// платформенного оператора на экран выбора тенанта. Второе значение ложно, если
// ответ уже отправлен (редирект).
func (s *Server) requireTenant(w http.ResponseWriter, r *http.Request, actor string) (string, bool) {
	tenant := s.activeTenant(r)
	if tenant != "" {
		return tenant, true
	}
	http.Redirect(w, r, consoleURL("/console/tenant", "", actor, ""), http.StatusSeeOther)
	return "", false
}

// decideSection принимает решение о доступе на сервере и возвращает статус и
// сообщение отказа (0 — доступ разрешён). Роли актора читаются из снимка
// access-decision владельца, затем применяется матрица «раздел → действие →
// право». Общая точка для страниц и JSON-эндпоинтов консоли.
func (s *Server) decideSection(r *http.Request, tenantID string, section app.Section, action app.Action) (int, string) {
	if s.deps.DecideAccess == nil {
		return http.StatusServiceUnavailable, "Проверка доступа недоступна"
	}
	actor := consoleActor(r)
	if actor == "" {
		return http.StatusUnauthorized, "Не задан субъект действия"
	}
	roles, err := s.deps.DecideAccess.Roles(r.Context(), domain.TenantID(strings.TrimSpace(tenantID)), domain.ActorID(actor))
	if err != nil {
		return consoleAccessError(err)
	}
	if err := app.AuthorizeSection(roles, section, action); err != nil {
		return http.StatusForbidden, "Действие недоступно для вашей роли"
	}
	return 0, ""
}

// consoleGuard принимает решение о доступе на сервере. Отказ рисуется страницей и
// возвращает false. Отказ не зависит от навигации: матрица решает одинаково для
// страницы и для действия.
func (s *Server) consoleGuard(w http.ResponseWriter, r *http.Request, tenantID string, section app.Section, action app.Action) bool {
	if status, message := s.decideSection(r, tenantID, section, action); status != 0 {
		s.renderConsoleDenied(w, status, message)
		return false
	}
	return true
}

// consoleAccessError классифицирует сбой чтения ролей актора.
func consoleAccessError(err error) (int, string) {
	switch {
	case errors.Is(err, app.ErrMissingActor):
		return http.StatusUnauthorized, "Не задан субъект действия"
	case errors.Is(err, domain.ErrEmptyID):
		return http.StatusBadRequest, "Не задан тенант"
	case errors.Is(err, ports.ErrTenantNotFound):
		return http.StatusNotFound, "Тенант не найден"
	default:
		return http.StatusInternalServerError, "Не удалось проверить доступ"
	}
}

// handleConsoleRoot обслуживает GET /console: точка входа в раздел управления.
// Ведёт на раздел регистраций — он доступен платформенному оператору без
// выбранного тенанта, в отличие от «Пользователей», требующих тенант.
func (s *Server) handleConsoleRoot(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, consoleURL("/console/registrations", s.activeTenant(r), consoleActor(r), ""), http.StatusSeeOther)
}

// handleConsoleTenant обслуживает GET /console/tenant: список тенантов и выбор
// активного. Доступ к списку решается матрицей по активному тенанту
// (SectionTenant/View); само переключение проверяется отдельно в
// handleConsoleTenantSwitch — видимость формы не заменяет серверную проверку.
func (s *Server) handleConsoleTenant(w http.ResponseWriter, r *http.Request) {
	tenant := s.activeTenant(r)
	actor := consoleActor(r)
	if strings.TrimSpace(tenant) == "" {
		// Список тенантов открывается без активного тенанта: право на него даёт
		// принадлежность к платформенным операторам, а не роль в конкретном
		// тенанте — иначе выбор тенанта недостижим («курица и яйцо»).
		if !s.platformOperator(actor) {
			s.renderConsoleDenied(w, http.StatusBadRequest, "Не задан тенант")
			return
		}
	} else if !s.consoleGuard(w, r, tenant, app.SectionTenant, app.ActionView) {
		return
	}
	tenants, err := s.deps.ListTenants.Handle(r.Context(), query.ListTenants{})
	if err != nil {
		s.render(w, adminStatus(err), pageConsoleTenant, components.Page{
			Layout:  components.Layout{Title: "Выбор тенанта"},
			Sidebar: s.consoleNav("/console/tenant", tenant, actor),
			View:    consoleTenantView{TenantID: tenant, Actor: actor, Notice: adminMessage(err)},
		})
		return
	}
	s.render(w, http.StatusOK, pageConsoleTenant, components.Page{
		Layout:  components.Layout{Title: "Выбор тенанта"},
		Sidebar: s.consoleNav("/console/tenant", tenant, actor),
		View: consoleTenantView{
			TenantID: tenant,
			Actor:    actor,
			CSRF:     sessionCSRF(r),
			Notice:   consoleNotice(r.URL.Query().Get("notice")),
			Tenants:  tenantRows(tenants, tenant),
		},
	})
}

// platformOperator сообщает, что субъект действует в платформенном масштабе
// (оператор во всех тенантах). Пустой субъект или неподключённый владелец доступа
// оператором не считается.
func (s *Server) platformOperator(actor string) bool {
	if s.deps.DecideAccess == nil {
		return false
	}
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return false
	}
	return s.deps.DecideAccess.IsOperator(domain.ActorID(actor))
}

// handleConsoleUsers обслуживает GET /console/users: состав пользователей и ролей
// тенанта.
func (s *Server) handleConsoleUsers(w http.ResponseWriter, r *http.Request) {
	actor := consoleActor(r)
	tenant, ok := s.requireTenant(w, r, actor)
	if !ok {
		return
	}
	if !s.consoleGuard(w, r, tenant, app.SectionUsers, app.ActionView) {
		return
	}
	users, err := s.deps.TenantUsers.Handle(r.Context(), query.TenantUsers{TenantID: tenant})
	if err != nil {
		s.renderConsoleUsers(w, r, adminStatus(err), tenant, actor, adminMessage(err))
		return
	}
	s.renderConsoleUsers(w, r, http.StatusOK, tenant, actor, consoleNotice(r.URL.Query().Get("notice")), users...)
}

// handleConsoleAssignRole обслуживает POST /console/users/roles/assign. Действие
// проверяется на сервере и после успеха возвращается редиректом (PRG).
func (s *Server) handleConsoleAssignRole(w http.ResponseWriter, r *http.Request) {
	s.handleConsoleRoleCommand(w, r, app.ActionManage, func(actor, tenant string, form url.Values) error {
		return s.deps.AssignRole.Handle(r.Context(), command.AssignRole{
			Actor:     actor,
			CommandID: form.Get("command_id"),
			TenantID:  tenant,
			UserID:    form.Get("user_id"),
			Role:      form.Get("role"),
		})
	}, "assigned")
}

// handleConsoleRevokeRole обслуживает POST /console/users/roles/revoke.
func (s *Server) handleConsoleRevokeRole(w http.ResponseWriter, r *http.Request) {
	s.handleConsoleRoleCommand(w, r, app.ActionManage, func(actor, tenant string, form url.Values) error {
		return s.deps.RevokeRole.Handle(r.Context(), command.RevokeRole{
			Actor:     actor,
			CommandID: form.Get("command_id"),
			TenantID:  tenant,
			UserID:    form.Get("user_id"),
			Role:      form.Get("role"),
		})
	}, "revoked")
}

// handleConsoleRoleCommand — общий путь изменения роли: серверная проверка,
// выполнение команды owning context и POST/redirect/GET. Ошибка возвращает форму
// с сообщением и введёнными значениями, а не «немой» отказ.
func (s *Server) handleConsoleRoleCommand(w http.ResponseWriter, r *http.Request, action app.Action, run func(actor, tenant string, form url.Values) error, okNotice string) {
	if err := r.ParseForm(); err != nil {
		s.renderPlainError(w, http.StatusBadRequest, "разбор формы: "+err.Error())
		return
	}
	tenant := strings.TrimSpace(r.PostFormValue("tenant_id"))
	actor := consoleActor(r)
	if !s.consoleGuard(w, r, tenant, app.SectionUsers, action) {
		return
	}
	if err := run(actor, tenant, r.PostForm); err != nil {
		s.renderConsoleUsers(w, r, adminStatus(err), tenant, actor, adminMessage(err))
		return
	}
	http.Redirect(w, r, consoleURL("/console/users", tenant, actor, okNotice), http.StatusSeeOther)
}

// handleConsoleIntegrations обслуживает GET /console/integrations: состояние
// адаптеров интеграций. Недоступность владельца показывается честно, а не пустым
// успехом.
func (s *Server) handleConsoleIntegrations(w http.ResponseWriter, r *http.Request) {
	actor := consoleActor(r)
	tenant, ok := s.requireTenant(w, r, actor)
	if !ok {
		return
	}
	if !s.consoleGuard(w, r, tenant, app.SectionIntegrations, app.ActionView) {
		return
	}
	view := consoleIntegrationsView{}
	status := http.StatusOK
	states, err := s.adapterStates(r.Context())
	if err != nil {
		// Владелец адаптеров недоступен: показываем честное состояние и не
		// выдаём технические детали. Текст ошибки в разметку не переносится.
		view.Unavailable = true
		status = statusForError(err)
	} else {
		view.Adapters = adapterRows(states)
	}
	s.render(w, status, pageConsoleIntegrations, components.Page{
		Layout:  components.Layout{Title: "Интеграции"},
		Sidebar: s.consoleNav("/console/integrations", tenant, actor),
		View:    view,
	})
}

// handleConsoleAudit обслуживает GET /console/audit: просмотр журнала аудита.
func (s *Server) handleConsoleAudit(w http.ResponseWriter, r *http.Request) {
	actor := consoleActor(r)
	tenant, ok := s.requireTenant(w, r, actor)
	if !ok {
		return
	}
	if !s.consoleGuard(w, r, tenant, app.SectionAudit, app.ActionView) {
		return
	}
	entries, err := s.deps.AuditJournal.Handle(r.Context(), query.AuditJournal{TenantID: tenant})
	if err != nil {
		s.render(w, adminStatus(err), pageConsoleAudit, components.Page{
			Layout:  components.Layout{Title: "Журнал аудита"},
			Sidebar: s.consoleNav("/console/audit", tenant, actor),
			View:    consoleAuditView{TenantID: tenant, Notice: adminMessage(err)},
		})
		return
	}
	s.render(w, http.StatusOK, pageConsoleAudit, components.Page{
		Layout:  components.Layout{Title: "Журнал аудита"},
		Sidebar: s.consoleNav("/console/audit", tenant, actor),
		View: consoleAuditView{
			TenantID:  tenant,
			Entries:   auditRows(entries),
			ExportURL: consoleURL("/console/audit/export", tenant, actor, ""),
		},
	})
}

// handleConsoleAuditExport обслуживает GET /console/audit/export: выгрузка
// журнала в CSV. Журнал только читается — правка записей невозможна. Ответ
// персональных данных не кэшируется.
func (s *Server) handleConsoleAuditExport(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r, consoleActor(r))
	if !ok {
		return
	}
	if !s.consoleGuard(w, r, tenant, app.SectionAudit, app.ActionExport) {
		return
	}
	entries, err := s.deps.AuditJournal.Handle(r.Context(), query.AuditJournal{TenantID: tenant})
	if err != nil {
		s.renderConsoleDenied(w, adminStatus(err), adminMessage(err))
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="audit-`+safeFilename(tenant)+`.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{"id", "at", "actor", "action", "object", "outcome", "correction_of"})
	for _, entry := range entries {
		_ = writer.Write([]string{
			entry.ID,
			entry.At.UTC().Format(time.RFC3339),
			entry.Actor,
			entry.Action,
			entry.Object,
			entry.Outcome,
			entry.CorrectionOf,
		})
	}
	writer.Flush()
}

// handleConsoleTenantSwitch обслуживает POST /console/tenant/switch: переключение
// тенанта. Переключиться можно только на тенант, где актор имеет роль
// администратора (решение матрицы по снимку access-decision); факт переключения
// фиксируется структурированным логом.
func (s *Server) handleConsoleTenantSwitch(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderPlainError(w, http.StatusBadRequest, "разбор формы: "+err.Error())
		return
	}
	tenant := strings.TrimSpace(r.PostFormValue("tenant_id"))
	actor := consoleActor(r)
	if !s.consoleGuard(w, r, tenant, app.SectionTenant, app.ActionSwitch) {
		return
	}
	if s.deps.Logger != nil {
		s.deps.Logger.Info("console: переключение тенанта",
			observ.Fields{}, "service", consoleServiceName, "actor", actor, "tenant_id", tenant)
	}
	// Выбор активного тенанта сохраняется в cookie консоли: остальные консоли
	// (другие порты того же хоста) читают его как фолбэк, когда в запросе нет
	// tenant_id, — выбранный тенант переносится без ручного ввода.
	consolescope.SetActiveTenant(w, r, tenant)
	http.Redirect(w, r, consoleURL("/console/users", tenant, actor, "switched"), http.StatusSeeOther)
}

// renderConsoleUsers рисует экран пользователей с указанным статусом, собирая
// состав на сервере.
func (s *Server) renderConsoleUsers(w http.ResponseWriter, r *http.Request, status int, tenant, actor, notice string, users ...query.TenantUserView) {
	view := s.consoleUsersView(tenant, actor, notice, users)
	view.CSRF = sessionCSRF(r)
	s.render(w, status, pageConsoleUsers, components.Page{
		Layout:  components.Layout{Title: "Пользователи и роли"},
		Sidebar: s.consoleNav("/console/users", tenant, actor),
		View:    view,
	})
}

// consoleUsersView собирает модель экрана пользователей, назначая каждой строке
// уникальный ключ команды отзыва.
func (s *Server) consoleUsersView(tenant, actor, notice string, users []query.TenantUserView) consoleUsersView {
	rows := make([]consoleUserRow, 0, len(users))
	for _, user := range users {
		row := consoleUserRow{ID: user.ID, Login: user.Login}
		for _, role := range user.Roles {
			row.Roles = append(row.Roles, consoleRoleRow{
				Value: role,
				Label: role,
				Class: components.StatusClass(role),
			})
			row.Revoke = append(row.Revoke, consoleRevokeForm{
				UserID:    user.ID,
				Role:      role,
				CommandID: s.commandID(),
			})
		}
		rows = append(rows, row)
	}
	return consoleUsersView{
		TenantID: tenant,
		Actor:    actor,
		Notice:   notice,
		Users:    rows,
		UserField: components.Field{
			ID: "console-user-id", Name: "user_id", Label: "Идентификатор пользователя", Required: true,
		},
		RoleOptions:     s.roleOptions(),
		AssignCommandID: s.commandID(),
	}
}

// commandID возвращает новый ключ идемпотентности для экранной формы.
func (s *Server) commandID() string {
	if s.deps.IDs == nil {
		return ""
	}
	return s.deps.IDs.NewEventID().String()
}

// adapterStates читает состояние адаптеров через use case. Неподключённый порт
// и сбой владельца — один честный исход app.ErrUnavailable.
func (s *Server) adapterStates(ctx context.Context) ([]ports.AdapterStatus, error) {
	if s.deps.Adapters == nil {
		return nil, app.ErrUnavailable
	}
	return s.deps.Adapters.Handle(ctx)
}

// adapterRows переводит состояние адаптеров в строки раздела. Состояние и
// причина показываются нормализованными подписями из закрытого набора, а не
// текстом владельца.
func adapterRows(states []ports.AdapterStatus) []consoleAdapterRow {
	rows := make([]consoleAdapterRow, 0, len(states))
	for _, state := range states {
		rows = append(rows, consoleAdapterRow{
			Adapter:     state.Adapter,
			StateLabel:  adapterStateLabel(state.State),
			Class:       adapterStateClass(state.State),
			ReasonLabel: adapterReasonLabel(state.Reason),
		})
	}
	return rows
}

// adapterStateLabel переводит нормализованный код состояния в подпись оператору.
// Неизвестное состояние (в том числе у несконфигурированного адаптера) —
// «неизвестно», а не «в норме».
func adapterStateLabel(state string) string {
	switch state {
	case ports.AdapterStateUp:
		return "работает"
	case ports.AdapterStateDegraded:
		return "деградация"
	case ports.AdapterStateDown:
		return "недоступен"
	default:
		return "неизвестно"
	}
}

// adapterStateClass сопоставляет состояние CSS-классу общего бейджа. Классы
// принадлежат общему оформлению консоли (frontend/components); раздел не вводит
// собственных классов и не передаёт в разметку код состояния как есть.
func adapterStateClass(state string) string {
	switch state {
	case ports.AdapterStateUp:
		return "done"
	case ports.AdapterStateDegraded:
		return "running"
	case ports.AdapterStateDown:
		return "failed"
	default:
		return "queued"
	}
}

// adapterReasonLabel переводит нормализованный код причины в подпись. Набор
// закрыт: произвольный текст не показывается.
func adapterReasonLabel(reason string) string {
	switch reason {
	case ports.AdapterReasonUnreachable:
		return "не отвечает"
	case ports.AdapterReasonAuthFailed:
		return "отказ аутентификации"
	case ports.AdapterReasonRateLimited:
		return "ограничение частоты"
	case ports.AdapterReasonDegraded:
		return "деградация"
	case ports.AdapterReasonMisconfigured:
		return "некорректная настройка"
	default:
		return ""
	}
}

// tenantRows переводит список тенантов в строки экрана. Состояние показывается
// объявленной подписью, а не кодом домена; активный тенант отмечается флагом,
// чтобы форма выбора не предлагала повторно выбрать текущий.
func tenantRows(tenants []query.TenantView, active string) []consoleTenantRow {
	rows := make([]consoleTenantRow, 0, len(tenants))
	for _, tenant := range tenants {
		rows = append(rows, consoleTenantRow{
			ID:          tenant.ID,
			CompanyName: tenant.CompanyName,
			StateLabel:  tenantStateLabel(tenant.State),
			Class:       components.StatusClass(tenant.State),
			Active:      tenant.ID == active,
		})
	}
	return rows
}

// tenantStateLabel переводит состояние тенанта в подпись оператору. Неизвестное
// состояние — «неизвестно», а не «активен».
func tenantStateLabel(state string) string {
	switch state {
	case string(domain.TenantActive):
		return "активен"
	case string(domain.TenantDisabled):
		return "отключён"
	default:
		return "неизвестно"
	}
}

// auditRows переводит записи журнала в строки экрана.
func auditRows(entries []query.AuditEntry) []consoleAuditRow {
	rows := make([]consoleAuditRow, 0, len(entries))
	for _, entry := range entries {
		rows = append(rows, consoleAuditRow{
			At:           entry.At.UTC().Format(time.RFC3339),
			Actor:        entry.Actor,
			Action:       entry.Action,
			Object:       entry.Object,
			Outcome:      entry.Outcome,
			ID:           entry.ID,
			CorrectionOf: entry.CorrectionOf,
		})
	}
	return rows
}

// renderConsoleDenied рисует экран отказа без общей навигации: отказ не зависит
// от того, какие пункты показаны.
func (s *Server) renderConsoleDenied(w http.ResponseWriter, status int, message string) {
	s.render(w, status, pageConsoleDenied, components.Page{
		Layout: components.Layout{Title: "Доступ ограничен"},
		View:   deniedView{Heading: "Доступ ограничен", Message: message, Status: status},
	})
}

// consoleNav собирает навигацию админских экранов через общий слой: канонический
// список включает и операторские разделы (Заявки, Исполнение, Лиды, Отчёты,
// Рассылки), и платформенные (Тенант, Пользователи, Регистрации, Интеграции,
// Аудит, Топология, Операции). Ссылки несут выбранный тенант и актора; активен
// открытый раздел — общий слой отмечает его по пути раздела.
func (s *Server) consoleNav(active, tenant, actor string) components.Nav {
	ctx := components.ConsoleContext{TenantID: tenant, Actor: actor}
	return components.ConsoleNavMessagesWithContext(s.messages, s.console, active, ctx)
}

// consoleURL собирает адрес экрана с необязательными тенантом, актором и
// сообщением. Пустые значения в запрос не попадают.
func consoleURL(base, tenant, actor, notice string) string {
	values := url.Values{}
	if tenant = strings.TrimSpace(tenant); tenant != "" {
		values.Set("tenant_id", tenant)
	}
	if actor = strings.TrimSpace(actor); actor != "" {
		values.Set("actor", actor)
	}
	if notice = strings.TrimSpace(notice); notice != "" {
		values.Set("notice", notice)
	}
	if encoded := values.Encode(); encoded != "" {
		return base + "?" + encoded
	}
	return base
}

// consoleNotice переводит машинный код сообщения в текст. Произвольный текст из
// запроса не отражается: набор сообщений закрыт.
func consoleNotice(code string) string {
	switch code {
	case "assigned":
		return "Роль назначена"
	case "revoked":
		return "Роль отозвана"
	case "switched":
		return "Тенант переключён"
	case "registration_approved":
		return "Заявка на регистрацию подтверждена"
	case "registration_rejected":
		return "Заявка на регистрацию отклонена"
	case "profile_approved":
		return "Профиль подтверждён"
	case "profile_rejected":
		return "Профиль отклонён"
	case "panel_hidden":
		return "Панель скрыта"
	case "panel_shown":
		return "Панель показана"
	default:
		return ""
	}
}

// safeFilename оставляет в имени файла только безопасные символы.
func safeFilename(value string) string {
	var builder strings.Builder
	builder.Grow(len(value))
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			builder.WriteRune(r)
		}
	}
	if builder.Len() == 0 {
		return "tenant"
	}
	return builder.String()
}
