package httpport_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"lead-gen/contracts/lead-management/leadrelevance"
	"lead-gen/contracts/lead-management/leadrelevance/httpport"
)

func newClient(t *testing.T, server *httptest.Server) *httpport.Client {
	t.Helper()
	client, err := httpport.New(server.URL, httpport.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}

// TestReadSuccess проверяет успешное чтение: метод, путь, параметры запроса и
// перевод ответа владельца в Assessment.
func TestReadSuccess(t *testing.T) {
	var (
		gotMethod  string
		gotPath    string
		gotJob     string
		gotCompany string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotJob = r.URL.Query().Get("job_id")
		gotCompany = r.URL.Query().Get("company_id")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"contract":"lead-relevance","schema_version":"1.0.0","job_id":"j1","company_id":"c1","verdict":"unsuitable","signal_kind":"direct","evidence":[{"criterion":"k","fact":"f","source_url":"https://x.example","reasoning":"r","signal_kind":"direct","mismatch":"нет"}],"assessed_at":"2026-10-06T10:00:00Z"}`)
	}))
	defer server.Close()

	assessment, err := newClient(t, server).Read(context.Background(), leadrelevance.Request{JobID: "j1", CompanyID: "c1"})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/lead-relevance" {
		t.Fatalf("запрос %s %s", gotMethod, gotPath)
	}
	if gotJob != "j1" || gotCompany != "c1" {
		t.Fatalf("параметры запроса job=%q company=%q", gotJob, gotCompany)
	}
	if assessment.Verdict != leadrelevance.VerdictUnsuitable || assessment.Contract != leadrelevance.ContractName {
		t.Fatalf("оценка = %+v", assessment)
	}
	if len(assessment.Evidence) != 1 || assessment.Evidence[0].Mismatch != "нет" {
		t.Fatalf("свидетельства = %+v", assessment.Evidence)
	}
	if assessment.AssessedAt.IsZero() {
		t.Fatal("assessed_at не разобран")
	}
}

// TestErrorMapping проверяет перевод HTTP-кодов владельца в исходы контракта:
// 400 — доменный отказ, 404 — оценки нет, прочее — недоступность.
func TestErrorMapping(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   error
	}{
		{"400 отклонение", http.StatusBadRequest, leadrelevance.ErrRejected},
		{"404 не найдено", http.StatusNotFound, leadrelevance.ErrNotFound},
		{"500 сбой", http.StatusInternalServerError, leadrelevance.ErrUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, `{"error":"исход владельца"}`, tc.status)
			}))
			defer server.Close()

			_, err := newClient(t, server).Read(context.Background(), leadrelevance.Request{JobID: "j1", CompanyID: "c1"})
			if !errors.Is(err, tc.want) {
				t.Fatalf("ошибка = %v, ожидалась %v", err, tc.want)
			}
		})
	}
}

// TestNetworkFailureUnavailable проверяет, что недоступный сервис даёт
// ErrUnavailable, а не пустую оценку.
func TestNetworkFailureUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL
	server.Close()

	client, err := httpport.New(url)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := client.Read(context.Background(), leadrelevance.Request{JobID: "j1", CompanyID: "c1"}); !errors.Is(err, leadrelevance.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}

// TestNewRejectsInvalidBaseURL проверяет, что некорректный базовый адрес виден при
// сборке адаптера.
func TestNewRejectsInvalidBaseURL(t *testing.T) {
	for _, base := range []string{"", "   ", "://", "lead-management:8080", "ftp://host"} {
		if _, err := httpport.New(base); err == nil {
			t.Errorf("New(%q) ошибки не вернул", base)
		}
	}
}
