package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lead-gen/backend/contexts/outreach-orchestration/internal/app/command"
	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/contracts/outreach-orchestration/outreachhandoff"
	"lead-gen/contracts/outreach-orchestration/outreachhandoff/httpport"
)

// stubHandoff — управляемый сценарий приёма: он позволяет проверить провод без
// доменного состояния (например, недоступность хранилища).
type stubHandoff struct {
	result command.SubmitHandoffResult
	err    error
}

func (s stubHandoff) Handle(context.Context, command.SubmitHandoff) (command.SubmitHandoffResult, error) {
	return s.result, s.err
}

// handoffPayload — проводная форма команды передачи: ключи совпадают с контрактом.
func handoffPayload() map[string]any {
	return map[string]any{
		"request_id": "req-1",
		"tenant_id":  testTenant.String(),
		"actor":      testOperator.String(),
		"name":       "Передача заявки req-1",
		"items": []map[string]any{
			{"lead_id": "lead-a", "recipient_ref": "recipient-a", "material_ref": "material-a"},
			{"lead_id": "lead-b", "recipient_ref": "recipient-b", "material_ref": "material-b"},
		},
	}
}

// TestHandoffRoundTripThroughContractClient прогоняет сетевой клиент контракта
// outreach-handoff через реальный HTTP-адаптер: успех, повтор и состояние
// черновиков в очереди утверждения. Отправка не выполняется.
func TestHandoffRoundTripThroughContractClient(t *testing.T) {
	fixture := newFixture(t)
	client, err := httpport.New(fixture.server.URL)
	if err != nil {
		t.Fatalf("httpport.New: %v", err)
	}

	submit := func() outreachhandoff.Result {
		t.Helper()
		result, err := client.Submit(context.Background(), outreachhandoff.Command{
			RequestID: "req-1",
			TenantID:  testTenant.String(),
			Actor:     testOperator.String(),
			Items: []outreachhandoff.Item{
				{LeadID: "lead-a", RecipientRef: "recipient-a", MaterialRef: "material-a"},
				{LeadID: "lead-b", RecipientRef: "recipient-b", MaterialRef: "material-b"},
			},
		})
		if err != nil {
			t.Fatalf("Submit: %v", err)
		}
		return result
	}

	first := submit()
	if first.CampaignID != "handoff-req-1" || first.Accepted != 2 || first.Skipped != 0 || first.Repeated {
		t.Fatalf("первый исход = %+v", first)
	}

	campaign, err := fixture.repo.FindByID(context.Background(), domain.CampaignID("handoff-req-1"))
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if len(campaign.PendingApproval()) != 2 {
		t.Fatalf("черновиков в очереди утверждения = %d, ожидалось 2", len(campaign.PendingApproval()))
	}
	if got := countApproved(t, fixture); got != 0 {
		t.Fatalf("при приёме отправка не выполняется: action-approved %d", got)
	}

	repeat := submit()
	if !repeat.Repeated || repeat.Accepted != 0 || repeat.Skipped != 2 {
		t.Fatalf("повторный исход = %+v, ожидалось repeated/0/2", repeat)
	}
	if reloaded, err := fixture.repo.FindByID(context.Background(), domain.CampaignID("handoff-req-1")); err != nil {
		t.Fatalf("FindByID после повтора: %v", err)
	} else if len(reloaded.Drafts()) != 2 {
		t.Fatalf("повтор задублировал черновики: %d", len(reloaded.Drafts()))
	}
}

// TestHandoffServedWithoutSession проверяет, что контрактный эндпоинт не требует
// сессии консоли и CSRF: при настроенной консоли запрос без cookie обслуживается
// как контракт, а не уводится на вход.
func TestHandoffServedWithoutSession(t *testing.T) {
	fixture := newFixture(t)

	response, body := doJSON(t, http.MethodPost, fixture.server.URL+"/outreach-handoff", handoffPayload())
	if response.StatusCode != http.StatusOK {
		t.Fatalf("статус %d, тело %s", response.StatusCode, body)
	}

	// Ответ несёт только наблюдаемый исход: персональных данных переданных писем
	// (адресов получателей) в нём нет.
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("разбор ответа %q: %v", body, err)
	}
	for key := range payload {
		switch key {
		case "campaign_id", "accepted", "skipped", "repeated":
		default:
			t.Fatalf("неожиданное поле ответа %q: %s", key, body)
		}
	}
	if payload["campaign_id"] != "handoff-req-1" {
		t.Fatalf("campaign_id = %v", payload["campaign_id"])
	}
	for _, pii := range []string{"recipient-a", "recipient-b"} {
		if strings.Contains(string(body), pii) {
			t.Fatalf("ответ содержит персональные данные: %s", body)
		}
	}
}

// TestHandoffRejectedIsRejectedOutcome проверяет различение доменного отказа и
// недоступности: некорректная команда — 400 (ErrRejected у клиента).
func TestHandoffRejectedIsRejectedOutcome(t *testing.T) {
	fixture := newFixture(t)
	client, err := httpport.New(fixture.server.URL)
	if err != nil {
		t.Fatalf("httpport.New: %v", err)
	}

	_, err = client.Submit(context.Background(), outreachhandoff.Command{
		RequestID: "req-1",
		TenantID:  "", // пустой тенант — доменный отказ
		Actor:     testOperator.String(),
		Items: []outreachhandoff.Item{
			{LeadID: "lead-a", RecipientRef: "recipient-a", MaterialRef: "material-a"},
		},
	})
	if !errors.Is(err, outreachhandoff.ErrRejected) {
		t.Fatalf("ошибка = %v, ожидалась ErrRejected", err)
	}
}

// TestHandoffUnknownFieldRejected проверяет, что чужой формат тела не принимается
// молча: неизвестное поле — 400.
func TestHandoffUnknownFieldRejected(t *testing.T) {
	fixture := newFixture(t)

	body := handoffPayload()
	body["unexpected"] = "x"
	response, data := doJSON(t, http.MethodPost, fixture.server.URL+"/outreach-handoff", body)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("статус %d, тело %s", response.StatusCode, data)
	}
}

// TestHandoffUnavailableWhenNotWired проверяет честный отказ: неподключённый
// сценарий отвечает 503, а не 200 с пустым результатом.
func TestHandoffUnavailableWhenNotWired(t *testing.T) {
	server := NewServer(Dependencies{})
	httpServer := newTestHTTPServer(t, server)

	client, err := httpport.New(httpServer.URL)
	if err != nil {
		t.Fatalf("httpport.New: %v", err)
	}
	_, err = client.Submit(context.Background(), outreachhandoff.Command{
		RequestID: "req-1",
		TenantID:  "tenant-1",
		Items:     []outreachhandoff.Item{{LeadID: "lead-a", RecipientRef: "recipient-a", MaterialRef: "material-a"}},
	})
	if !errors.Is(err, outreachhandoff.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}

func TestRequestCancellationContractFencesLateHandoff(t *testing.T) {
	fixture := newFixture(t)
	client, err := httpport.New(fixture.server.URL)
	if err != nil {
		t.Fatalf("httpport.New: %v", err)
	}
	if err := client.CancelRequest(context.Background(), outreachhandoff.RequestCancellation{
		TenantID: testTenant.String(), RequestID: "req-cancelled-before-handoff",
	}); err != nil {
		t.Fatalf("CancelRequest: %v", err)
	}
	_, err = client.Submit(context.Background(), outreachhandoff.Command{
		RequestID: "req-cancelled-before-handoff", TenantID: testTenant.String(),
		Items: []outreachhandoff.Item{{LeadID: "lead-a", RecipientRef: "recipient-a", MaterialRef: "material-a"}},
	})
	if !errors.Is(err, outreachhandoff.ErrRejected) {
		t.Fatalf("late handoff error = %v, expected ErrRejected", err)
	}
}

// TestHandoffStorageFailureIsUnavailable проверяет, что сбой хранилища даёт
// недоступность (503), а не доменный отказ.
func TestHandoffStorageFailureIsUnavailable(t *testing.T) {
	server := NewServer(Dependencies{
		Handoff: stubHandoff{err: errors.New("хранилище недоступно")},
	})
	httpServer := newTestHTTPServer(t, server)

	client, err := httpport.New(httpServer.URL)
	if err != nil {
		t.Fatalf("httpport.New: %v", err)
	}
	_, err = client.Submit(context.Background(), outreachhandoff.Command{
		RequestID: "req-1",
		TenantID:  "tenant-1",
		Items:     []outreachhandoff.Item{{LeadID: "lead-a", RecipientRef: "recipient-a", MaterialRef: "material-a"}},
	})
	if !errors.Is(err, outreachhandoff.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}

// TestHandoffEmptyCompositionAcceptedZero проверяет, что пустой состав честно
// сообщается исходом accepted=0 и кампанию не создаёт.
func TestHandoffEmptyCompositionAcceptedZero(t *testing.T) {
	fixture := newFixture(t)

	body := handoffPayload()
	body["items"] = []map[string]any{}
	response, data := doJSON(t, http.MethodPost, fixture.server.URL+"/outreach-handoff", body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("статус %d, тело %s", response.StatusCode, data)
	}
	var payload handoffResponse
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("разбор ответа %q: %v", data, err)
	}
	if payload.CampaignID != "" || payload.Accepted != 0 || payload.Skipped != 0 || payload.Repeated {
		t.Fatalf("исход пустого состава = %+v", payload)
	}
}

// newTestHTTPServer поднимает httptest-сервер над обработчиком и закрывает его
// по завершении теста.
func newTestHTTPServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}
