package httpapi

import (
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
// owning context: консоль не заводит собственного списка ролей.
func roleOptions() []roleOption {
	return []roleOption{
		{Value: domain.RoleAdmin.String(), Label: "Администратор платформы"},
		{Value: domain.RoleClient.String(), Label: "Клиент"},
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
	Notice          string
	Users           []consoleUserRow
	UserField       components.Field
	RoleOptions     []roleOption
	AssignCommandID string
}

// consoleAdapterRow — строка раздела «Интеграции».
type consoleAdapterRow struct {
	Name        string
	Version     string
	Health      string
	Class       string
	ReasonLabel string
}

// consoleIntegrationsView — модель раздела «Интеграции и адаптеры».
type consoleIntegrationsView struct {
	Notice      string
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

// deniedView — модель экрана отказа в доступе.
type deniedView struct {
	Heading string
	Message string
	Status  int
}

// consoleActor извлекает субъекта действия: из формы (POST), запроса или
// заголовка X-Actor. Аутентификация вне среза (dev-контур), но пустой субъект
// операцией не считается: серверный страж отвергает его до принятия решения.
func consoleActor(r *http.Request) string {
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

// consoleGuard принимает решение о доступе на сервере. Роли актора читаются из
// агрегата tenant (снимок access-decision), затем применяется матрица «раздел →
// действие → право». Отказ рисуется страницей и возвращает false.
func (s *Server) consoleGuard(w http.ResponseWriter, r *http.Request, tenantID string, section app.Section, action app.Action) bool {
	if s.deps.DecideAccess == nil {
		s.renderConsoleDenied(w, http.StatusServiceUnavailable, "Проверка доступа недоступна")
		return false
	}
	actor := consoleActor(r)
	if actor == "" {
		s.renderConsoleDenied(w, http.StatusUnauthorized, "Не задан субъект действия")
		return false
	}
	roles, err := s.deps.DecideAccess.Roles(r.Context(), domain.TenantID(strings.TrimSpace(tenantID)), domain.ActorID(actor))
	if err != nil {
		status, message := consoleAccessError(err)
		s.renderConsoleDenied(w, status, message)
		return false
	}
	if err := app.AuthorizeSection(roles, section, action); err != nil {
		// Отказ не зависит от навигации: матрица решает одинаково для страницы и
		// для действия.
		s.renderConsoleDenied(w, http.StatusForbidden, "Действие недоступно для вашей роли")
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
func (s *Server) handleConsoleRoot(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, consoleURL("/console/users", r.URL.Query().Get("tenant_id"), consoleActor(r), ""), http.StatusSeeOther)
}

// handleConsoleUsers обслуживает GET /console/users: состав пользователей и ролей
// тенанта.
func (s *Server) handleConsoleUsers(w http.ResponseWriter, r *http.Request) {
	tenant := r.URL.Query().Get("tenant_id")
	actor := consoleActor(r)
	if !s.consoleGuard(w, r, tenant, app.SectionUsers, app.ActionView) {
		return
	}
	users, err := s.deps.TenantUsers.Handle(r.Context(), query.TenantUsers{TenantID: tenant})
	if err != nil {
		s.renderConsoleUsers(w, adminStatus(err), tenant, actor, adminMessage(err))
		return
	}
	s.renderConsoleUsers(w, http.StatusOK, tenant, actor, consoleNotice(r.URL.Query().Get("notice")), users...)
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
		s.renderConsoleUsers(w, adminStatus(err), tenant, actor, adminMessage(err))
		return
	}
	http.Redirect(w, r, consoleURL("/console/users", tenant, actor, okNotice), http.StatusSeeOther)
}

// handleConsoleIntegrations обслуживает GET /console/integrations: состояние
// адаптеров интеграций. Недоступность владельца показывается честно, а не пустым
// успехом.
func (s *Server) handleConsoleIntegrations(w http.ResponseWriter, r *http.Request) {
	tenant := r.URL.Query().Get("tenant_id")
	actor := consoleActor(r)
	if !s.consoleGuard(w, r, tenant, app.SectionIntegrations, app.ActionView) {
		return
	}
	view := consoleIntegrationsView{}
	states, err := s.deps.Adapters.Handle(r.Context())
	if err != nil {
		view.Unavailable = true
		view.Notice = "Состояние интеграций недоступно"
	} else {
		view.Adapters = adapterRows(states)
	}
	s.render(w, http.StatusOK, pageConsoleIntegrations, components.Page{
		Layout:  components.Layout{Title: "Интеграции"},
		Sidebar: consoleNav("/console/integrations", tenant, actor),
		View:    view,
	})
}

// handleConsoleAudit обслуживает GET /console/audit: просмотр журнала аудита.
func (s *Server) handleConsoleAudit(w http.ResponseWriter, r *http.Request) {
	tenant := r.URL.Query().Get("tenant_id")
	actor := consoleActor(r)
	if !s.consoleGuard(w, r, tenant, app.SectionAudit, app.ActionView) {
		return
	}
	entries, err := s.deps.AuditJournal.Handle(r.Context(), query.AuditJournal{TenantID: tenant})
	if err != nil {
		s.render(w, adminStatus(err), pageConsoleAudit, components.Page{
			Layout:  components.Layout{Title: "Журнал аудита"},
			Sidebar: consoleNav("/console/audit", tenant, actor),
			View:    consoleAuditView{TenantID: tenant, Notice: adminMessage(err)},
		})
		return
	}
	s.render(w, http.StatusOK, pageConsoleAudit, components.Page{
		Layout:  components.Layout{Title: "Журнал аудита"},
		Sidebar: consoleNav("/console/audit", tenant, actor),
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
	tenant := r.URL.Query().Get("tenant_id")
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
	http.Redirect(w, r, consoleURL("/console/users", tenant, actor, "switched"), http.StatusSeeOther)
}

// renderConsoleUsers рисует экран пользователей с указанным статусом, собирая
// состав на сервере.
func (s *Server) renderConsoleUsers(w http.ResponseWriter, status int, tenant, actor, notice string, users ...query.TenantUserView) {
	s.render(w, status, pageConsoleUsers, components.Page{
		Layout:  components.Layout{Title: "Пользователи и роли"},
		Sidebar: consoleNav("/console/users", tenant, actor),
		View:    s.consoleUsersView(tenant, actor, notice, users),
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
		RoleOptions:     roleOptions(),
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

// adapterRows переводит состояние адаптеров в строки раздела. Причина
// показывается нормализованной подписью, а не текстом владельца.
func adapterRows(states []ports.AdapterState) []consoleAdapterRow {
	rows := make([]consoleAdapterRow, 0, len(states))
	for _, state := range states {
		rows = append(rows, consoleAdapterRow{
			Name:        state.Name,
			Version:     state.CapabilitySetVersion,
			Health:      string(state.Health),
			Class:       components.StatusClass(string(state.Health)),
			ReasonLabel: adapterReasonLabel(state.Reason),
		})
	}
	return rows
}

// adapterReasonLabel переводит нормализованный код причины в подпись. Набор
// закрыт: произвольный текст не показывается.
func adapterReasonLabel(reason ports.AdapterReason) string {
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

// consoleNav собирает навигацию раздела управления с активным пунктом.
func consoleNav(active, tenant, actor string) components.Nav {
	items := []components.NavItem{
		{Label: "Пользователи", Href: consoleURL("/console/users", tenant, actor, "")},
		{Label: "Интеграции", Href: consoleURL("/console/integrations", tenant, actor, "")},
		{Label: "Аудит", Href: consoleURL("/console/audit", tenant, actor, "")},
	}
	for i := range items {
		items[i].Active = strings.HasPrefix(items[i].Href, active)
	}
	return components.Nav{Items: items}
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
