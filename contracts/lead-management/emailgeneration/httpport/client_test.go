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

	"lead-gen/contracts/lead-management/emailgeneration"
	"lead-gen/contracts/lead-management/emailgeneration/httpport"
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

// TestGenerateSuccess проверяет успешную генерацию: метод, путь, тело запроса и
// перевод ответа владельца в Email.
func TestGenerateSuccess(t *testing.T) {
	var (
		gotMethod string
		gotPath   string
		gotBody   map[string]any
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"subject":"Автоматизация продаж для {{company}}","body":"Здравствуйте!","language":"ru"}`)
	}))
	defer server.Close()

	email, err := newClient(t, server).Generate(context.Background(), emailgeneration.Request{
		RequestID: "req-9", TenantID: "tenant-1", LeadID: "lead.example",
		Criteria: "оффер: автоматизация продаж", Language: "ru", Tone: "деловой",
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/email-generation" {
		t.Fatalf("запрос %s %s", gotMethod, gotPath)
	}
	if gotBody["request_id"] != "req-9" || gotBody["tenant_id"] != "tenant-1" ||
		gotBody["lead_id"] != "lead.example" || gotBody["criteria"] != "оффер: автоматизация продаж" ||
		gotBody["language"] != "ru" || gotBody["tone"] != "деловой" {
		t.Fatalf("тело команды = %+v", gotBody)
	}
	if email.Subject != "Автоматизация продаж для {{company}}" || email.Body != "Здравствуйте!" || email.Language != "ru" {
		t.Fatalf("письмо = %+v", email)
	}
}

// TestErrorMapping проверяет перевод HTTP-кодов владельца в исходы контракта:
// 400 — доменный отказ, прочее — недоступность.
func TestErrorMapping(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   error
	}{
		{"400 отклонение", http.StatusBadRequest, emailgeneration.ErrRejected},
		{"409 конфликт", http.StatusConflict, emailgeneration.ErrUnavailable},
		{"500 сбой", http.StatusInternalServerError, emailgeneration.ErrUnavailable},
		{"503 недоступно", http.StatusServiceUnavailable, emailgeneration.ErrUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, `{"error":"отказ владельца"}`, tc.status)
			}))
			defer server.Close()

			if _, err := newClient(t, server).Generate(context.Background(), emailgeneration.Request{LeadID: "x", Criteria: "y"}); !errors.Is(err, tc.want) {
				t.Fatalf("Generate(%d) ошибка = %v, ожидалась %v", tc.status, err, tc.want)
			}
		})
	}
}

// TestMalformedSuccessBodyUnavailable проверяет, что неразборчивый успешный ответ
// считается сбоем транспорта, а не выдуманным исходом.
func TestMalformedSuccessBodyUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{не json`)
	}))
	defer server.Close()

	if _, err := newClient(t, server).Generate(context.Background(), emailgeneration.Request{LeadID: "x", Criteria: "y"}); !errors.Is(err, emailgeneration.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
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
	if _, err := client.Generate(context.Background(), emailgeneration.Request{LeadID: "x", Criteria: "y"}); !errors.Is(err, emailgeneration.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}

// TestContextDeadlineHonored проверяет, что дедлайн берётся из вызова и завершает
// запрос недоступностью, а не висит бесконечно.
func TestContextDeadlineHonored(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, err := newClient(t, server).Generate(ctx, emailgeneration.Request{LeadID: "x", Criteria: "y"}); !errors.Is(err, emailgeneration.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
	if elapsed := time.Since(start); elapsed > 150*time.Millisecond {
		t.Fatalf("дедлайн вызова не соблюдён: %v", elapsed)
	}
}

// TestNewRejectsInvalidBaseURL проверяет, что некорректный базовый адрес виден при
// сборке адаптера, а не при первом вызове.
func TestNewRejectsInvalidBaseURL(t *testing.T) {
	for _, base := range []string{"", "   ", "://", "lead-management:8080", "ftp://host"} {
		if _, err := httpport.New(base); err == nil {
			t.Errorf("New(%q) ошибки не вернул", base)
		}
	}
}
