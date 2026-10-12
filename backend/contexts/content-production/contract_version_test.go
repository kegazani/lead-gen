package contentproduction

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/content-production/internal/app/command"
	"lead-gen/backend/contexts/content-production/internal/domain"
	"lead-gen/contracts/content-production/contentrequest"
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

func TestVersionOnlyPreservesExactTextWithoutRenderAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	slice := buildSlice(t)
	subject := ""
	intent := contentrequest.Intent{RequestID: "editorial-version-1", AssetID: "asset-editorial", TenantID: "tenant-1", Language: "ru", Actor: "actor-1", Mode: contentrequest.ModeVersionOnly, Subject: &subject, Body: "  Точный текст\nбез изменений.  "}
	created, err := NewContentRequest(slice).RequestContent(ctx, intent)
	if err != nil {
		t.Fatalf("RequestContent: %v", err)
	}
	if created.RenderJobID != "" {
		t.Fatalf("version-only создал render job: %+v", created)
	}
	if jobs, err := slice.RenderJobs.ListPending(ctx, 10); err != nil || len(jobs) != 0 {
		t.Fatalf("ожидалась пустая render queue, jobs=%d err=%v", len(jobs), err)
	}
	version, err := NewContentVersion(slice).Version(ctx, contentversion.Query{AssetID: intent.AssetID, TenantID: intent.TenantID, VersionID: created.VersionID})
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if version.Subject != subject || version.Body != intent.Body {
		t.Fatalf("текст версии изменён: subject=%q body=%q", version.Subject, version.Body)
	}
	replayed, err := NewContentRequest(slice).RequestContent(ctx, intent)
	if err != nil || replayed.VersionID != created.VersionID {
		t.Fatalf("повтор: result=%+v err=%v", replayed, err)
	}
	intent.Body += " изменено"
	if _, err := NewContentRequest(slice).RequestContent(ctx, intent); !errors.Is(err, contentrequest.ErrRejected) {
		t.Fatalf("изменённый повтор должен конфликтовать, получено %v", err)
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
