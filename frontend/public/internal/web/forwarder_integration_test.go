package web_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lead-gen/frontend/public/internal/adapters/forwarder"
	"lead-gen/frontend/public/internal/ports"
	"lead-gen/frontend/public/internal/web"
)

// recordingEndpoint поднимает реальный HTTP-приёмник и отдаёт канал принятых тел
// (разобранных как JSON). Это тестовый двойник приёмника клиента: доставку
// выполняет настоящий HTTP-клиент адаптера, а сервер только фиксирует заявку.
func recordingEndpoint(t *testing.T, status int) (*httptest.Server, <-chan map[string]any) {
	t.Helper()
	received := make(chan map[string]any, 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			payload = map[string]any{"_decode_error": err.Error()}
		}
		received <- payload
		w.WriteHeader(status)
	}))
	t.Cleanup(server.Close)
	return server, received
}

// handlerWithForwarding собирает edge на реальном адаптере пересылки. Журнал
// пишется в переданный буфер — его и проверяет аудит персональных данных.
func handlerWithForwarding(t *testing.T, config forwarder.Config, logs *bytes.Buffer) http.Handler {
	t.Helper()
	sink, err := forwarder.New(config)
	if err != nil {
		t.Fatalf("forwarder.New: %v", err)
	}
	var logger *slog.Logger
	if logs != nil {
		logger = slog.New(slog.NewJSONHandler(logs, nil))
	}
	handler, _, _ := handlerWith(t, func(webConfig *web.Config) {
		webConfig.Submissions = sink
		if logger != nil {
			webConfig.Logger = logger
		}
	})
	return handler
}

// TestSubmittedApplicationReachesClientReceiver — задача 6.2: поданная через форму
// заявка достигает приёмника клиента с ожидаемыми полями.
func TestSubmittedApplicationReachesClientReceiver(t *testing.T) {
	receiver, received := recordingEndpoint(t, http.StatusNoContent)
	handler := handlerWithForwarding(t, forwarder.Config{
		Receivers: map[ports.FormRef]string{demoForm: receiver.URL},
	}, nil)

	recorder := serve(t, handler, http.MethodPost, "/f/"+demoForm, validForm())
	if recorder.Code != http.StatusSeeOther {
		t.Fatalf("код ответа: получено %d, ожидалось %d", recorder.Code, http.StatusSeeOther)
	}

	payload := <-received
	expected := map[string]any{
		"form":    demoForm,
		"name":    "Иван Петров",
		"contact": "ivan@example.com",
		"message": "Прошу связаться со мной",
		"consent": true,
	}
	for field, want := range expected {
		if payload[field] != want {
			t.Errorf("поле %q у приёмника клиента: получено %v, ожидалось %v", field, payload[field], want)
		}
	}
}

// TestTransitForwardsWithoutKeepingPersonalData — задача 6.3: серия заявок, где
// формы без своего приёмника уходят в транзитный контур, пересылается целиком, а
// аудит журналов не находит персональных данных заявителей. Локальных хранилищ у
// edge нет вовсе (модуль не подключает БД и не пишет файлов; границу держит
// tools/importlint), поэтому проверяемое хранилище на стороне платформы — журнал.
func TestTransitForwardsWithoutKeepingPersonalData(t *testing.T) {
	clientReceiver, clientReceived := recordingEndpoint(t, http.StatusNoContent)
	transitReceiver, transitReceived := recordingEndpoint(t, http.StatusNoContent)

	var logs bytes.Buffer
	handler := handlerWithForwarding(t, forwarder.Config{
		Receivers: map[ports.FormRef]string{demoForm: clientReceiver.URL},
		Transit:   transitReceiver.URL,
	}, &logs)

	var secrets []string
	for i := 0; i < 3; i++ {
		// Форма со своим приёмником: уходит прямо клиенту.
		own := validForm()
		ownName := "КлиентИмя"
		ownContact := "client@example.com"
		own.Set("name", ownName)
		own.Set("contact", ownContact)
		secrets = append(secrets, ownName, ownContact)
		if recorder := serve(t, handler, http.MethodPost, "/f/"+demoForm, own); recorder.Code != http.StatusSeeOther {
			t.Fatalf("заявка на приёмник клиента: код %d", recorder.Code)
		}

		// Форма без своего приёмника: уходит в транзитный контур.
		transit := validForm()
		transitName := "ТранзитИмя"
		transitContact := "transit@example.com"
		transit.Set("name", transitName)
		transit.Set("contact", transitContact)
		secrets = append(secrets, transitName, transitContact)
		if recorder := serve(t, handler, http.MethodPost, "/f/no-receiver", transit); recorder.Code != http.StatusSeeOther {
			t.Fatalf("транзитная заявка: код %d", recorder.Code)
		}
	}

	// Обе серии доехали до своих приёмников — по три заявки в каждый.
	for i := 0; i < 3; i++ {
		if payload := <-clientReceived; payload["form"] != demoForm {
			t.Errorf("приёмник клиента получил форму %v", payload["form"])
		}
		if payload := <-transitReceived; payload["form"] != "no-receiver" {
			t.Errorf("транзитный приёмник получил форму %v", payload["form"])
		}
	}

	// Аудит журналов: значения заявок в них не попали (ни имена, ни контакты,
	// ни текст сообщения).
	logged := logs.String()
	if logged == "" {
		t.Fatal("журнал пуст: аудит нечего проверять, поведение не подтверждено")
	}
	audited := append([]string{"Прошу связаться со мной"}, secrets...)
	for _, secret := range audited {
		if strings.Contains(logged, secret) {
			t.Errorf("персональные данные %q попали в журнал:\n%s", secret, logged)
		}
	}
}

// TestUnavailableReceiverIsNotAccepted — задача 6.4: при недоступном приёмнике
// посетитель получает сообщение об отказе, заявка не выдаётся как принятая и не
// сохраняется на платформе (в том числе не попадает в журнал).
func TestUnavailableReceiverIsNotAccepted(t *testing.T) {
	receiver, _ := recordingEndpoint(t, http.StatusBadGateway)

	var logs bytes.Buffer
	handler := handlerWithForwarding(t, forwarder.Config{
		Receivers: map[ports.FormRef]string{demoForm: receiver.URL},
	}, &logs)

	recorder := serve(t, handler, http.MethodPost, "/f/"+demoForm, validForm())
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("код ответа: получено %d, ожидалось %d", recorder.Code, http.StatusServiceUnavailable)
	}
	if location := recorder.Header().Get("Location"); location != "" {
		t.Errorf("недоступный приёмник выдал заявку как принятую: Location %q", location)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "Заявка не отправлена") {
		t.Errorf("нет честного сообщения посетителю об отказе:\n%s", body)
	}
	if cache := recorder.Header().Get("Cache-Control"); cache != "no-store" {
		t.Errorf("Cache-Control отказа: получено %q", cache)
	}

	logged := logs.String()
	for _, secret := range []string{"Иван Петров", "ivan@example.com", "Прошу связаться со мной"} {
		if strings.Contains(logged, secret) {
			t.Errorf("персональные данные %q попали в журнал:\n%s", secret, logged)
		}
	}
}
