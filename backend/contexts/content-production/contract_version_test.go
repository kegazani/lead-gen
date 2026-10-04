package contentproduction

import (
	"context"
	"testing"

	"lead-gen/backend/contexts/content-production/internal/app/command"
	"lead-gen/backend/contexts/content-production/internal/domain"
	"lead-gen/contracts/content-production/contentversion"
)

// TestNewContentVersionReturnsMaterialSubject проверяет, что контракт
// content-version отдаёт тему материала письма — заголовок материала, привязанного
// к запрошенной версии. Материал документа порождается рендером; его заголовок —
// первая строка текста версии.
func TestNewContentVersionReturnsMaterialSubject(t *testing.T) {
	ctx := context.Background()
	slice := buildSlice(t)

	produced, err := slice.ProduceContent.Handle(ctx, command.ProduceContent{
		RequestID:  "req-subject",
		AssetID:    "asset-subject",
		TenantID:   "tenant-1",
		Language:   "ru",
		Format:     domain.RenderFormatDocument,
		TemplateID: "doc",
		Body:       "Тема письма\n\nТекст письма.",
		Actor:      "actor-1",
	})
	if err != nil {
		t.Fatalf("ProduceContent: %v", err)
	}
	if rendered := processRender(t, slice, produced.RenderJobID); rendered.State != domain.RenderStateRendered {
		t.Fatalf("состояние после рендера: %s", rendered.State)
	}

	version, err := NewContentVersion(slice).Version(ctx, contentversion.Query{AssetID: "asset-subject", TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if version.Subject != "Тема письма" {
		t.Fatalf("Subject: получено %q, ожидалась тема материала", version.Subject)
	}
	if version.Body == "" {
		t.Fatalf("тело материала пусто: %+v", version)
	}
}

// TestNewContentVersionEmptySubjectWithoutMaterial проверяет честную пустую тему:
// у версии без материала с заголовком тема не выдумывается (AGENTS.md §3).
func TestNewContentVersionEmptySubjectWithoutMaterial(t *testing.T) {
	ctx := context.Background()
	slice := buildSlice(t)

	if _, err := slice.CreateContentPackage.Handle(ctx, command.CreateContentPackage{
		AssetID:   "asset-plain",
		TenantID:  "tenant-1",
		VersionID: "ver-1",
		Language:  "ru",
		Body:      "Текст без материала.",
		Actor:     "actor-1",
	}); err != nil {
		t.Fatalf("CreateContentPackage: %v", err)
	}

	version, err := NewContentVersion(slice).Version(ctx, contentversion.Query{AssetID: "asset-plain", TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if version.Subject != "" {
		t.Fatalf("Subject: получено %q, ожидалась пустая строка", version.Subject)
	}
}
