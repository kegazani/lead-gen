package chromium

import (
	"bytes"
	"context"
	"errors"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lead-gen/backend/contexts/content-production/internal/domain"
	"lead-gen/backend/contexts/content-production/internal/ports"
)

// chromiumExecutable находит исполняемый Chromium в окружении. Если браузера
// нет — тест пропускается честно: пропуск не выдаётся за успешный рендер.
func chromiumExecutable(t *testing.T) string {
	t.Helper()
	candidates := make([]string, 0, 8)
	if fromEnv := strings.TrimSpace(os.Getenv("LEADGEN_CHROMIUM_PATH")); fromEnv != "" {
		candidates = append(candidates, fromEnv)
	}
	candidates = append(candidates,
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
	)
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable", "chrome"} {
		if path, err := exec.LookPath(name); err == nil {
			candidates = append(candidates, path)
		}
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	t.Skip("Chromium недоступен: задайте LEADGEN_CHROMIUM_PATH или установите Chrome/Chromium")
	return ""
}

// newRenderer собирает реальный рендер на найденном Chromium.
func newRenderer(t *testing.T) *Renderer {
	t.Helper()
	renderer, err := New(Config{ExecPath: chromiumExecutable(t), Timeout: 30 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		if err := renderer.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return renderer
}

func portRequest(format domain.RenderFormat, body string) ports.RenderRequest {
	return ports.RenderRequest{
		AssetID:    "asset-1",
		VersionID:  "ver-1",
		TemplateID: "tmpl-1",
		Format:     format,
		Body:       body,
	}
}

// TestRenderPDFProducesRealPDF проверяет реальный HTML → PDF: движок возвращает
// непустой документ с сигнатурой PDF.
func TestRenderPDFProducesRealPDF(t *testing.T) {
	renderer := newRenderer(t)
	if err := renderer.ReadinessCheck(context.Background()); err != nil {
		t.Fatalf("ReadinessCheck: %v", err)
	}
	htmlDocument := composeDocumentHTML("ru", documentLayout{title: "Отчёт", body: "Отчёт\n\nПервый абзац."})

	pdf, err := renderer.RenderPDF(context.Background(), htmlDocument)
	if err != nil {
		t.Fatalf("RenderPDF: %v", err)
	}
	if len(pdf) < 100 {
		t.Fatalf("PDF подозрительно мал: %d байт", len(pdf))
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Fatalf("результат не начинается с %%PDF-: %q", pdf[:min(8, len(pdf))])
	}
	t.Logf("реальный HTML → PDF: %d байт", len(pdf))
}

func TestReadinessCheckHonorsCallerDeadline(t *testing.T) {
	renderer, err := New(Config{ExecPath: "chromium-not-installed-anywhere", Timeout: time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer renderer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	err = renderer.ReadinessCheck(ctx)
	if !errors.Is(err, ports.ErrRenderUnavailable) {
		t.Fatalf("ожидалась честная недоступность Chromium, получено %v", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("проверка readiness не уважает отмену контекста: %s", elapsed)
	}
}

// TestRenderPNGProducesRealImage проверяет реальный HTML → PNG: движок
// возвращает декодируемое изображение ненулевого размера.
func TestRenderPNGProducesRealImage(t *testing.T) {
	renderer := newRenderer(t)
	htmlDocument := composeLandingHTML("ru", landingLayout{
		title:    "Лендинг",
		headline: "Лендинг",
		intro:    "Вводный абзац.",
		sections: mustSections(t, "Первый блок"),
	})

	image, err := renderer.RenderPNG(context.Background(), htmlDocument)
	if err != nil {
		t.Fatalf("RenderPNG: %v", err)
	}
	if !bytes.HasPrefix(image, pngMagic) {
		t.Fatalf("результат не начинается с сигнатуры PNG")
	}
	decoded, err := png.Decode(bytes.NewReader(image))
	if err != nil {
		t.Fatalf("декодирование PNG: %v", err)
	}
	if bounds := decoded.Bounds(); bounds.Dx() <= 0 || bounds.Dy() <= 0 {
		t.Fatalf("пустое изображение: %v", bounds)
	}
	t.Logf("реальный HTML → PNG: %d байт, %dx%d", len(image), decoded.Bounds().Dx(), decoded.Bounds().Dy())
}

// TestRenderImplementsPortForAllFormats проверяет порт рендеринга на реальном
// движке: для каждого формата артефакт собирается и наполнение непусто.
func TestRenderImplementsPortForAllFormats(t *testing.T) {
	renderer := newRenderer(t)

	landing, err := renderer.Render(context.Background(), portRequest(domain.RenderFormatLandingPage,
		"Заголовок лендинга\n\nПервый абзац. Ещё предложение.\n\nВторой абзац."))
	if err != nil {
		t.Fatalf("Render(landing): %v", err)
	}
	if landing.Headline == "" || len(landing.Sections) != 2 {
		t.Fatalf("наполнение лендинга неполно: %+v", landing)
	}

	deck, err := renderer.Render(context.Background(), portRequest(domain.RenderFormatPresentation,
		"Заголовок\n\nПервый пункт. Второй пункт."))
	if err != nil {
		t.Fatalf("Render(presentation): %v", err)
	}
	if deck.Title == "" || len(deck.Slides) != 1 || len(deck.Slides[0].Bullets()) != 2 {
		t.Fatalf("презентация неполна: %+v", deck)
	}

	document, err := renderer.Render(context.Background(), portRequest(domain.RenderFormatDocument,
		"Документ.\n\nТело документа."))
	if err != nil {
		t.Fatalf("Render(document): %v", err)
	}
	if document.Title == "" || document.Body == "" {
		t.Fatalf("документ неполон: %+v", document)
	}
}

// TestRenderRejectsBadInputBeforeBrowser проверяет отказ по качеству входа до
// запуска браузера: пустое тело и неизвестный формат отвергаются.
func TestRenderRejectsBadInputBeforeBrowser(t *testing.T) {
	renderer, err := New(Config{ExecPath: "chromium-not-installed-anywhere"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer renderer.Close()

	if _, err := renderer.Render(context.Background(), portRequest(domain.RenderFormatLandingPage, "   ")); !errors.Is(err, ports.ErrRenderRejected) {
		t.Fatalf("пустое тело: %v", err)
	}
	if _, err := renderer.Render(context.Background(), portRequest(domain.RenderFormat("nope"), "текст")); !errors.Is(err, ports.ErrRenderRejected) {
		t.Fatalf("неизвестный формат: %v", err)
	}
}

// TestRenderUnavailableWithoutBrowser проверяет честный нормализованный отказ,
// когда браузера нет: движок недоступен, а не выдумывает документ.
func TestRenderUnavailableWithoutBrowser(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-chromium")
	renderer, err := New(Config{ExecPath: missing, Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer renderer.Close()

	if _, err := renderer.RenderPDF(context.Background(), "<html><body>текст</body></html>"); !errors.Is(err, ports.ErrRenderUnavailable) {
		t.Fatalf("ожидалась недоступность движка, получено %v", err)
	}
}

// TestRenderUnavailableWithBadWSURL проверяет честный отказ и при
// WSURL-конфигурации: подключение к недоступному CDP-эндпоинту не выдаётся за
// успех.
func TestRenderUnavailableWithBadWSURL(t *testing.T) {
	renderer, err := New(Config{
		WSURL:   "ws://127.0.0.1:1/devtools/browser/00000000-0000-0000-0000-000000000000",
		Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer renderer.Close()

	if _, err := renderer.RenderPNG(context.Background(), "<html><body>текст</body></html>"); !errors.Is(err, ports.ErrRenderUnavailable) {
		t.Fatalf("ожидалась недоступность движка, получено %v", err)
	}
}

// TestNewRequiresEngineConfig проверяет, что без ExecPath и WSURL конфигурация
// отвергается: рендер не собирается «на пустом месте».
func TestNewRequiresEngineConfig(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("ожидалась ошибка конфигурации без ExecPath и WSURL")
	}
}

func mustSections(t *testing.T, paragraphs ...string) []domain.LandingSection {
	t.Helper()
	sections := make([]domain.LandingSection, 0, len(paragraphs))
	for i, paragraph := range paragraphs {
		section, err := domain.NewLandingSection("", paragraph)
		if err != nil {
			t.Fatalf("NewLandingSection(%d): %v", i, err)
		}
		sections = append(sections, section)
	}
	return sections
}
