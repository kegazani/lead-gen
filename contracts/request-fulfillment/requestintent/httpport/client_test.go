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

	"lead-gen/contracts/request-fulfillment/requestintent"
	"lead-gen/contracts/request-fulfillment/requestintent/httpport"
)

// newClient собирает адаптер на тестовом сервере.
func newClient(t *testing.T, server *httptest.Server) *httpport.Client {
	t.Helper()
	client, err := httpport.New(server.URL, httpport.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}

// TestSubmitSuccess проверяет успешную команду: метод, путь, тело, заголовки
// идемпотентности и корреляции, а также перевод ответа 201 в SubmitResult.
func TestSubmitSuccess(t *testing.T) {
	var (
		gotMethod string
		gotPath   string
		gotBody   map[string]any
		gotIdem   string
		gotCorr   string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotIdem = r.Header.Get("Idempotency-Key")
		gotCorr = r.Header.Get("X-Correlation-Id")
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"request_id":"req-9","status":"submitted","version":1}`)
	}))
	defer server.Close()

	result, err := newClient(t, server).Submit(context.Background(), requestintent.SubmitIntent{
		RequestID:     "req-9",
		OfferID:       "offer-1",
		TenantID:      "tenant-1",
		Title:         "Заявка клиента",
		Actor:         "client",
		Content:       "Оффер",
		Target:        "рынок",
		CorrelationID: "corr-1",
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if result.RequestID != "req-9" || result.Status != "submitted" {
		t.Fatalf("результат = %+v", result)
	}
	if gotMethod != http.MethodPost || gotPath != "/requests" {
		t.Fatalf("запрос %s %s", gotMethod, gotPath)
	}
	if gotIdem != "req-9" {
		t.Fatalf("Idempotency-Key = %q", gotIdem)
	}
	if gotCorr != "corr-1" {
		t.Fatalf("X-Correlation-Id = %q", gotCorr)
	}
	if gotBody["request_id"] != "req-9" || gotBody["offer_id"] != "offer-1" || gotBody["title"] != "Заявка клиента" ||
		gotBody["tenant_id"] != "tenant-1" || gotBody["content"] != "Оффер" ||
		gotBody["target"] != "рынок" || gotBody["correlation_id"] != "corr-1" {
		t.Fatalf("тело команды = %+v", gotBody)
	}
}

// TestSubmitFallsBackToIntentID проверяет, что пустой request_id в ответе не
// теряет идентификатор, назначенный потребителем.
func TestSubmitFallsBackToIntentID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"status":"submitted"}`)
	}))
	defer server.Close()

	result, err := newClient(t, server).Submit(context.Background(), requestintent.SubmitIntent{RequestID: "req-1"})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if result.RequestID != "req-1" {
		t.Fatalf("RequestID = %q", result.RequestID)
	}
}

// TestStatusSuccess проверяет успешное чтение: путь, обязательный тенант в
// query и перевод представления владельца в StatusResult.
func TestStatusSuccess(t *testing.T) {
	var gotPath, gotTenant string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotTenant = r.URL.Query().Get("tenant_id")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"RequestID":"req-9","Status":"processing","Title":"Короткое имя","Offer":{"OfferID":"offer-1","Content":"Оффер","Target":"рынок","Revision":3}}`)
	}))
	defer server.Close()

	result, err := newClient(t, server).Status(context.Background(), requestintent.StatusQuery{RequestID: "req-9", TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if gotPath != "/requests/req-9" || gotTenant != "tenant-1" {
		t.Fatalf("запрос %s?tenant_id=%s", gotPath, gotTenant)
	}
	want := requestintent.StatusResult{RequestID: "req-9", Status: "processing", Title: "Короткое имя", OfferContent: "Оффер", OfferTarget: "рынок", OfferRevision: 3}
	if result != want {
		t.Fatalf("результат = %+v, ожидалось %+v", result, want)
	}
}

// TestStatusDecodesFailureFields проверяет аддитивные поля MINOR 1.1.0: ответ с
// фазой и причиной сбоя декодируется в StatusResult, а старый ответ без них
// остаётся читаемым (поля пусты).
func TestStatusDecodesFailureFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"RequestID":"req-f","Status":"failed","Phase":"failed","ErrorReason":"search_failed","ErrorMessage":"поиск упал"}`)
	}))
	defer server.Close()

	result, err := newClient(t, server).Status(context.Background(), requestintent.StatusQuery{RequestID: "req-f", TenantID: "t"})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	want := requestintent.StatusResult{
		RequestID: "req-f", Status: "failed", Phase: "failed",
		ErrorReason: "search_failed", ErrorMessage: "поиск упал",
	}
	if result != want {
		t.Fatalf("результат = %+v, ожидалось %+v", result, want)
	}
}

// TestStatusEscapesRequestID проверяет, что идентификатор заявки попадает в путь
// безопасно, а не ломает маршрут.
func TestStatusEscapesRequestID(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		_, _ = io.WriteString(w, `{"RequestID":"a/b","Status":"submitted"}`)
	}))
	defer server.Close()

	if _, err := newClient(t, server).Status(context.Background(), requestintent.StatusQuery{RequestID: "a/b", TenantID: "t"}); err != nil {
		t.Fatalf("Status: %v", err)
	}
	if gotPath != "/requests/a%2Fb" {
		t.Fatalf("путь = %q", gotPath)
	}
}

// TestListSuccess проверяет успешное чтение списка: путь, обязательный тенант в
// query и перевод ответа владельца в []ListEntry.
func TestListSuccess(t *testing.T) {
	var gotPath, gotTenant string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotTenant = r.URL.Query().Get("tenant_id")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[{"RequestID":"req-a","Status":"submitted"},{"RequestID":"req-b","Status":"needs-revision"}]`)
	}))
	defer server.Close()

	entries, err := newClient(t, server).List(context.Background(), requestintent.ListQuery{TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if gotPath != "/requests" || gotTenant != "tenant-1" {
		t.Fatalf("запрос %s?tenant_id=%s", gotPath, gotTenant)
	}
	want := []requestintent.ListEntry{
		{RequestID: "req-a", Status: "submitted"},
		{RequestID: "req-b", Status: "needs-revision"},
	}
	if len(entries) != len(want) || entries[0] != want[0] || entries[1] != want[1] {
		t.Fatalf("результат = %+v, ожидалось %+v", entries, want)
	}
}

// TestErrorMapping проверяет перевод HTTP-кодов владельца в исходы контракта:
// 400 — доменный отказ, 404 — заявки нет, прочее (5xx) — недоступность.
func TestErrorMapping(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   error
	}{
		{"400 отклонение", http.StatusBadRequest, requestintent.ErrRejected},
		{"404 не найдено", http.StatusNotFound, requestintent.ErrNotFound},
		{"409 конфликт", http.StatusConflict, requestintent.ErrUnavailable},
		{"500 сбой", http.StatusInternalServerError, requestintent.ErrUnavailable},
		{"503 недоступно", http.StatusServiceUnavailable, requestintent.ErrUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, `{"error":"отказ владельца"}`, tc.status)
			}))
			defer server.Close()
			client := newClient(t, server)

			if _, err := client.Submit(context.Background(), requestintent.SubmitIntent{RequestID: "req-1"}); !errors.Is(err, tc.want) {
				t.Fatalf("Submit(%d) ошибка = %v, ожидалась %v", tc.status, err, tc.want)
			}
			if _, err := client.Status(context.Background(), requestintent.StatusQuery{RequestID: "req-1", TenantID: "t"}); !errors.Is(err, tc.want) {
				t.Fatalf("Status(%d) ошибка = %v, ожидалась %v", tc.status, err, tc.want)
			}
		})
	}
}

// TestMalformedSuccessBodyUnavailable проверяет, что неразборчивый успешный
// ответ считается сбоем транспорта, а не выдуманным исходом.
func TestMalformedSuccessBodyUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{не json`)
	}))
	defer server.Close()
	client := newClient(t, server)

	if _, err := client.Submit(context.Background(), requestintent.SubmitIntent{}); !errors.Is(err, requestintent.ErrUnavailable) {
		t.Fatalf("Submit ошибка = %v, ожидалась ErrUnavailable", err)
	}
	if _, err := client.Status(context.Background(), requestintent.StatusQuery{}); !errors.Is(err, requestintent.ErrUnavailable) {
		t.Fatalf("Status ошибка = %v, ожидалась ErrUnavailable", err)
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
	if _, err := client.Submit(context.Background(), requestintent.SubmitIntent{RequestID: "req-1"}); !errors.Is(err, requestintent.ErrUnavailable) {
		t.Fatalf("Submit ошибка = %v, ожидалась ErrUnavailable", err)
	}
}

// TestContextDeadlineHonored проверяет, что дедлайн берётся из вызова и
// завершает запрос недоступностью, а не висит бесконечно.
func TestContextDeadlineHonored(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Владелец отвечает заметно позже дедлайна вызова: адаптер обязан
		// оборвать вызов сам, а не ждать ответа.
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, err := newClient(t, server).Submit(ctx, requestintent.SubmitIntent{RequestID: "req-1"}); !errors.Is(err, requestintent.ErrUnavailable) {
		t.Fatalf("Submit ошибка = %v, ожидалась ErrUnavailable", err)
	}
	if elapsed := time.Since(start); elapsed > 150*time.Millisecond {
		t.Fatalf("дедлайн вызова не соблюдён: %v", elapsed)
	}
}

// TestNewRejectsInvalidBaseURL проверяет, что некорректный базовый адрес виден
// при сборке адаптера, а не при первой заявке.
func TestNewRejectsInvalidBaseURL(t *testing.T) {
	for _, base := range []string{"", "   ", "://", "request-fulfillment:8081", "ftp://host"} {
		if _, err := httpport.New(base); err == nil {
			t.Errorf("New(%q) ошибки не вернул", base)
		}
	}
}
