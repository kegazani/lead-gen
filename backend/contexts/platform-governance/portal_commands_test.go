package platformgovernance_test

import (
	"context"
	"errors"
	"testing"

	platformgovernance "lead-gen/backend/contexts/platform-governance"
	"lead-gen/backend/contexts/platform-governance/internal/app"
	"lead-gen/backend/contexts/platform-governance/internal/app/command"
	"lead-gen/backend/contexts/platform-governance/internal/domain"
)

// registerClient подаёт заявку на регистрацию и возвращает tenantID и userID.
func registerClient(t *testing.T, slice *platformgovernance.Slice, email, password, inn, companyEmail string) (string, string) {
	t.Helper()
	result, err := slice.RegisterClient.Handle(context.Background(), command.RegisterClient{
		CompanyName:  "ООО Ромашка",
		Description:  "Поставщик промышленного оборудования",
		Email:        email,
		Password:     password,
		INN:          inn,
		CompanyEmail: companyEmail,
		Website:      "https://romashka.ru",
		LegalData:    "ООО «Ромашка»",
	})
	if err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	return result.TenantID, result.UserID
}

// approve регистрирует тенант как активный.
func approve(t *testing.T, slice *platformgovernance.Slice, tenantID string) {
	t.Helper()
	if err := slice.ApproveRegistration.Handle(context.Background(), command.ApproveRegistration{
		TenantID: tenantID, Actor: "admin-1", CommandID: "cmd-approve",
	}); err != nil {
		t.Fatalf("ApproveRegistration: %v", err)
	}
}

// TestAuthenticateClientSuccess проверяет вход по корректным учётным данным.
func TestAuthenticateClientSuccess(t *testing.T) {
	slice := newSlice(t)
	tenantID, userID := registerClient(t, slice, "client@romashka.ru", "secret", "7701234567", "hello@romashka.ru")
	approve(t, slice, tenantID)

	result, err := slice.AuthenticateClient.Handle(context.Background(), command.AuthenticateClient{Email: "client@romashka.ru", Password: "secret"})
	if err != nil {
		t.Fatalf("AuthenticateClient: %v", err)
	}
	if result.TenantID != tenantID || result.UserID != userID {
		t.Fatalf("результат = %+v", result)
	}
}

// TestAuthenticateClientWrongPassword проверяет отказ при неверном пароле.
func TestAuthenticateClientWrongPassword(t *testing.T) {
	slice := newSlice(t)
	tenantID, _ := registerClient(t, slice, "client@romashka.ru", "secret", "7701234567", "hello@romashka.ru")
	approve(t, slice, tenantID)

	if _, err := slice.AuthenticateClient.Handle(context.Background(), command.AuthenticateClient{Email: "client@romashka.ru", Password: "wrong"}); !errors.Is(err, app.ErrInvalidCredentials) {
		t.Fatalf("ошибка = %v, ожидалась ErrInvalidCredentials", err)
	}
}

// TestAuthenticateClientInactiveTenant проверяет, что проверяемый тенант не
// пускает в портал.
func TestAuthenticateClientInactiveTenant(t *testing.T) {
	slice := newSlice(t)
	_, _ = registerClient(t, slice, "client@romashka.ru", "secret", "7701234567", "hello@romashka.ru")

	if _, err := slice.AuthenticateClient.Handle(context.Background(), command.AuthenticateClient{Email: "client@romashka.ru", Password: "secret"}); !errors.Is(err, app.ErrInvalidCredentials) {
		t.Fatalf("ошибка = %v, ожидалась ErrInvalidCredentials для pending-тенанта", err)
	}
}

// TestApproveRejectRegistration проверяет переходы заявки на регистрацию.
func TestApproveRejectRegistration(t *testing.T) {
	slice := newSlice(t)
	tenantID, _ := registerClient(t, slice, "client@romashka.ru", "secret", "7701234567", "hello@romashka.ru")

	if err := slice.ApproveRegistration.Handle(context.Background(), command.ApproveRegistration{TenantID: tenantID, Actor: "admin-1", CommandID: "cmd-a"}); err != nil {
		t.Fatalf("ApproveRegistration: %v", err)
	}
	tenant, err := slice.Repository.FindByID(context.Background(), domain.TenantID(tenantID))
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if tenant.State() != domain.TenantActive {
		t.Fatalf("состояние = %s, ожидался active", tenant.State())
	}

	// Отклонение работает только из pending: новая заявка.
	tenantID2, _ := registerClient(t, slice, "other@romashka.ru", "secret", "7707654321", "other@romashka.ru")
	if err := slice.RejectRegistration.Handle(context.Background(), command.RejectRegistration{TenantID: tenantID2, Actor: "admin-1", CommandID: "cmd-r", Description: "Некорректные данные"}); err != nil {
		t.Fatalf("RejectRegistration: %v", err)
	}
	tenant2, err := slice.Repository.FindByID(context.Background(), domain.TenantID(tenantID2))
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if tenant2.State() != domain.TenantRejected || tenant2.RegistrationReason() != "Некорректные данные" {
		t.Fatalf("состояние = %s, причина = %q", tenant2.State(), tenant2.RegistrationReason())
	}
}

// TestProfileCommands проверяет полный цикл профиля: сохранение, подача,
// согласование.
func TestProfileCommands(t *testing.T) {
	slice := newSlice(t)
	tenantID, _ := registerClient(t, slice, "client@romashka.ru", "secret", "7701234567", "hello@romashka.ru")
	approve(t, slice, tenantID)

	services := []domain.Service{{Name: "Аутсорсинг найма", Short: "Подбор", Full: "Полный цикл подбора"}}
	if err := slice.SaveProfile.Handle(context.Background(), command.SaveProfile{
		TenantID: tenantID, Description: "Рекрутинговое агентство", Target: "IT-компании", Services: services,
	}); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}

	if err := slice.SubmitProfile.Handle(context.Background(), command.SubmitProfile{TenantID: tenantID}); err != nil {
		t.Fatalf("SubmitProfile: %v", err)
	}
	tenant, err := slice.Repository.FindByID(context.Background(), domain.TenantID(tenantID))
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if tenant.ProfileStatus() != domain.ProfilePending {
		t.Fatalf("статус профиля = %s, ожидался pending", tenant.ProfileStatus())
	}

	// Правка в pending запрещена.
	if err := slice.SaveProfile.Handle(context.Background(), command.SaveProfile{TenantID: tenantID, Description: "x", Target: "y", Services: services}); !errors.Is(err, domain.ErrProfilePending) {
		t.Fatalf("ошибка = %v, ожидалась ErrProfilePending", err)
	}

	if err := slice.ApproveProfile.Handle(context.Background(), command.ApproveProfile{TenantID: tenantID, Actor: "admin-1", CommandID: "cmd-p"}); err != nil {
		t.Fatalf("ApproveProfile: %v", err)
	}
	tenant, err = slice.Repository.FindByID(context.Background(), domain.TenantID(tenantID))
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if tenant.ProfileStatus() != domain.ProfileActive {
		t.Fatalf("статус профиля = %s, ожидался active", tenant.ProfileStatus())
	}
}

// TestResetClientPassword проверяет, что администратор задаёт новый пароль и
// клиент входит с ним.
func TestResetClientPassword(t *testing.T) {
	slice := newSlice(t)
	tenantID, userID := registerClient(t, slice, "client@romashka.ru", "old-secret", "7701234567", "hello@romashka.ru")
	approve(t, slice, tenantID)

	if err := slice.ResetClientPassword.Handle(context.Background(), command.ResetClientPassword{
		Actor: "admin-1", TenantID: tenantID, UserID: userID, NewPassword: "new-secret",
	}); err != nil {
		t.Fatalf("ResetClientPassword: %v", err)
	}

	if _, err := slice.AuthenticateClient.Handle(context.Background(), command.AuthenticateClient{Email: "client@romashka.ru", Password: "new-secret"}); err != nil {
		t.Fatalf("вход с новым паролем: %v", err)
	}
	if _, err := slice.AuthenticateClient.Handle(context.Background(), command.AuthenticateClient{Email: "client@romashka.ru", Password: "old-secret"}); !errors.Is(err, app.ErrInvalidCredentials) {
		t.Fatalf("вход со старым паролем: ошибка = %v, ожидалась ErrInvalidCredentials", err)
	}
}
