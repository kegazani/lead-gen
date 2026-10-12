package httpport_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lead-gen/contracts/outreach-orchestration/approvedaction"
	"lead-gen/contracts/outreach-orchestration/approvedaction/httpport"
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

// newClient собирает адаптер на тестовом сервере.
func newClient(t *testing.T, server *httptest.Server) *httpport.Client {
	t.Helper()
	client, err := httpport.New(server.URL, httpport.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}

// TestPublishSuccess проверяет успешную публикацию: метод, путь, wire-формат тела
// и приём кодов 200 и 204 как успеха.
func TestPublishSuccess(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusNoContent} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var (
				gotMethod string
				gotPath   string
				gotBody   approvedaction.ApprovedAction
			)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod = r.Method
				gotPath = r.URL.Path
				data, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(data, &gotBody)
				w.WriteHeader(status)
			}))
			defer server.Close()

			if err := newClient(t, server).Publish(context.Background(), validAction()); err != nil {
				t.Fatalf("Publish: %v", err)
			}
			if gotMethod != http.MethodPost || gotPath != "/approved-action" {
				t.Fatalf("запрос %s %s", gotMethod, gotPath)
			}
			if gotBody.Name != approvedaction.ContractName {
				t.Fatalf("name = %q", gotBody.Name)
			}
			if gotBody.ApprovedActionID != "act-1" || gotBody.DraftVersion != 3 ||
				gotBody.Channel != approvedaction.ChannelEmailName {
				t.Fatalf("тело команды = %+v", gotBody)
			}
		})
	}
}

// TestPublishDomainRejection проверяет, что 400 приёмной стороны — доменный отказ,
// отличимый от недоступности.
func TestPublishDomainRejection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"действие отклонено приёмником"}`, http.StatusBadRequest)
	}))
	defer server.Close()

	err := newClient(t, server).Publish(context.Background(), validAction())
	if !errors.Is(err, httpport.ErrRejected) {
		t.Fatalf("ошибка = %v, ожидалась ErrRejected", err)
	}
	if errors.Is(err, httpport.ErrUnavailable) {
		t.Fatalf("доменный отказ не должен быть недоступностью: %v", err)
	}
}

// TestPublishUnavailable проверяет, что прочие коды приёмной стороны (в том числе
// 5xx) — недоступность, а не выдуманный исход.
func TestPublishUnavailable(t *testing.T) {
	for _, status := range []int{http.StatusConflict, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, `{"error":"сбой приёмника"}`, status)
			}))
			defer server.Close()

			err := newClient(t, server).Publish(context.Background(), validAction())
			if !errors.Is(err, httpport.ErrUnavailable) {
				t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
			}
			if errors.Is(err, httpport.ErrRejected) {
				t.Fatalf("недоступность не должна быть доменным отказом: %v", err)
			}
		})
	}
}

// TestNetworkFailureUnavailable проверяет, что недоступный сервис даёт
// ErrUnavailable, а не доменный исход.
func TestNetworkFailureUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL
	server.Close()

	client, err := httpport.New(url)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := client.Publish(context.Background(), validAction()); !errors.Is(err, httpport.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}

// TestInvalidActionNotSent проверяет локальное отклонение невалидного действия:
// ошибка возвращается до сети, сетевого вызова нет.
func TestInvalidActionNotSent(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	err := newClient(t, server).Publish(context.Background(), approvedaction.ApprovedAction{})
	if !errors.Is(err, httpport.ErrRejected) {
		t.Fatalf("ошибка = %v, ожидалась ErrRejected", err)
	}
	if called {
		t.Fatal("невалидное действие не должно уходить в сеть")
	}
}

// TestContextDeadlineHonored проверяет, что дедлайн берётся из вызова и завершает
// запрос недоступностью, а не висит бесконечно.
func TestContextDeadlineHonored(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	if err := newClient(t, server).Publish(ctx, validAction()); !errors.Is(err, httpport.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
	if elapsed := time.Since(start); elapsed > 150*time.Millisecond {
		t.Fatalf("дедлайн вызова не соблюдён: %v", elapsed)
	}
}

// TestNewRejectsInvalidBaseURL проверяет, что некорректный базовый адрес виден при
// сборке адаптера, а не при первой публикации.
func TestNewRejectsInvalidBaseURL(t *testing.T) {
	for _, base := range []string{"", "   ", "://", "message-delivery:8088", "ftp://host"} {
		if _, err := httpport.New(base); err == nil {
			t.Errorf("New(%q) ошибки не вернул", base)
		}
	}
}
