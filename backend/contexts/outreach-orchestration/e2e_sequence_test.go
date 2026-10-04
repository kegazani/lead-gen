package outreachorchestration_test

import (
	"context"
	"net/http"
	"testing"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
)

// defineSequenceBody — тело команды создания сценария из двух шагов без задержки.
func defineSequenceBody(transition string) map[string]any {
	return map[string]any{
		"tenant_id":   e2eTenant.String(),
		"actor_id":    e2eOperator.String(),
		"sequence_id": "sequence-1",
		"name":        "Сценарий сквозного сценария",
		"steps": []map[string]any{
			{
				"action":      domain.ActionChannelEmailName,
				"material_id": "material-1",
				"segment_id":  "segment-1",
				"delay_nanos": 0,
				"transition":  transition,
			},
			{
				"action":      domain.ActionChannelEmailName,
				"material_id": "material-1",
				"segment_id":  "segment-1",
				"delay_nanos": 0,
				"transition":  domain.TransitionDefault.String(),
			},
		},
	}
}

func enrollLeadBody() map[string]any {
	return map[string]any{
		"tenant_id":    e2eTenant.String(),
		"actor_id":     e2eOperator.String(),
		"sequence_id":  "sequence-1",
		"execution_id": "execution-1",
		"lead_id":      "lead-1",
		"recipient_id": "recipient-1",
	}
}

// TestSequenceFlowFormsDraftsWithoutSending — сквозной сценарий задач 2–8:
// сценарий → добавление лида → черновик в очереди (без отправки) → отдельное
// утверждение → продвижение движком → ответ приостанавливает цепочку →
// возобновление оператором.
func TestSequenceFlowFormsDraftsWithoutSending(t *testing.T) {
	fixture := newE2EFixture(t)
	base := fixture.server.URL

	response, body := doJSON(t, http.MethodPost, base+"/campaigns", createCampaignBody())
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("создание кампании: статус %d, тело %s", response.StatusCode, body)
	}

	// Условие открытия не существует в языке ветвления: команда отвергается.
	opened := defineSequenceBody("opened")
	response, body = doJSON(t, http.MethodPost, base+"/campaigns/campaign-1/sequences", opened)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("условие открытия должно быть отвергнуто: статус %d, тело %s", response.StatusCode, body)
	}

	response, body = doJSON(t, http.MethodPost, base+"/campaigns/campaign-1/sequences",
		defineSequenceBody(domain.TransitionDefault.String()))
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("создание сценария: статус %d, тело %s", response.StatusCode, body)
	}

	response, body = doJSON(t, http.MethodPost, base+"/campaigns/campaign-1/enrollments", enrollLeadBody())
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("добавление лида: статус %d, тело %s", response.StatusCode, body)
	}
	enrolled := decodeBody[struct {
		DraftID string `json:"draft_id"`
		State   string `json:"state"`
	}](t, body)
	if enrolled.DraftID != "execution-1.step-0" {
		t.Fatalf("ожидался черновик первого шага, получено %+v", enrolled)
	}

	// Черновик ждёт утверждения; отправка не выполнена.
	response, body = doJSON(t, http.MethodGet,
		base+"/campaigns/campaign-1/sequences?tenant_id=tenant-1&actor_id=operator-1", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("состояние цепочек: статус %d, тело %s", response.StatusCode, body)
	}
	state := decodeBody[struct {
		PendingDrafts int `json:"pending_drafts"`
	}](t, body)
	if state.PendingDrafts != 1 {
		t.Fatalf("в очереди утверждений ожидался один черновик, получено %d", state.PendingDrafts)
	}

	// Отдельное утверждение первого шага.
	response, body = doJSON(t, http.MethodPost,
		base+"/campaigns/campaign-1/drafts/execution-1.step-0/approve", map[string]any{
			"tenant_id":        e2eTenant.String(),
			"actor_id":         e2eOperator.String(),
			"decision_id":      "decision-1",
			"expected_version": 1,
		})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("утверждение шага: статус %d, тело %s", response.StatusCode, body)
	}

	// Проход движка продвигает цепочку и формирует черновик второго шага,
	// который снова ждёт отдельного утверждения.
	report, err := fixture.slice.SequenceEngine.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("проход движка: %v", err)
	}
	if report.Advanced != 1 {
		t.Fatalf("после утверждения ожидалось продвижение цепочки, advanced=%d", report.Advanced)
	}
	if report.Drafted != 1 {
		t.Fatalf("ожидался черновик второго шага, drafted=%d", report.Drafted)
	}

	// Ответ приостанавливает цепочку.
	response, body = doJSON(t, http.MethodPost,
		base+"/campaigns/campaign-1/enrollments/execution-1/outcomes", map[string]any{
			"tenant_id": e2eTenant.String(),
			"actor_id":  e2eOperator.String(),
			"reaction":  string(domain.ReactionReplied),
		})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("запись ответа: статус %d, тело %s", response.StatusCode, body)
	}
	suspended := decodeBody[struct {
		State string `json:"state"`
	}](t, body)
	if suspended.State != string(domain.ExecutionSuspended) {
		t.Fatalf("ответ должен приостановить цепочку, состояние %q", suspended.State)
	}

	// Возобновление — только решением оператора.
	response, body = doJSON(t, http.MethodPost,
		base+"/campaigns/campaign-1/enrollments/execution-1/resume", map[string]any{
			"tenant_id": e2eTenant.String(),
			"actor_id":  e2eOperator.String(),
		})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("возобновление цепочки: статус %d, тело %s", response.StatusCode, body)
	}
	resumed := decodeBody[struct {
		State string `json:"state"`
	}](t, body)
	if resumed.State != string(domain.ExecutionActive) {
		t.Fatalf("после решения оператора цепочка должна продолжиться, состояние %q", resumed.State)
	}

	// История шагов по лиду доступна.
	response, body = doJSON(t, http.MethodGet,
		base+"/campaigns/campaign-1/sequences/history?tenant_id=tenant-1&actor_id=operator-1&lead_id=lead-1", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("история шагов: статус %d, тело %s", response.StatusCode, body)
	}
	history := decodeBody[struct {
		Entries []struct {
			Result string `json:"result"`
		} `json:"entries"`
	}](t, body)
	if len(history.Entries) == 0 {
		t.Fatal("история шагов не должна быть пустой")
	}
}
