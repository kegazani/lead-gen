package httpapi_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	contentproduction "lead-gen/backend/contexts/content-production"
	httpapi "lead-gen/backend/contexts/content-production/internal/adapters/http"
	"lead-gen/backend/contexts/content-production/internal/app/command"
	"lead-gen/backend/contexts/content-production/internal/domain"
	"lead-gen/contracts/content-production/contentversion"
	"lead-gen/contracts/content-production/contentversion/httpport"
)

// newContentVersionClient собирает сетевой клиент контракта content-version к
// тестовому серверу владельца (round-trip).
func newContentVersionClient(t *testing.T, ts *httptest.Server) *httpport.Client {
	t.Helper()
	client, err := httpport.New(ts.URL, httpport.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("httpport.New: %v", err)
	}
	return client
}

// buildContentSlice собирает срез content-production с in-memory адаптерами.
func buildContentSlice(t *testing.T) *contentproduction.Slice {
	t.Helper()
	slice, err := contentproduction.Build(contentproduction.Config{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return slice
}

// TestContentVersionHTTPRoundTrip проверяет сквозной путь контракта: сетевой
// клиент → POST /content-version → сценарий чтения → ответ, читаемый клиентом.
// Материал версии отрендерен, поэтому тема письма непуста, а тело — не выдумано.
func TestContentVersionHTTPRoundTrip(t *testing.T) {
	ctx := context.Background()
	slice := buildContentSlice(t)

	produced, err := slice.ProduceContent.Handle(ctx, command.ProduceContent{
		RequestID:  "req-cv",
		AssetID:    "asset-cv",
		TenantID:   "tenant-1",
		Language:   "ru",
		Region:     "RU",
		Format:     domain.RenderFormatDocument,
		TemplateID: "doc",
		Body:       "Тема письма\n\nТекст письма.",
		Actor:      "actor-1",
	})
	if err != nil {
		t.Fatalf("ProduceContent: %v", err)
	}
	if _, err := slice.ProcessRender.Handle(ctx, command.ProcessRender{RenderJobID: produced.RenderJobID}); err != nil {
		t.Fatalf("ProcessRender: %v", err)
	}

	ts := httptest.NewServer(slice.Server)
	defer ts.Close()

	version, err := newContentVersionClient(t, ts).Version(ctx, contentversion.Query{AssetID: "asset-cv", TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if version.Subject != "Тема письма" {
		t.Fatalf("Subject: получено %q, ожидалась тема материала", version.Subject)
	}
	if version.Body == "" {
		t.Fatalf("тело материала пусто: %+v", version)
	}
	if version.Revision != 1 {
		t.Fatalf("Revision: получено %d, ожидалось 1", version.Revision)
	}
	if version.Language != "ru" || version.Region != "RU" {
		t.Fatalf("локаль версии: %q-%q", version.Language, version.Region)
	}
}

// TestContentVersionHTTPNotFound проверяет честный доменный исход: и отсутствующая
// версия существующего пакета, и отсутствующий пакет переводятся в ErrNotFound.
func TestContentVersionHTTPNotFound(t *testing.T) {
	ctx := context.Background()
	slice := buildContentSlice(t)

	if _, err := slice.CreateContentPackage.Handle(ctx, command.CreateContentPackage{
		AssetID:   "asset-nf",
		TenantID:  "tenant-1",
		VersionID: "ver-1",
		Language:  "ru",
		Body:      "Текст без материала.",
		Actor:     "actor-1",
	}); err != nil {
		t.Fatalf("CreateContentPackage: %v", err)
	}

	ts := httptest.NewServer(slice.Server)
	defer ts.Close()
	client := newContentVersionClient(t, ts)

	if _, err := client.Version(ctx, contentversion.Query{AssetID: "asset-nf", VersionID: "ver-missing", TenantID: "tenant-1"}); !errors.Is(err, contentversion.ErrNotFound) {
		t.Fatalf("несуществующая версия: ошибка = %v, ожидалась ErrNotFound", err)
	}
	if _, err := client.Version(ctx, contentversion.Query{AssetID: "asset-absent", TenantID: "tenant-1"}); !errors.Is(err, contentversion.ErrNotFound) {
		t.Fatalf("несуществующий пакет: ошибка = %v, ожидалась ErrNotFound", err)
	}
}

// TestContentVersionHTTPUnavailable проверяет, что незамонтированный сценарий
// чтения честно отвечает недоступностью владельца, а не выдуманной версией.
func TestContentVersionHTTPUnavailable(t *testing.T) {
	ts := httptest.NewServer(httpapi.NewServer(httpapi.Dependencies{}))
	defer ts.Close()

	if _, err := newContentVersionClient(t, ts).Version(context.Background(), contentversion.Query{AssetID: "asset-1", TenantID: "tenant-1"}); !errors.Is(err, contentversion.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}
