package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"lead-gen/backend/contexts/platform-governance/internal/app"
	"lead-gen/backend/contexts/platform-governance/internal/app/command"
	"lead-gen/backend/contexts/platform-governance/internal/app/query"
	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/backend/contexts/platform-governance/internal/ports"
	"lead-gen/frontend/components"
)

// Заголовки внутреннего API доступа. Актор приходит проверенным: аутентификация
// вне среза (design.md, Решение 8), граница api аутентификацию не эмулирует и не
// выдаёт «актора из заголовка» за готовую проверку подлинности.
const (
	headerActor     = "X-Actor"
	headerCommandID = "X-Command-Id"
)

// createTenantRequest — тело команды создания тенанта.
type createTenantRequest struct {
	TenantID    string   `json:"tenant_id"`
	CompanyName string   `json:"company_name"`
	Description string   `json:"description"`
	UserID      string   `json:"user_id"`
	Login       string   `json:"login"`
	Roles       []string `json:"roles"`
}

// createTenantResponse — ответ команды создания тенанта.
type createTenantResponse struct {
	TenantID string `json:"tenant_id"`
	Created  bool   `json:"created"`
}

// roleRequest — тело команд назначения и отзыва роли.
type roleRequest struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
}

// accessResponse — ответ решения доступа.
type accessResponse struct {
	TenantID string `json:"tenant_id"`
	Actor    string `json:"actor"`
	Allowed  bool   `json:"allowed"`
}

// auditResponse — ответ журнала аудита.
type auditResponse struct {
	TenantID string       `json:"tenant_id"`
	Entries  []auditEntry `json:"entries"`
}

// auditEntry — строка журнала аудита в wire-формате.
type auditEntry struct {
	ID           string `json:"id"`
	Actor        string `json:"actor"`
	Action       string `json:"action"`
	Object       string `json:"object"`
	At           string `json:"at"`
	Outcome      string `json:"outcome"`
	CorrectionOf string `json:"correction_of,omitempty"`
}

// handleCreateTenant обслуживает POST /api/tenants: создание тенанта
// уполномоченным актором.
func (s *Server) handleCreateTenant(w http.ResponseWriter, r *http.Request) {
	var body createTenantRequest
	if !s.decodeJSON(w, r, &body) {
		return
	}
	result, err := s.deps.CreateTenant.Handle(r.Context(), command.CreateTenant{
		Actor:       r.Header.Get(headerActor),
		CommandID:   r.Header.Get(headerCommandID),
		TenantID:    body.TenantID,
		CompanyName: body.CompanyName,
		Description: body.Description,
		UserID:      body.UserID,
		Login:       body.Login,
		Roles:       body.Roles,
	})
	if err != nil {
		s.writeError(w, err)
		return
	}
	status := http.StatusOK
	if result.Created {
		status = http.StatusCreated
	}
	s.writeJSON(w, status, createTenantResponse{TenantID: result.TenantID, Created: result.Created})
}

// handleDisableTenant обслуживает POST /api/tenants/{tenantID}/disable.
func (s *Server) handleDisableTenant(w http.ResponseWriter, r *http.Request) {
	err := s.deps.DisableTenant.Handle(r.Context(), command.DisableTenant{
		Actor:     r.Header.Get(headerActor),
		CommandID: r.Header.Get(headerCommandID),
		TenantID:  r.PathValue("tenantID"),
	})
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "disabled"})
}

// handleAssignRole обслуживает POST /api/tenants/{tenantID}/roles/assign.
func (s *Server) handleAssignRole(w http.ResponseWriter, r *http.Request) {
	var body roleRequest
	if !s.decodeJSON(w, r, &body) {
		return
	}
	err := s.deps.AssignRole.Handle(r.Context(), command.AssignRole{
		Actor:     r.Header.Get(headerActor),
		CommandID: r.Header.Get(headerCommandID),
		TenantID:  r.PathValue("tenantID"),
		UserID:    body.UserID,
		Role:      body.Role,
	})
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "assigned"})
}

// handleRevokeRole обслуживает POST /api/tenants/{tenantID}/roles/revoke.
func (s *Server) handleRevokeRole(w http.ResponseWriter, r *http.Request) {
	var body roleRequest
	if !s.decodeJSON(w, r, &body) {
		return
	}
	err := s.deps.RevokeRole.Handle(r.Context(), command.RevokeRole{
		Actor:     r.Header.Get(headerActor),
		CommandID: r.Header.Get(headerCommandID),
		TenantID:  r.PathValue("tenantID"),
		UserID:    body.UserID,
		Role:      body.Role,
	})
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

// handleAccessDecision обслуживает GET /api/tenants/{tenantID}/access?actor=...
func (s *Server) handleAccessDecision(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("tenantID")
	actor := strings.TrimSpace(r.URL.Query().Get("actor"))
	if actor == "" {
		actor = strings.TrimSpace(r.Header.Get(headerActor))
	}
	allowed, err := s.deps.DecideAccess.Handle(r.Context(), query.DecideAccess{Actor: actor, TenantID: tenantID})
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, accessResponse{TenantID: tenantID, Actor: actor, Allowed: allowed})
}

// handleAuditJournal обслуживает GET /api/tenants/{tenantID}/audit.
func (s *Server) handleAuditJournal(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("tenantID")
	entries, err := s.deps.AuditJournal.Handle(r.Context(), query.AuditJournal{TenantID: tenantID})
	if err != nil {
		s.writeError(w, err)
		return
	}
	response := auditResponse{TenantID: tenantID, Entries: make([]auditEntry, 0, len(entries))}
	for _, entry := range entries {
		response.Entries = append(response.Entries, auditEntry{
			ID:           entry.ID,
			Actor:        entry.Actor,
			Action:       entry.Action,
			Object:       entry.Object,
			At:           entry.At.UTC().Format("2006-01-02T15:04:05Z07:00"),
			Outcome:      entry.Outcome,
			CorrectionOf: entry.CorrectionOf,
		})
	}
	s.writeJSON(w, http.StatusOK, response)
}

// decodeJSON читает тело запроса как JSON. Неразборчивое тело — 400, а не
// молчаливое продолжение с пустой командой.
func (s *Server) decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	if err := json.NewDecoder(r.Body).Decode(target); err != nil {
		s.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "некорректное тело запроса"})
		return false
	}
	return true
}

// writeJSON отдаёт JSON-ответ. Ответы консоли несут данные тенанта и оператора,
// поэтому не кэшируются (web-frontend-architecture, срез 7, 7.5).
func (s *Server) writeJSON(w http.ResponseWriter, status int, payload any) {
	components.SetNoStore(w)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// writeError переводит исход сценария в HTTP-код. Запрещённая команда — 403,
// некорректная — 400, отсутствие объекта — 404, конфликт состояния — 409,
// прочее — 500. Текст причины клиенту не раскрывается.
func (s *Server) writeError(w http.ResponseWriter, err error) {
	s.writeJSON(w, adminStatus(err), map[string]string{"error": adminMessage(err)})
}

// adminStatus классифицирует ошибку сценария управления доступом.
func adminStatus(err error) int {
	switch {
	case errors.Is(err, app.ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, app.ErrMissingActor), errors.Is(err, app.ErrMissingCommandID):
		return http.StatusBadRequest
	case errors.Is(err, domain.ErrEmptyID), errors.Is(err, domain.ErrUnknownRole),
		errors.Is(err, domain.ErrRolesRequired), errors.Is(err, domain.ErrCompanyNameRequired),
		errors.Is(err, domain.ErrDescriptionRequired), errors.Is(err, domain.ErrUserLoginRequired),
		errors.Is(err, domain.ErrUnknownAuditAction),
		errors.Is(err, domain.ErrRegistrationReasonRequired),
		errors.Is(err, domain.ErrProfileIncomplete):
		return http.StatusBadRequest
	case errors.Is(err, domain.ErrTenantDisabled), errors.Is(err, domain.ErrRoleNotAssigned),
		errors.Is(err, domain.ErrDuplicateUser),
		errors.Is(err, domain.ErrRegistrationState),
		errors.Is(err, domain.ErrProfileState):
		return http.StatusConflict
	case errors.Is(err, ports.ErrTenantNotFound), errors.Is(err, domain.ErrUserNotFound):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

// adminMessage отдаёт краткое сообщение об ошибке, не раскрывая внутренних
// деталей для неклассифицированных сбоев.
func adminMessage(err error) string {
	switch {
	case errors.Is(err, app.ErrForbidden):
		return "действие запрещено"
	case errors.Is(err, app.ErrMissingActor):
		return "не задан субъект действия"
	case errors.Is(err, app.ErrMissingCommandID):
		return "не задан идентификатор команды"
	case errors.Is(err, ports.ErrTenantNotFound):
		return "тенант не найден"
	case errors.Is(err, domain.ErrUserNotFound):
		return "пользователь не найден в тенанте"
	case errors.Is(err, domain.ErrTenantDisabled):
		return "тенант отключён"
	case errors.Is(err, domain.ErrRoleNotAssigned):
		return "роль не назначена пользователю"
	case errors.Is(err, domain.ErrRegistrationReasonRequired):
		return "Укажите причину отклонения"
	case errors.Is(err, domain.ErrRegistrationState):
		return "Заявка уже не на рассмотрении"
	case errors.Is(err, domain.ErrProfileState):
		return "Действие недопустимо в текущем состоянии профиля"
	case errors.Is(err, domain.ErrProfileIncomplete):
		return "Профиль заполнен не полностью"
	default:
		return "команда отклонена"
	}
}
