package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	approvedactionadapter "lead-gen/backend/contexts/message-delivery/internal/adapters/commandsource/approvedaction"
	httpapi "lead-gen/backend/contexts/message-delivery/internal/adapters/http"
	approvedaction "lead-gen/contracts/outreach-orchestration/approvedaction"
)

var testInstant = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

// validAction собирает корректный контракт approved-action для проверок.
func validAction() approvedaction.ApprovedAction {
	action := approvedaction.NewApprovedAction(approvedaction.Envelope{
		EventID:       "evt-1",
		OccurredAt:    testInstant,
		TenantID:      "tenant-1",
		CorrelationID: "corr-1",
		CausationID:   "dec-1",
	})
	action.ApprovedActionID = "act-1"
	action.CampaignID = "camp-1"
	action.DraftID = "draft-1"
	action.DraftVersion = 3
	action.DecisionID = "dec-1"
	action.Channel = approvedaction.ChannelEmailName
	action.RecipientRef = "recipient-ref-1"
	action.DecidedAt = testInstant
	return action
}

// recordingPublisher запоминает принятые действия или отдаёт заданную ошибку.
type recordingPublisher struct {
	actions []approvedaction.ApprovedAction
	err     error
}

func (p *recordingPublisher) Publish(_ context.Context, action approvedaction.ApprovedAction) error {
	if p.err != nil {
		return p.err
	}
	p.actions = append(p.actions, action)
	return nil
}

// post отправляет тело на приёмную поверхность и возвращает ответ.
func post(t *testing.T, server *httptest.Server, body []byte) *http.Response {
	t.Helper()
	resp, err := server.Client().Post(server.URL+"/approved-action", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /approved-action: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// TestIntakeAcceptsValidAction проверяет успешный приём: 204 и действие дошло до
// приёмника.
func TestIntakeAcceptsValidAction(t *testing.T) {
	publisher := &recordingPublisher{}
	server := httptest.NewServer(httpapi.NewServer(publisher))
	defer server.Close()

	body, err := json.Marshal(validAction())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp := post(t, server, body)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("статус = %d, ожидался 204", resp.StatusCode)
	}
	if len(publisher.actions) != 1 {
		t.Fatalf("приёмник получил %d действий, ожидалось 1", len(publisher.actions))
	}
	if publisher.actions[0].ApprovedActionID != "act-1" {
		t.Fatalf("действие = %+v", publisher.actions[0])
	}
}

// TestIntakeRejectsMalformedBody проверяет, что неразборчивое тело — 400, а не
// успех и не недоступность.
func TestIntakeRejectsMalformedBody(t *testing.T) {
	server := httptest.NewServer(httpapi.NewServer(&recordingPublisher{}))
	defer server.Close()

	if resp := post(t, server, []byte("{не json")); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("статус = %d, ожидался 400", resp.StatusCode)
	}
}

// TestIntakeRejectsUnknownField проверяет, что чужой формат отвергается:
// DisallowUnknownFields.
func TestIntakeRejectsUnknownField(t *testing.T) {
	server := httptest.NewServer(httpapi.NewServer(&recordingPublisher{}))
	defer server.Close()

	raw, err := json.Marshal(validAction())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var withExtra map[string]any
	if err := json.Unmarshal(raw, &withExtra); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	withExtra["unexpected_field"] = true
	body, err := json.Marshal(withExtra)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if resp := post(t, server, body); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("статус = %d, ожидался 400", resp.StatusCode)
	}
}

// TestIntakeRejectsInvalidAction проверяет, что структурно верное, но неполное
// действие (не проходит Validate) — 400.
func TestIntakeRejectsInvalidAction(t *testing.T) {
	publisher := &recordingPublisher{}
	server := httptest.NewServer(httpapi.NewServer(publisher))
	defer server.Close()

	body, err := json.Marshal(approvedaction.ApprovedAction{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if resp := post(t, server, body); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("статус = %d, ожидался 400", resp.StatusCode)
	}
	if len(publisher.actions) != 0 {
		t.Fatal("невалидное действие не должно доходить до приёмника")
	}
}

// TestIntakeUnavailableWithoutPublisher проверяет, что приём без подключённого
// приёмника честно отвечает 503.
func TestIntakeUnavailableWithoutPublisher(t *testing.T) {
	server := httptest.NewServer(httpapi.NewServer(nil))
	defer server.Close()

	body, err := json.Marshal(validAction())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if resp := post(t, server, body); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("статус = %d, ожидался 503", resp.StatusCode)
	}
}

// TestIntakeMapsPublisherOutcome проверяет перевод исхода приёмника в код ответа:
// доменный отказ — 400, недоступность — 503.
func TestIntakeMapsPublisherOutcome(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
	}{
		{"доменный отказ", approvedactionadapter.ErrRejected, http.StatusBadRequest},
		{"недоступность", approvedactionadapter.ErrUnavailable, http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(httpapi.NewServer(&recordingPublisher{err: tc.err}))
			defer server.Close()

			body, err := json.Marshal(validAction())
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if resp := post(t, server, body); resp.StatusCode != tc.status {
				t.Fatalf("статус = %d, ожидался %d", resp.StatusCode, tc.status)
			}
		})
	}
}
