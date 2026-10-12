package httpport_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"lead-gen/contracts/content-production/contentversion"
	"lead-gen/contracts/content-production/contentversion/httpport"
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

// TestVersionSuccess проверяет успешное чтение: метод, путь, тело запроса и перевод
// ответа владельца в Version (включая тему материала).
func TestVersionSuccess(t *testing.T) {
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
		_, _ = io.WriteString(w, `{"asset_id":"asset-1","version_id":"ver-1","revision":2,"language":"ru","region":"RU","subject":"Тема письма","body":"Текст письма."}`)
	}))
	defer server.Close()

	got, err := newClient(t, server).Version(context.Background(), contentversion.Query{
		AssetID: "asset-1", VersionID: "ver-1", TenantID: "tenant-1",
	})
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/content-version" {
		t.Fatalf("запрос %s %s", gotMethod, gotPath)
	}
	if gotBody["asset_id"] != "asset-1" || gotBody["version_id"] != "ver-1" || gotBody["tenant_id"] != "tenant-1" {
		t.Fatalf("тело команды = %+v", gotBody)
	}
	want := contentversion.Version{
		AssetID:   "asset-1",
		VersionID: "ver-1",
		Revision:  2,
		Language:  "ru",
		Region:    "RU",
		Subject:   "Тема письма",
		Body:      "Текст письма.",
	}
	if got != want {
		t.Fatalf("версия = %+v, ожидалось %+v", got, want)
	}
}

// TestVersionEmptyVersionID проверяет, что пустой VersionID уходит владельцу как
// есть: поле присутствует и пусто, владелец сам выбирает последнюю версию.
func TestVersionEmptyVersionID(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &gotBody)
		_, _ = io.WriteString(w, `{"asset_id":"asset-1","version_id":"ver-9","revision":1,"language":"ru","region":"RU","subject":"","body":"Текст."}`)
	}))
	defer server.Close()

	got, err := newClient(t, server).Version(context.Background(), contentversion.Query{AssetID: "asset-1", TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if _, present := gotBody["version_id"]; !present || gotBody["version_id"] != "" {
		t.Fatalf("version_id в теле = %+v", gotBody["version_id"])
	}
	if got.VersionID != "ver-9" || got.Subject != "" || got.Body != "Текст." {
		t.Fatalf("версия = %+v", got)
	}
}

// TestNotFound проверяет, что 404 владельца переводится в доменный исход
// ErrNotFound, а не в недоступность.
func TestNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"версия не найдена"}`, http.StatusNotFound)
	}))
	defer server.Close()

	if _, err := newClient(t, server).Version(context.Background(), contentversion.Query{AssetID: "asset-1", TenantID: "tenant-1"}); !errors.Is(err, contentversion.ErrNotFound) {
		t.Fatalf("ошибка = %v, ожидалась ErrNotFound", err)
	}
}

// TestErrorMapping проверяет перевод прочих HTTP-кодов владельца в недоступность:
// доменного исхода, кроме 404, у контракта нет.
func TestErrorMapping(t *testing.T) {
	cases := []struct {
		name   string
		status int
	}{
		{"400 отклонение", http.StatusBadRequest},
		{"409 конфликт", http.StatusConflict},
		{"500 сбой", http.StatusInternalServerError},
		{"503 недоступно", http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, `{"error":"отказ владельца"}`, tc.status)
			}))
			defer server.Close()

			if _, err := newClient(t, server).Version(context.Background(), contentversion.Query{AssetID: "asset-1", TenantID: "tenant-1"}); !errors.Is(err, contentversion.ErrUnavailable) {
				t.Fatalf("Version(%d) ошибка = %v, ожидалась ErrUnavailable", tc.status, err)
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

	if _, err := newClient(t, server).Version(context.Background(), contentversion.Query{AssetID: "asset-1", TenantID: "tenant-1"}); !errors.Is(err, contentversion.ErrUnavailable) {
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
	if _, err := client.Version(context.Background(), contentversion.Query{AssetID: "asset-1", TenantID: "tenant-1"}); !errors.Is(err, contentversion.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}

// TestNewRejectsInvalidBaseURL проверяет, что некорректный базовый адрес виден при
// сборке адаптера, а не при первом чтении версии.
func TestNewRejectsInvalidBaseURL(t *testing.T) {
	for _, base := range []string{"", "   ", "://", "content-production:8080", "ftp://host"} {
		if _, err := httpport.New(base); err == nil {
			t.Errorf("New(%q) ошибки не вернул", base)
		}
	}
}
