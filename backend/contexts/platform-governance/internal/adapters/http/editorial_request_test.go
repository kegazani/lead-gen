package httpapi_test

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"lead-gen/backend/contexts/platform-governance"
	"lead-gen/backend/contexts/platform-governance/internal/app/command"
	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/contracts/request-fulfillment/requestintent"
)

type capturingIntent struct{ got requestintent.SubmitIntent }

func (c *capturingIntent) Submit(_ context.Context, in requestintent.SubmitIntent) (requestintent.SubmitResult, error) {
	c.got = in
	return requestintent.SubmitResult{RequestID: in.RequestID, Status: "submitted"}, nil
}
func (*capturingIntent) Status(context.Context, requestintent.StatusQuery) (requestintent.StatusResult, error) {
	return requestintent.StatusResult{}, nil
}
func (*capturingIntent) List(context.Context, requestintent.ListQuery) ([]requestintent.ListEntry, error) {
	return nil, nil
}

func approveDemoProfile(t *testing.T, slice *platformgovernance.Slice, tenantID string) {
	t.Helper()
	if err := slice.SaveProfile.Handle(context.Background(), command.SaveProfile{
		TenantID: tenantID, Description: "Услуги по автоматизации продаж", Target: "B2B компании",
		Services: []domain.Service{{Name: "Автоматизация", Short: "CRM", Full: "Внедрение CRM и аналитики"}},
	}); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	if err := slice.SubmitProfile.Handle(context.Background(), command.SubmitProfile{TenantID: tenantID}); err != nil {
		t.Fatalf("SubmitProfile: %v", err)
	}
	if err := slice.ApproveProfile.Handle(context.Background(), command.ApproveProfile{
		TenantID: tenantID, Actor: "admin-1", CommandID: "cmd-profile-" + tenantID,
	}); err != nil {
		t.Fatalf("ApproveProfile: %v", err)
	}
}

func TestPortalBuildsEditorialIntentFromActiveProfile(t *testing.T) {
	port := &capturingIntent{}
	slice, server := newAuthServer(t, port)
	tenantID, userID := registerAndApprove(t, slice, "client@romashka.ru", "secret", "7701234567", "hello@romashka.ru")
	approveDemoProfile(t, slice, tenantID)
	cookie := login(t, server, "client@romashka.ru", "secret")
	resp := postFormCookie(t, server, "/portal/request", url.Values{
		"content": {"Предлагаем CRM"}, "target": {"B2B компании"},
		"tenant_id": {"attacker-tenant"}, "execution_purpose": {"delivery"},
		"sender_company[company_name]": {"Чужая компания"},
	}, cookie)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("подача заявки = %d", resp.StatusCode)
	}
	got := port.got
	if got.TenantID != tenantID || got.Actor != userID || got.ExecutionPurpose != "editorial" {
		t.Fatalf("trusted intent fields подменены: %+v", got)
	}
	if got.Content != "Предлагаем CRM" || got.Target != "B2B компании" {
		t.Fatalf("offer content/target смешаны или потеряны: %+v", got)
	}
	if got.SenderCompany == nil || got.SenderCompany.CompanyName != "ООО Ромашка" || got.SenderCompany.Website != "https://romashka.ru" || got.SenderCompany.Description != "Услуги по автоматизации продаж" {
		t.Fatalf("sender snapshot не построен из активного профиля: %+v", got.SenderCompany)
	}
	if len(got.SenderCompany.Services) != 1 || got.SenderCompany.Services[0] != (requestintent.SenderServiceSnapshot{Name: "Автоматизация", Short: "CRM", Full: "Внедрение CRM и аналитики"}) {
		t.Fatalf("услуги snapshot = %+v", got.SenderCompany.Services)
	}
}

func TestPortalRejectsRequestBeforeProfileApproval(t *testing.T) {
	port := &capturingIntent{}
	slice, server := newAuthServer(t, port)
	tenantID, _ := registerAndApprove(t, slice, "client2@romashka.ru", "secret", "7701234568", "hello2@romashka.ru")
	if err := slice.SaveProfile.Handle(context.Background(), command.SaveProfile{
		TenantID: tenantID, Description: "Описание", Target: "Цель", Services: []domain.Service{{Name: "Услуга", Short: "Кратко", Full: "Полностью"}},
	}); err != nil {
		t.Fatal(err)
	}
	cookie := login(t, server, "client2@romashka.ru", "secret")
	resp := postFormCookie(t, server, "/portal/request", url.Values{"content": {"Оффер"}}, cookie)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("без одобренного профиля = %d, ожидался 403", resp.StatusCode)
	}
	if port.got.RequestID != "" {
		t.Fatal("заявка отправлена владельцу до одобрения профиля")
	}
}
