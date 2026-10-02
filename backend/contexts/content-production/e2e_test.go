package contentproduction

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lead-gen/backend/contexts/content-production/internal/app/command"
	"lead-gen/backend/contexts/content-production/internal/domain"
	"lead-gen/backend/contexts/content-production/internal/ports"
	"lead-gen/contracts/content-production/contentrequest"
	"lead-gen/contracts/content-production/contentversion"
	"lead-gen/contracts/content-production/renderedartifact"
)

const sampleBody = "Заголовок лендинга\n\nПервый абзац текста. Второе предложение.\n\nВторой абзац."

// buildSlice собирает срез с in-memory адаптерами по умолчанию.
func buildSlice(t *testing.T) *Slice {
	t.Helper()
	slice, err := Build(Config{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return slice
}

// processRender исполняет задание воркером до терминального состояния.
func processRender(t *testing.T, slice *Slice, jobID domain.RenderJobID) command.ProcessRenderResult {
	t.Helper()
	result, err := slice.ProcessRender.Handle(context.Background(), command.ProcessRender{RenderJobID: jobID})
	if err != nil {
		t.Fatalf("ProcessRender(%s): %v", jobID, err)
	}
	return result
}

func TestProduceAndRenderLandingEndToEnd(t *testing.T) {
	ctx := context.Background()
	slice := buildSlice(t)

	produced, err := slice.ProduceContent.Handle(ctx, command.ProduceContent{
		RequestID:  "req-1",
		AssetID:    "asset-1",
		TenantID:   "tenant-1",
		Language:   "ru",
		Region:     "RU",
		Format:     domain.RenderFormatLandingPage,
		TemplateID: "tmpl-1",
		Body:       sampleBody,
		Actor:      "actor-1",
	})
	if err != nil {
		t.Fatalf("ProduceContent: %v", err)
	}
	if produced.State != domain.RenderStateRequested {
		t.Fatalf("состояние задания: %s, ожидалось requested", produced.State)
	}

	rendered := processRender(t, slice, produced.RenderJobID)
	if rendered.State != domain.RenderStateRendered {
		t.Fatalf("состояние после рендера: %s", rendered.State)
	}

	// Контракт rendered-artifact отдаёт наполнение лендинга.
	page, err := NewRenderedArtifact(slice).Landing(ctx, renderedartifact.ArtifactRef("asset-1"))
	if err != nil {
		t.Fatalf("Landing: %v", err)
	}
	if page.Title == "" || len(page.Sections) == 0 {
		t.Fatalf("наполнение лендинга неполно: %+v", page)
	}

	// Контракт content-version отдаёт неизменяемую версию.
	version, err := NewContentVersion(slice).Version(ctx, contentversion.Query{AssetID: "asset-1", TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if version.Revision != 1 || version.Body != strings.TrimSpace(sampleBody) {
		t.Fatalf("версия неполна: %+v", version)
	}
	if version.Language != "ru" || version.Region != "RU" {
		t.Fatalf("локаль версии: %q-%q", version.Language, version.Region)
	}

	// Оба доменных события ушли в outbox.
	pending, err := slice.Journal.Pending(ctx, 0)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("событий в outbox: %d, ожидалось 2", len(pending))
	}
	if pending[0].RoutingKey != "content-version-created.v1" || pending[1].RoutingKey != "content-rendered.v1" {
		t.Fatalf("ключи маршрутизации: %q, %q", pending[0].RoutingKey, pending[1].RoutingKey)
	}
}

func TestPresentationRenderAndContract(t *testing.T) {
	ctx := context.Background()
	slice := buildSlice(t)

	if _, err := slice.CreateContentPackage.Handle(ctx, command.CreateContentPackage{
		AssetID:   "asset-2",
		TenantID:  "tenant-1",
		VersionID: "ver-1",
		Language:  "en",
		Body:      "Deck title\n\nFirst point. Second point.\n\nThird point.",
		Actor:     "actor-1",
	}); err != nil {
		t.Fatalf("CreateContentPackage: %v", err)
	}
	if _, err := slice.RequestRender.Handle(ctx, command.RequestRender{
		RenderJobID: "job-2",
		AssetID:     "asset-2",
		VersionID:   "ver-1",
		TenantID:    "tenant-1",
		TemplateID:  "deck",
		Format:      domain.RenderFormatPresentation,
	}); err != nil {
		t.Fatalf("RequestRender: %v", err)
	}
	rendered := processRender(t, slice, "job-2")
	if rendered.State != domain.RenderStateRendered {
		t.Fatalf("состояние после рендера: %s", rendered.State)
	}

	deck, err := NewRenderedArtifact(slice).Presentation(ctx, renderedartifact.ArtifactRef("job-2"))
	if err != nil {
		t.Fatalf("Presentation: %v", err)
	}
	if deck.Title == "" || len(deck.Slides) == 0 {
		t.Fatalf("презентация неполна: %+v", deck)
	}
	if len(deck.Slides[0].Bullets) == 0 {
		t.Fatalf("слайд без пунктов: %+v", deck.Slides[0])
	}
}

func TestProduceContentIsIdempotent(t *testing.T) {
	ctx := context.Background()
	slice := buildSlice(t)

	cmd := command.ProduceContent{
		RequestID:  "req-9",
		AssetID:    "asset-9",
		TenantID:   "tenant-1",
		Language:   "ru",
		Format:     domain.RenderFormatDocument,
		TemplateID: "doc",
		Body:       "Документ.\n\nТело документа.",
		Actor:      "actor-1",
	}
	first, err := slice.ProduceContent.Handle(ctx, cmd)
	if err != nil {
		t.Fatalf("первый ProduceContent: %v", err)
	}
	second, err := slice.ProduceContent.Handle(ctx, cmd)
	if err != nil {
		t.Fatalf("повтор ProduceContent: %v", err)
	}
	if first.RenderJobID != second.RenderJobID {
		t.Fatalf("повтор создал другое задание: %s ≠ %s", first.RenderJobID, second.RenderJobID)
	}
	// Повтор не публикует новую версию: событий ровно одно (первая версия).
	pending, _ := slice.Journal.Pending(ctx, 0)
	if len(pending) != 1 {
		t.Fatalf("повтор опубликовал лишние события: %d", len(pending))
	}
}

func TestRenderFailureIsRecorded(t *testing.T) {
	ctx := context.Background()
	slice, err := Build(Config{Renderer: failingRenderer{}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := slice.ProduceContent.Handle(ctx, command.ProduceContent{
		RequestID:  "req-fail",
		AssetID:    "asset-fail",
		TenantID:   "tenant-1",
		Language:   "ru",
		Format:     domain.RenderFormatLandingPage,
		TemplateID: "tmpl",
		Body:       "Текст.",
		Actor:      "actor-1",
	}); err != nil {
		t.Fatalf("ProduceContent: %v", err)
	}
	result := processRender(t, slice, "req-fail")
	if result.State != domain.RenderStateFailed || result.Reason == "" {
		t.Fatalf("отказ не зафиксирован: %+v", result)
	}
}

func TestHTTPServerHealthAndCreatePackage(t *testing.T) {
	slice := buildSlice(t)
	server := httptest.NewServer(slice.Server)
	defer server.Close()

	resp, err := http.Get(server.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("статус /healthz: %d", resp.StatusCode)
	}

	body := `{"asset_id":"asset-http","tenant_id":"tenant-1","version_id":"ver-1","language":"ru","body":"Текст.","actor":"actor-1"}`
	created, err := http.Post(server.URL+"/content/packages", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("POST /content/packages: %v", err)
	}
	defer created.Body.Close()
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("статус создания: %d", created.StatusCode)
	}
	var payload struct {
		AssetID   string `json:"asset_id"`
		VersionID string `json:"version_id"`
	}
	if err := json.NewDecoder(created.Body).Decode(&payload); err != nil {
		t.Fatalf("разбор ответа: %v", err)
	}
	if payload.AssetID != "asset-http" || payload.VersionID != "ver-1" {
		t.Fatalf("ответ неполон: %+v", payload)
	}
}

func TestContentRequestContractRejectsBadFormat(t *testing.T) {
	slice := buildSlice(t)
	_, err := NewContentRequest(slice).RequestContent(context.Background(), contentrequest.Intent{
		RequestID:  "req-bad",
		TenantID:   "tenant-1",
		AssetID:    "asset-bad",
		Format:     "nope",
		Language:   "ru",
		TemplateID: "tmpl",
		Body:       "Текст.",
		Actor:      "actor-1",
	})
	if !errors.Is(err, contentrequest.ErrRejected) {
		t.Fatalf("ожидалась ErrRejected, получено %v", err)
	}
}

// failingRenderer — тестовый движок, всегда отказывающий.
type failingRenderer struct{}

func (failingRenderer) Render(context.Context, ports.RenderRequest) (ports.RenderResult, error) {
	return ports.RenderResult{}, ports.ErrRenderUnavailable
}
