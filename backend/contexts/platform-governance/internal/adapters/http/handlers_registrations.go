package httpapi

import (
	"net/http"
	"strings"
	"time"

	"lead-gen/backend/contexts/platform-governance/internal/app"
	"lead-gen/backend/contexts/platform-governance/internal/app/command"
	"lead-gen/backend/contexts/platform-governance/internal/app/query"
	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/frontend/components"
)

// Раздел «Регистрации» консоли платформенного оператора: заявки на регистрацию
// компаний и профили на согласовании. Оператор решает по заявке (подтвердить или
// отклонить с причиной) и согласует профиль. Доступ проверяется признаком
// платформенного оператора, а сами команды повторно проверяют право актора.

// registrationsGuard проверяет доступ к разделу «Регистрации» на сервере. Раздел
// не адресован активному тенанту (у заявки его нет), поэтому право решает
// принадлежность к платформенным операторам, а требуемое право берётся из матрицы
// раздела. Отсутствие владельца доступа — честная недоступность, пустой субъект —
// отказ аутентификации.
func (s *Server) registrationsGuard(w http.ResponseWriter, r *http.Request, actor string, action app.Action) bool {
	if s.deps.DecideAccess == nil {
		s.renderConsoleDenied(w, http.StatusServiceUnavailable, "Проверка доступа недоступна")
		return false
	}
	if strings.TrimSpace(actor) == "" {
		s.renderConsoleDenied(w, http.StatusUnauthorized, "Не задан субъект действия")
		return false
	}
	if _, ok := app.RequiredRight(app.SectionRegistrations, action); !ok {
		s.renderConsoleDenied(w, http.StatusInternalServerError, "Раздел не объявлен")
		return false
	}
	if !s.platformOperator(actor) {
		s.renderConsoleDenied(w, http.StatusForbidden, "Раздел доступен только платформенному оператору")
		return false
	}
	return true
}

// consoleRegistrationRow — строка заявки на регистрацию: данные компании,
// поданные клиентом, и дата подачи.
type consoleRegistrationRow struct {
	TenantID         string
	ApproveCommandID string
	RejectCommandID  string
	CompanyName      string
	Description      string
	INN              string
	CompanyEmail     string
	Website          string
	LegalData        string
	CreatedAt        string
}

// consoleProfileRow — профиль компании на согласовании: содержимое, по которому
// оператор принимает решение. Unavailable — содержимое не прочитано: решение
// недоступно, чтобы не согласовывать невидимый профиль.
type consoleProfileRow struct {
	TenantID         string
	ApproveCommandID string
	RejectCommandID  string
	CompanyName      string
	Description      string
	Target           string
	Services         []query.ServiceView
	Unavailable      bool
}

// consoleRegistrationsView — модель раздела «Регистрации».
type consoleRegistrationsView struct {
	Actor         string
	CSRF          string
	Notice        string
	Registrations []consoleRegistrationRow
	Profiles      []consoleProfileRow
}

// handleConsoleRegistrations обслуживает GET /console/registrations: список
// заявок на регистрацию и профилей на согласовании.
func (s *Server) handleConsoleRegistrations(w http.ResponseWriter, r *http.Request) {
	actor := consoleActor(r)
	if !s.registrationsGuard(w, r, actor, app.ActionView) {
		return
	}
	// Тенант может быть выбран ранее: переносим его в навигацию, чтобы переход
	// в тенантные разделы не требовал повторного выбора.
	tenant := s.activeTenant(r)
	views, err := s.deps.ListTenants.Handle(r.Context(), query.ListTenants{})
	if err != nil {
		s.render(w, adminStatus(err), pageConsoleRegistrations, components.Page{
			Layout:  components.Layout{Title: "Регистрации"},
			Sidebar: s.consoleNav("/console/registrations", tenant, actor),
			View:    consoleRegistrationsView{Actor: actor, CSRF: sessionCSRF(r), Notice: adminMessage(err)},
		})
		return
	}
	view := s.registrationsView(r, actor, views)
	s.render(w, http.StatusOK, pageConsoleRegistrations, components.Page{
		Layout:  components.Layout{Title: "Регистрации"},
		Sidebar: s.consoleNav("/console/registrations", tenant, actor),
		View:    view,
	})
}

// handleConsoleRegistrationApprove обслуживает POST .../approve.
func (s *Server) handleConsoleRegistrationApprove(w http.ResponseWriter, r *http.Request) {
	s.consoleRegistrationDecision(w, r, true)
}

// handleConsoleRegistrationReject обслуживает POST .../reject.
func (s *Server) handleConsoleRegistrationReject(w http.ResponseWriter, r *http.Request) {
	s.consoleRegistrationDecision(w, r, false)
}

// consoleRegistrationDecision — общий путь решения по заявке на регистрацию:
// серверная проверка доступа, вызов команды owning context и POST/redirect/GET.
// Отклонение требует непустой причины; ошибка возвращает экран с сообщением.
func (s *Server) consoleRegistrationDecision(w http.ResponseWriter, r *http.Request, approve bool) {
	if err := r.ParseForm(); err != nil {
		s.renderPlainError(w, http.StatusBadRequest, "разбор формы: "+err.Error())
		return
	}
	actor := consoleActor(r)
	if !s.registrationsGuard(w, r, actor, app.ActionManage) {
		return
	}
	tenantID := r.PathValue("tenantID")
	commandID := r.PostFormValue("command_id")
	var err error
	if approve {
		err = s.deps.ApproveRegistration.Handle(r.Context(), command.ApproveRegistration{
			TenantID:  tenantID,
			Actor:     actor,
			CommandID: commandID,
		})
	} else {
		err = s.deps.RejectRegistration.Handle(r.Context(), command.RejectRegistration{
			TenantID:    tenantID,
			Actor:       actor,
			CommandID:   commandID,
			Description: strings.TrimSpace(r.PostFormValue("reason")),
		})
	}
	if err != nil {
		s.renderRegistrationsNotice(w, r, actor, adminMessage(err), adminStatus(err))
		return
	}
	notice := "registration_rejected"
	if approve {
		notice = "registration_approved"
	}
	http.Redirect(w, r, consoleURL("/console/registrations", "", actor, notice), http.StatusSeeOther)
}

// handleConsoleProfileApprove обслуживает POST .../profile/approve.
func (s *Server) handleConsoleProfileApprove(w http.ResponseWriter, r *http.Request) {
	s.consoleProfileDecision(w, r, true)
}

// handleConsoleProfileReject обслуживает POST .../profile/reject.
func (s *Server) handleConsoleProfileReject(w http.ResponseWriter, r *http.Request) {
	s.consoleProfileDecision(w, r, false)
}

// consoleProfileDecision — общий путь согласования профиля: серверная проверка
// доступа, вызов команды и POST/redirect/GET.
func (s *Server) consoleProfileDecision(w http.ResponseWriter, r *http.Request, approve bool) {
	if err := r.ParseForm(); err != nil {
		s.renderPlainError(w, http.StatusBadRequest, "разбор формы: "+err.Error())
		return
	}
	actor := consoleActor(r)
	if !s.registrationsGuard(w, r, actor, app.ActionManage) {
		return
	}
	tenantID := r.PathValue("tenantID")
	commandID := r.PostFormValue("command_id")
	var err error
	if approve {
		err = s.deps.ApproveProfile.Handle(r.Context(), command.ApproveProfile{
			TenantID:  tenantID,
			Actor:     actor,
			CommandID: commandID,
		})
	} else {
		err = s.deps.RejectProfile.Handle(r.Context(), command.RejectProfile{
			TenantID:  tenantID,
			Actor:     actor,
			CommandID: commandID,
		})
	}
	if err != nil {
		s.renderRegistrationsNotice(w, r, actor, adminMessage(err), adminStatus(err))
		return
	}
	notice := "profile_rejected"
	if approve {
		notice = "profile_approved"
	}
	http.Redirect(w, r, consoleURL("/console/registrations", "", actor, notice), http.StatusSeeOther)
}

// renderRegistrationsNotice перерисовывает раздел с сообщением (например, об
// ошибке команды), заново собирая списки.
func (s *Server) renderRegistrationsNotice(w http.ResponseWriter, r *http.Request, actor, notice string, status int) {
	views, err := s.deps.ListTenants.Handle(r.Context(), query.ListTenants{})
	if err != nil {
		views = nil
	}
	view := s.registrationsView(r, actor, views)
	view.Notice = notice
	s.render(w, status, pageConsoleRegistrations, components.Page{
		Layout:  components.Layout{Title: "Регистрации"},
		Sidebar: s.consoleNav("/console/registrations", s.activeTenant(r), actor),
		View:    view,
	})
}

// registrationsView собирает модель раздела: заявки в состоянии «на проверке» и
// профили активных компаний на согласовании. Содержимое профиля читается через
// use case владельца.
func (s *Server) registrationsView(r *http.Request, actor string, tenants []query.TenantView) consoleRegistrationsView {
	view := consoleRegistrationsView{
		Actor:  actor,
		CSRF:   sessionCSRF(r),
		Notice: consoleNotice(r.URL.Query().Get("notice")),
	}
	for _, tenant := range tenants {
		switch {
		case tenant.State == string(domain.TenantPending):
			view.Registrations = append(view.Registrations, consoleRegistrationRow{
				TenantID:         tenant.ID,
				ApproveCommandID: s.commandID(),
				RejectCommandID:  s.commandID(),
				CompanyName:      tenant.CompanyName,
				Description:      tenant.Description,
				INN:              tenant.INN,
				CompanyEmail:     tenant.CompanyEmail,
				Website:          tenant.Website,
				LegalData:        tenant.LegalData,
				CreatedAt:        formatRegistrationDate(tenant.CreatedAt),
			})
		case tenant.State == string(domain.TenantActive) && tenant.ProfileStatus == string(domain.ProfilePending):
			row := consoleProfileRow{
				TenantID:         tenant.ID,
				ApproveCommandID: s.commandID(),
				RejectCommandID:  s.commandID(),
				CompanyName:      tenant.CompanyName,
			}
			if profile, err := s.deps.TenantProfile.Handle(r.Context(), query.TenantProfile{TenantID: tenant.ID}); err == nil {
				row.Description = profile.Description
				row.Target = profile.Target
				row.Services = profile.Services
			} else {
				// Содержимое профиля не прочитано: решение по нему не предлагаем.
				row.Unavailable = true
			}
			view.Profiles = append(view.Profiles, row)
		}
	}
	return view
}

// formatRegistrationDate приводит время подачи к виду без зоны: экран показывает
// локальную для процесса отметку, а не машинный формат.
func formatRegistrationDate(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	return at.Format("2006-01-02 15:04")
}
