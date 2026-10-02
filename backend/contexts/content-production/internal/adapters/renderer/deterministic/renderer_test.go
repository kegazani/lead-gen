package deterministic

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/content-production/internal/domain"
	"lead-gen/backend/contexts/content-production/internal/ports"
)

func req(format domain.RenderFormat, body string) ports.RenderRequest {
	return ports.RenderRequest{
		AssetID:    "asset-1",
		VersionID:  "ver-1",
		TemplateID: "tmpl-1",
		Format:     format,
		Body:       body,
	}
}

func TestRenderLandingIsDeterministic(t *testing.T) {
	renderer := New()
	body := "Заголовок\n\nПервый абзац. Ещё предложение.\n\nВторой абзац."
	first, err := renderer.Render(context.Background(), req(domain.RenderFormatLandingPage, body))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	second, err := renderer.Render(context.Background(), req(domain.RenderFormatLandingPage, body))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if first.Title != second.Title || len(first.Sections) != len(second.Sections) {
		t.Fatal("рендер недетерминирован")
	}
	if first.Title != "Заголовок" || len(first.Sections) != 2 {
		t.Fatalf("наполнение лендинга неполно: %+v", first)
	}
}

func TestRenderPresentationProducesSlides(t *testing.T) {
	result, err := New().Render(context.Background(), req(domain.RenderFormatPresentation, "Заголовок\n\nПервый пункт. Второй пункт."))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if len(result.Slides) != 1 || len(result.Slides[0].Bullets()) != 2 {
		t.Fatalf("слайды неполны: %+v", result.Slides)
	}
}

func TestRenderDocumentReturnsBody(t *testing.T) {
	result, err := New().Render(context.Background(), req(domain.RenderFormatDocument, "Документ.\n\nТело."))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if result.Title != "Документ." || result.Body == "" {
		t.Fatalf("документ неполон: %+v", result)
	}
}

func TestRenderRejectsEmptyBodyAndUnknownFormat(t *testing.T) {
	if _, err := New().Render(context.Background(), req(domain.RenderFormatLandingPage, "   ")); !errors.Is(err, ports.ErrRenderRejected) {
		t.Fatalf("пустой текст: %v", err)
	}
	if _, err := New().Render(context.Background(), req(domain.RenderFormat("nope"), "текст")); !errors.Is(err, ports.ErrRenderRejected) {
		t.Fatalf("неизвестный формат: %v", err)
	}
}
