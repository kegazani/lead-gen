package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	platformgovernance "lead-gen/backend/contexts/platform-governance"
	"lead-gen/backend/contexts/platform-governance/internal/adapters/grants"
	"lead-gen/backend/contexts/platform-governance/internal/app/command"
	"lead-gen/backend/contexts/platform-governance/internal/domain"
)

// newConsoleOperatorServer собирает срез с правами администратора и платформенным
// оператором admin-1 — как точка входа контура. Возвращает срез и сервер.
func newConsoleOperatorServer(t *testing.T) (*platformgovernance.Slice, *httptest.Server) {
	t.Helper()
	adminGrants, err := grants.FromStrings("admin-1", []string{string(domain.RoleAdmin)})
	if err != nil {
		t.Fatalf("grants: %v", err)
	}
	slice, err := platformgovernance.Build(platformgovernance.Config{
		Grants:    adminGrants,
		Requests:  newFakeRequests(),
		Operators: []string{"admin-1"},
		Clock:     fixedClock{at: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)},
		IDs:       &seqIDs{},
	})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	server := httptest.NewServer(slice.Server)
	t.Cleanup(server.Close)
	return slice, server
}

// registerClientPending подаёт заявку на регистрацию, не подтверждая её.
func registerClientPending(t *testing.T, slice *platformgovernance.Slice, email, inn, companyEmail string) string {
	t.Helper()
	result, err := slice.RegisterClient.Handle(context.Background(), command.RegisterClient{
		CompanyName:  "ООО Ромашка",
		Description:  "Поставщик промышленного оборудования",
		Email:        email,
		Password:     "secret",
		INN:          inn,
		CompanyEmail: companyEmail,
		Website:      "https://romashka.ru",
		LegalData:    "ООО «Ромашка»",
	})
	if err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	return result.TenantID
}

// tenantState читает тенант из репозитория среза.
func tenantState(t *testing.T, slice *platformgovernance.Slice, tenantID string) *domain.Tenant {
	t.Helper()
	tenant, err := slice.Repository.FindByID(context.Background(), domain.TenantID(tenantID))
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	return tenant
}

// TestConsoleRegistrationsList проверяет, что заявка на регистрацию видна
// платформенному оператору с данными компании.
func TestConsoleRegistrationsList(t *testing.T) {
	slice, server := newConsoleOperatorServer(t)
	registerClientPending(t, slice, "client@romashka.ru", "7701234567", "hello@romashka.ru")

	page := body(t, get(t, server, "/console/registrations?actor=admin-1"))
	for _, want := range []string{"Заявки на регистрацию", "ООО Ромашка", "7701234567", "hello@romashka.ru"} {
		if !strings.Contains(page, want) {
			t.Fatalf("раздел не показал %q:\n%s", want, page)
		}
	}
}

// TestConsoleRegistrationApprove проверяет подтверждение заявки через HTTP.
func TestConsoleRegistrationApprove(t *testing.T) {
	slice, server := newConsoleOperatorServer(t)
	tenantID := registerClientPending(t, slice, "client@romashka.ru", "7701234567", "hello@romashka.ru")

	resp := postForm(t, server, "/console/registrations/"+tenantID+"/approve", url.Values{
		"actor": {"admin-1"}, "command_id": {"cmd-approve"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("одобрение = %d, ожидался 303: %s", resp.StatusCode, body(t, resp))
	}
	if state := tenantState(t, slice, tenantID).State(); state != domain.TenantActive {
		t.Fatalf("состояние = %s, ожидалось active", state)
	}
}

// TestConsoleRegistrationRejectRequiresReason проверяет обязательность причины.
func TestConsoleRegistrationRejectRequiresReason(t *testing.T) {
	slice, server := newConsoleOperatorServer(t)
	tenantID := registerClientPending(t, slice, "client@romashka.ru", "7701234567", "hello@romashka.ru")

	resp := postForm(t, server, "/console/registrations/"+tenantID+"/reject", url.Values{
		"actor": {"admin-1"}, "command_id": {"cmd-reject"}, "reason": {""},
	})
	page := body(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("отклонение без причины = %d, ожидался 400:\n%s", resp.StatusCode, page)
	}
	if !strings.Contains(page, "Укажите причину отклонения") {
		t.Fatalf("нет сообщения об обязательной причине:\n%s", page)
	}
}

// TestConsoleRegistrationReject проверяет отклонение с причиной.
func TestConsoleRegistrationReject(t *testing.T) {
	slice, server := newConsoleOperatorServer(t)
	tenantID := registerClientPending(t, slice, "client@romashka.ru", "7701234567", "hello@romashka.ru")

	resp := postForm(t, server, "/console/registrations/"+tenantID+"/reject", url.Values{
		"actor": {"admin-1"}, "command_id": {"cmd-reject"}, "reason": {"Некорректные данные"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("отклонение = %d, ожидался 303: %s", resp.StatusCode, body(t, resp))
	}
	tenant := tenantState(t, slice, tenantID)
	if tenant.State() != domain.TenantRejected || tenant.RegistrationReason() != "Некорректные данные" {
		t.Fatalf("состояние = %s, причина = %q", tenant.State(), tenant.RegistrationReason())
	}
}

// TestConsoleRegistrationsDeniedForNonOperator проверяет серверный отказ не-оператору.
func TestConsoleRegistrationsDeniedForNonOperator(t *testing.T) {
	_, server := newConsoleOperatorServer(t)
	resp := getCookie(t, server, "/console/registrations?actor=user-9", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("не-оператор = %d, ожидался 403", resp.StatusCode)
	}
}

// TestConsoleProfileReject проверяет отклонение профиля: статус становится
// rejected (профиль снова доступен клиенту для правки).
func TestConsoleProfileReject(t *testing.T) {
	slice, server := newConsoleOperatorServer(t)
	tenantID := registerClientPending(t, slice, "client@romashka.ru", "7701234567", "hello@romashka.ru")

	approve := postForm(t, server, "/console/registrations/"+tenantID+"/approve", url.Values{
		"actor": {"admin-1"}, "command_id": {"cmd-approve"},
	})
	approve.Body.Close()

	if err := slice.SaveProfile.Handle(context.Background(), command.SaveProfile{
		TenantID:    tenantID,
		Description: "Рекрутинговое агентство",
		Target:      "IT-компании",
		Services:    []domain.Service{{Name: "Аутсорсинг найма", Short: "Подбор", Full: "Полный цикл"}},
	}); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	if err := slice.SubmitProfile.Handle(context.Background(), command.SubmitProfile{TenantID: tenantID}); err != nil {
		t.Fatalf("SubmitProfile: %v", err)
	}

	resp := postForm(t, server, "/console/registrations/"+tenantID+"/profile/reject", url.Values{
		"actor": {"admin-1"}, "command_id": {"cmd-profile-reject"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("отклонение профиля = %d, ожидался 303: %s", resp.StatusCode, body(t, resp))
	}
	if status := tenantState(t, slice, tenantID).ProfileStatus(); status != domain.ProfileRejected {
		t.Fatalf("статус профиля = %s, ожидался rejected", status)
	}
}

// TestConsoleRegistrationsEmptyActor проверяет отказ аутентификации при пустом субъекте.
func TestConsoleRegistrationsEmptyActor(t *testing.T) {
	_, server := newConsoleOperatorServer(t)
	resp := getCookie(t, server, "/console/registrations", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("пустой субъект = %d, ожидался 401", resp.StatusCode)
	}
}

// TestConsoleRegistrationRejectBlankReason проверяет, что причина из пробелов не
// считается заполненной.
func TestConsoleRegistrationRejectBlankReason(t *testing.T) {
	slice, server := newConsoleOperatorServer(t)
	tenantID := registerClientPending(t, slice, "client@romashka.ru", "7701234567", "hello@romashka.ru")

	resp := postForm(t, server, "/console/registrations/"+tenantID+"/reject", url.Values{
		"actor": {"admin-1"}, "command_id": {"cmd-reject"}, "reason": {"   "},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("причина из пробелов = %d, ожидался 400", resp.StatusCode)
	}
}

// TestConsoleRegistrationNonOperatorPost проверяет отказ на изменяющем действии.
func TestConsoleRegistrationNonOperatorPost(t *testing.T) {
	slice, server := newConsoleOperatorServer(t)
	tenantID := registerClientPending(t, slice, "client@romashka.ru", "7701234567", "hello@romashka.ru")

	resp := postForm(t, server, "/console/registrations/"+tenantID+"/approve", url.Values{
		"actor": {"user-9"}, "command_id": {"cmd-x"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("не-оператор на POST = %d, ожидался 403", resp.StatusCode)
	}
	if state := tenantState(t, slice, tenantID).State(); state != domain.TenantPending {
		t.Fatalf("состояние изменилось не-оператором: %s", state)
	}
}

// TestConsoleRegistrationUnknownTenant проверяет решение по несуществующему тенанту.
func TestConsoleRegistrationUnknownTenant(t *testing.T) {
	_, server := newConsoleOperatorServer(t)
	resp := postForm(t, server, "/console/registrations/tenant-missing/approve", url.Values{
		"actor": {"admin-1"}, "command_id": {"cmd-missing"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("несуществующий тенант = %d, ожидался 404: %s", resp.StatusCode, body(t, resp))
	}
}

func TestConsoleProfileApprove(t *testing.T) {
	slice, server := newConsoleOperatorServer(t)
	tenantID := registerClientPending(t, slice, "client@romashka.ru", "7701234567", "hello@romashka.ru")

	approve := postForm(t, server, "/console/registrations/"+tenantID+"/approve", url.Values{
		"actor": {"admin-1"}, "command_id": {"cmd-approve"},
	})
	approve.Body.Close()

	if err := slice.SaveProfile.Handle(context.Background(), command.SaveProfile{
		TenantID:    tenantID,
		Description: "Рекрутинговое агентство",
		Target:      "IT-компании",
		Services:    []domain.Service{{Name: "Аутсорсинг найма", Short: "Подбор", Full: "Полный цикл"}},
	}); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	if err := slice.SubmitProfile.Handle(context.Background(), command.SubmitProfile{TenantID: tenantID}); err != nil {
		t.Fatalf("SubmitProfile: %v", err)
	}

	page := body(t, get(t, server, "/console/registrations?actor=admin-1"))
	if !strings.Contains(page, "Профили на согласовании") || !strings.Contains(page, "Аутсорсинг найма") {
		t.Fatalf("раздел не показал профиль на согласовании:\n%s", page)
	}

	resp := postForm(t, server, "/console/registrations/"+tenantID+"/profile/approve", url.Values{
		"actor": {"admin-1"}, "command_id": {"cmd-profile"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("подтверждение профиля = %d, ожидался 303: %s", resp.StatusCode, body(t, resp))
	}
	if status := tenantState(t, slice, tenantID).ProfileStatus(); status != domain.ProfileActive {
		t.Fatalf("статус профиля = %s, ожидался active", status)
	}
}
