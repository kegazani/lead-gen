package forwarder_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lead-gen/contracts/outreach-orchestration/formsubmission"
	"lead-gen/frontend/public/internal/adapters/forwarder"
	"lead-gen/frontend/public/internal/ports"
)

// capturedRequest — принятый приёмником запрос.
type capturedRequest struct {
	contentType string
	payload     map[string]any
}

// recordingReceiver поднимает реальный HTTP-приёмник и складывает принятые
// запросы в канал. Сервер здесь — тестовый двойник приёмника: настоящий путь
// доставки проходит HTTP-клиент адаптера, а сервер лишь фиксирует, что заявка
// доехала и с какими полями.
func recordingReceiver(t *testing.T, status int) (*httptest.Server, <-chan capturedRequest) {
	t.Helper()
	received := make(chan capturedRequest, 32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			payload = map[string]any{"_decode_error": err.Error()}
		}
		received <- capturedRequest{contentType: r.Header.Get("Content-Type"), payload: payload}
		w.WriteHeader(status)
	}))
	t.Cleanup(server.Close)
	return server, received
}

// filledSubmission — заявка с персональными данными для проверок доставки.
func filledSubmission() ports.Submission {
	return ports.Submission{
		Form:       "demo",
		Name:       "Иван",
		Contact:    "ivan@example.com",
		Message:    "Здравствуйте",
		Consent:    true,
		ReceivedAt: time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC),
	}
}

// TestDeliversToClientReceiver — задача 6.2: заявка достигает приёмника,
// указанного клиентом, с ожидаемыми полями.
func TestDeliversToClientReceiver(t *testing.T) {
	server, received := recordingReceiver(t, http.StatusNoContent)

	sink, err := forwarder.New(forwarder.Config{
		Receivers: map[ports.FormRef]string{"demo": server.URL},
	})
	if err != nil {
		t.Fatalf("forwarder.New: %v", err)
	}

	if err := sink.Submit(context.Background(), filledSubmission()); err != nil {
		t.Fatalf("Submit: неожиданная ошибка доставки: %v", err)
	}

	got := <-received
	if got.contentType != "application/json; charset=utf-8" {
		t.Errorf("Content-Type: получено %q", got.contentType)
	}
	expected := map[string]any{
		"form":       "demo",
		"name":       "Иван",
		"contact":    "ivan@example.com",
		"message":    "Здравствуйте",
		"consent":    true,
		"receivedAt": "2026-03-04T05:06:07Z",
	}
	for field, want := range expected {
		if got.payload[field] != want {
			t.Errorf("поле %q: получено %v, ожидалось %v", field, got.payload[field], want)
		}
	}
}

// TestRoutesFormWithoutOwnReceiverToTransit — задача 6.3: форма без своего
// приёмника уходит в транзитный приёмник, а форма со своим — прямо клиенту.
func TestRoutesFormWithoutOwnReceiverToTransit(t *testing.T) {
	client, clientGot := recordingReceiver(t, http.StatusOK)
	transit, transitGot := recordingReceiver(t, http.StatusOK)

	sink, err := forwarder.New(forwarder.Config{
		Receivers: map[ports.FormRef]string{"own": client.URL},
		Transit:   transit.URL,
	})
	if err != nil {
		t.Fatalf("forwarder.New: %v", err)
	}

	if err := sink.Submit(context.Background(), ports.Submission{Form: "own", Name: "А"}); err != nil {
		t.Fatalf("Submit(own): %v", err)
	}
	if err := sink.Submit(context.Background(), ports.Submission{Form: "free", Name: "Б"}); err != nil {
		t.Fatalf("Submit(free): %v", err)
	}

	if got := <-clientGot; got.payload["form"] != "own" {
		t.Errorf("приёмник клиента получил %v", got.payload["form"])
	}
	if got := <-transitGot; got.payload["form"] != "free" {
		t.Errorf("транзитный приёмник получил %v", got.payload["form"])
	}
	// Приёмник клиента не должен увидеть заявку чужой формы.
	select {
	case extra := <-clientGot:
		t.Errorf("приёмник клиента получил лишнюю заявку: %v", extra.payload["form"])
	default:
	}
}

// TestUnknownFormWithoutTransitIsNotFound: форма, которой нет ни у клиента, ни в
// транзитном контуре, — «не найдена», а не молчаливый успех.
func TestUnknownFormWithoutTransitIsNotFound(t *testing.T) {
	server, _ := recordingReceiver(t, http.StatusOK)
	sink, err := forwarder.New(forwarder.Config{
		Receivers: map[ports.FormRef]string{"own": server.URL},
	})
	if err != nil {
		t.Fatalf("forwarder.New: %v", err)
	}
	if err := sink.Submit(context.Background(), ports.Submission{Form: "unknown"}); !errors.Is(err, formsubmission.ErrNotFound) {
		t.Fatalf("ожидалась formsubmission.ErrNotFound, получено %v", err)
	}
}

// TestUnavailableReceiverReturnsError — задача 6.4: недоступный приёмник (ответ
// вне 2xx) — ошибка доставки, а не «принято».
func TestUnavailableReceiverReturnsError(t *testing.T) {
	server, _ := recordingReceiver(t, http.StatusBadGateway)
	sink, err := forwarder.New(forwarder.Config{
		Receivers: map[ports.FormRef]string{"demo": server.URL},
	})
	if err != nil {
		t.Fatalf("forwarder.New: %v", err)
	}

	err = sink.Submit(context.Background(), filledSubmission())
	if err == nil {
		t.Fatal("недоступный приёмник принят за успешную доставку")
	}
	if errors.Is(err, formsubmission.ErrNotFound) {
		t.Errorf("недоступность приёмника выдана за неизвестную форму: %v", err)
	}
}

// TestClosedReceiverReturnsError: приёмник не отвечает вовсе (соединение
// отвергнуто) — тоже ошибка доставки, а не успех.
func TestClosedReceiverReturnsError(t *testing.T) {
	server, _ := recordingReceiver(t, http.StatusOK)
	url := server.URL
	server.Close()

	sink, err := forwarder.New(forwarder.Config{
		Receivers: map[ports.FormRef]string{"demo": url},
	})
	if err != nil {
		t.Fatalf("forwarder.New: %v", err)
	}
	if err := sink.Submit(context.Background(), filledSubmission()); err == nil {
		t.Fatal("закрытый приёмник принят за успешную доставку")
	}
}

// TestRedirectIsNotFollowed: перенаправление приёмника — не доставка. Заявка не
// должна уходить по адресу, которого клиент не указывал.
func TestRedirectIsNotFollowed(t *testing.T) {
	target, targetGot := recordingReceiver(t, http.StatusOK)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	t.Cleanup(redirector.Close)

	sink, err := forwarder.New(forwarder.Config{
		Receivers: map[ports.FormRef]string{"demo": redirector.URL},
	})
	if err != nil {
		t.Fatalf("forwarder.New: %v", err)
	}
	if err := sink.Submit(context.Background(), filledSubmission()); err == nil {
		t.Fatal("перенаправление принято за успешную доставку")
	}
	select {
	case got := <-targetGot:
		t.Errorf("заявка ушла по перенаправлению: %v", got.payload["form"])
	case <-time.After(50 * time.Millisecond):
	}
}

// TestSubmitHonoursCancellation: отменённый контекст посетителя не выполняет
// доставку «всё равно».
func TestSubmitHonoursCancellation(t *testing.T) {
	server, received := recordingReceiver(t, http.StatusOK)
	sink, err := forwarder.New(forwarder.Config{
		Receivers: map[ports.FormRef]string{"demo": server.URL},
	})
	if err != nil {
		t.Fatalf("forwarder.New: %v", err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sink.Submit(cancelled, filledSubmission()); !errors.Is(err, context.Canceled) {
		t.Fatalf("ожидалась context.Canceled, получено %v", err)
	}
	select {
	case got := <-received:
		t.Errorf("отменённая заявка всё равно доставлена: %v", got.payload["form"])
	case <-time.After(50 * time.Millisecond):
	}
}

// TestNewRejectsInvalidReceiver: адрес приёмника проверяется на абсолютный
// http/https при сборке, а не при заявке посетителя.
func TestNewRejectsInvalidReceiver(t *testing.T) {
	for _, raw := range []string{"", "  ", "not-a-url", "ftp://example.com", "//example.com", "/relative", "javascript:alert(1)"} {
		if _, err := forwarder.New(forwarder.Config{Receivers: map[ports.FormRef]string{"demo": raw}}); err == nil {
			t.Errorf("адрес приёмника %q принят", raw)
		}
	}
}

// TestNewRejectsInvalidTransit: транзитный приёмник проверяется так же.
func TestNewRejectsInvalidTransit(t *testing.T) {
	if _, err := forwarder.New(forwarder.Config{Transit: "example.com/webhook"}); err == nil {
		t.Error("относительный адрес транзитного приёмника принят")
	}
}

// TestNewRequiresAtLeastOneReceiver: адаптер без единого приёмника не собирается —
// иначе каждая заявка оканчивалась бы отказом уже в проде.
func TestNewRequiresAtLeastOneReceiver(t *testing.T) {
	if _, err := forwarder.New(forwarder.Config{}); err == nil {
		t.Error("сборка без приёмников принята")
	}
}
