// Package chromium — реальный адаптер порта рендеринга поверх headless Chromium
// по Chrome DevTools Protocol (CDP, библиотека chromedp).
//
// Адаптер заменяет заглушку renderer/deterministic на продуктовом пути: он
// обращается к внешнему движку — headless Chromium — и производит физический
// артефакт (PDF для документа и презентации, PNG-превью страницы для лендинга).
// Детерминированная раскладка текста здесь не выдумывается: движок сам верстает
// HTML-документ и рендерит его браузером. Поэтому адаптер назван честно и
// ошибка внешней системы не выдаётся за успех.
//
// Физический артефакт (байты PDF/PNG) отдаётся методами RenderPDF и RenderPNG.
// Хранилище объектов вне границ этого среза, поэтому порт ports.Renderer
// возвращает структурированное наполнение, которое моделирует домен, а сами
// байты остаются доступны вызывающему напрямую.
package chromium

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"

	"lead-gen/backend/contexts/content-production/internal/domain"
	"lead-gen/backend/contexts/content-production/internal/ports"
)

const (
	// defaultTimeout — таймаут одной операции рендера, если он не задан.
	defaultTimeout = 30 * time.Second

	// defaultViewportWidth — ширина окна рендера лендинга (CSS-пиксели), если она
	// не задана. PDF использует размеры страницы из CSS (@page).
	defaultViewportWidth = 1280

	// defaultViewportHeight — высота окна рендера (CSS-пиксели) до снятия
	// полностраничного снимка.
	defaultViewportHeight = 800
)

// pngMagic — сигнатура PNG; результат рендера обязан её начинать.
var pngMagic = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}

// Config — настройки реального рендера Chromium.
type Config struct {
	// ExecPath — путь к исполняемому Chromium/Chrome. Абсолютный путь или имя
	// программы, находимое в PATH. Используется, когда не задан WSURL.
	ExecPath string

	// WSURL — адрес CDP-эндпоинта уже запущенного браузера (ws://host:port/...).
	// Задан — адаптер подключается к нему, а не запускает свой процесс.
	WSURL string

	// Timeout — таймаут одной операции рендера. Не более нуля — defaultTimeout.
	Timeout time.Duration

	// ViewportWidth — ширина окна рендера лендинга в CSS-пикселях. Ноль —
	// defaultViewportWidth.
	ViewportWidth int64
}

// Renderer — реализация ports.Renderer поверх headless Chromium по CDP.
//
// Экземпляр потокобезопасен: операции сериализуются, браузер запускается лениво
// при первой операции и переиспользуется до Close. Ошибка запуска браузера не
// подменяется фиктивным документом: она нормализуется в ports.ErrRenderUnavailable.
type Renderer struct {
	allocCtx  context.Context
	stopAlloc context.CancelFunc
	timeout   time.Duration
	width     int64

	mu      sync.Mutex
	tabCtx  context.Context
	stopTab context.CancelFunc
}

// Renderer реализует порт рендеринга.
var _ ports.Renderer = (*Renderer)(nil)

// New собирает адаптер реального рендера. Пустое значение и ExecPath, и WSURL —
// ошибка конфигурации: без внешнего движка рендер не настраивается. Браузер в
// момент сборки не запускается: запуск и его отказ проявляются при первой
// операции рендера.
func New(cfg Config) (*Renderer, error) {
	execPath := strings.TrimSpace(cfg.ExecPath)
	wsURL := strings.TrimSpace(cfg.WSURL)
	if execPath == "" && wsURL == "" {
		return nil, errors.New("chromium: не задан ни ExecPath, ни WSURL — движок рендера не сконфигурирован")
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	width := cfg.ViewportWidth
	if width <= 0 {
		width = defaultViewportWidth
	}

	var (
		allocCtx  context.Context
		stopAlloc context.CancelFunc
	)
	if wsURL != "" {
		allocCtx, stopAlloc = chromedp.NewRemoteAllocator(context.Background(), wsURL)
	} else {
		// Стандартные опции chromedp (headless, изоляция профиля) плюс явный путь к
		// браузеру. PDF не байт-идентичен между запусками (Chromium пишет даты
		// создания/печати); байтовая воспроизводимость не требуется.
		opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(execPath))
		allocCtx, stopAlloc = chromedp.NewExecAllocator(context.Background(), opts...)
	}

	return &Renderer{
		allocCtx:  allocCtx,
		stopAlloc: stopAlloc,
		timeout:   timeout,
		width:     width,
	}, nil
}

// Close останавливает браузер и освобождает ресурсы. Повторный вызов безопасен.
func (r *Renderer) Close() error {
	r.mu.Lock()
	stopTab := r.stopTab
	r.tabCtx = nil
	r.stopTab = nil
	r.mu.Unlock()

	if stopTab != nil {
		stopTab()
	}
	if r.stopAlloc != nil {
		r.stopAlloc()
		r.stopAlloc = nil
	}
	return nil
}

// Render реализует порт рендеринга: раскладывает версию контента в наполнение
// формата и заставляет реальный Chromium произвести физический артефакт
// (документ и презентация — PDF, лендинг — PNG-превью). Недоступность браузера —
// честный отказ ports.ErrRenderUnavailable, непригодный текст —
// ports.ErrRenderRejected.
func (r *Renderer) Render(ctx context.Context, req ports.RenderRequest) (ports.RenderResult, error) {
	if err := ctx.Err(); err != nil {
		return ports.RenderResult{}, err
	}
	body := strings.TrimSpace(req.Body)
	if body == "" {
		return ports.RenderResult{}, ports.ErrRenderRejected
	}
	lang := localeLang(req.Locale)

	switch req.Format {
	case domain.RenderFormatLandingPage:
		layout, err := layoutLanding(body)
		if err != nil {
			return ports.RenderResult{}, err
		}
		if _, err := r.RenderPNG(ctx, composeLandingHTML(lang, layout)); err != nil {
			return ports.RenderResult{}, err
		}
		return layout.result(), nil
	case domain.RenderFormatPresentation:
		layout, err := layoutPresentation(body)
		if err != nil {
			return ports.RenderResult{}, err
		}
		if _, err := r.RenderPDF(ctx, composePresentationHTML(lang, layout)); err != nil {
			return ports.RenderResult{}, err
		}
		return layout.result(), nil
	case domain.RenderFormatDocument:
		layout, err := layoutDocument(body)
		if err != nil {
			return ports.RenderResult{}, err
		}
		if _, err := r.RenderPDF(ctx, composeDocumentHTML(lang, layout)); err != nil {
			return ports.RenderResult{}, err
		}
		return layout.result(), nil
	default:
		return ports.RenderResult{}, ports.ErrRenderRejected
	}
}

// ReadinessCheck открывает или проверяет CDP-сессию командой Browser.getVersion.
// Проверка ограничена контекстом и не генерирует документ, файл или бизнес-эффект.
func (r *Renderer) ReadinessCheck(ctx context.Context) error {
	checkCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	_, err := run(r, checkCtx, func(ctx context.Context, target *chromedp.Target) (struct{}, error) {
		_, err := cdp.Call(ctx, target, browser.GetVersion, cdp.Empty{})
		return struct{}{}, err
	})
	if err != nil {
		return fmt.Errorf("%w: проверка CDP", ports.ErrRenderUnavailable)
	}
	return nil
}

// RenderPDF рендерит самодостаточный HTML-документ в PDF через headless
// Chromium. Пустой HTML — отказ по качеству входа; недоступность браузера или
// не-PDF результат — честная недоступность движка.
func (r *Renderer) RenderPDF(ctx context.Context, htmlDocument string) ([]byte, error) {
	if strings.TrimSpace(htmlDocument) == "" {
		return nil, fmt.Errorf("%w: пустой HTML", ports.ErrRenderRejected)
	}
	data, err := r.render(ctx, printToPDF(htmlDocument), "PDF")
	if err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		return nil, fmt.Errorf("%w: результат рендера не является PDF", ports.ErrRenderUnavailable)
	}
	return data, nil
}

// RenderPNG рендерит самодостаточный HTML-документ в PNG-снимок всей страницы
// через headless Chromium. Пустой HTML — отказ по качеству входа; недоступность
// браузера или не-PNG результат — честная недоступность движка.
func (r *Renderer) RenderPNG(ctx context.Context, htmlDocument string) ([]byte, error) {
	if strings.TrimSpace(htmlDocument) == "" {
		return nil, fmt.Errorf("%w: пустой HTML", ports.ErrRenderRejected)
	}
	data, err := r.render(ctx, capturePNG(htmlDocument, r.width), "PNG")
	if err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(data, pngMagic) {
		return nil, fmt.Errorf("%w: результат рендера не является PNG", ports.ErrRenderUnavailable)
	}
	return data, nil
}

// render выполняет действие Chromium, уважая отмену вызывающего контекста.
// Собственный таймаут операции и ошибки внешнего движка нормализуются в
// ports.ErrRenderUnavailable; отмена вызывающего контекста возвращается как есть.
func (r *Renderer) render(ctx context.Context, action chromedp.Action[[]byte], kind string) ([]byte, error) {
	data, err := run(r, ctx, action)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("%w: рендер %s: %v", ports.ErrRenderUnavailable, kind, err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: рендер %s вернул пустой результат", ports.ErrRenderUnavailable, kind)
	}
	return data, nil
}

// session возвращает контекст вкладки, при первом вызове лениво запуская браузер.
// Прогрев выполняется без таймаута: таймаут на первой операции остановил бы сам
// браузер, поэтому дедлайн ставится только на рабочие операции.
func (r *Renderer) session(ctx context.Context) (context.Context, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tabCtx != nil {
		return r.tabCtx, nil
	}
	tabCtx, stopTab := chromedp.NewContext(r.allocCtx)
	stopOnCancel := context.AfterFunc(ctx, stopTab)
	_, err := chromedp.Run(tabCtx, warmUp)
	stopOnCancel()
	if err != nil {
		stopTab()
		return nil, fmt.Errorf("запуск браузера: %w", err)
	}
	if err := ctx.Err(); err != nil {
		stopTab()
		return nil, err
	}
	r.tabCtx = tabCtx
	r.stopTab = stopTab
	return tabCtx, nil
}

// run выполняет одно действие на вкладке с таймаутом операции.
func run[T any](r *Renderer, ctx context.Context, action chromedp.Action[T]) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	tabCtx, err := r.session(ctx)
	if err != nil {
		return zero, err
	}
	opCtx, cancel := context.WithTimeout(tabCtx, r.timeout)
	stopOnCancel := context.AfterFunc(ctx, cancel)
	defer func() {
		stopOnCancel()
		cancel()
	}()
	return chromedp.Run(opCtx, action)
}

// warmUp — пустое действие: первая Run на контексте вкладки запускает браузер.
func warmUp(context.Context, *chromedp.Target) (chromedp.Void, error) {
	return chromedp.Void{}, nil
}

// printToPDF — действие печати самодостаточного HTML в PDF.
func printToPDF(htmlDocument string) chromedp.Action[[]byte] {
	return func(ctx context.Context, t *chromedp.Target) ([]byte, error) {
		if err := setContent(ctx, t, htmlDocument); err != nil {
			return nil, err
		}
		result, err := cdp.Call(ctx, t, page.PrintToPDF, page.PrintToPDFParams{
			PrintBackground: new(true),
		})
		if err != nil {
			return nil, err
		}
		return result.Data, nil
	}
}

// capturePNG — действие полностраничного снимка самодостаточного HTML в PNG.
func capturePNG(htmlDocument string, width int64) chromedp.Action[[]byte] {
	return func(ctx context.Context, t *chromedp.Target) ([]byte, error) {
		if _, err := cdp.Call(ctx, t, emulation.SetDeviceMetricsOverride, emulation.SetDeviceMetricsOverrideParams{
			Width:             width,
			Height:            defaultViewportHeight,
			DeviceScaleFactor: 1,
			Mobile:            false,
		}); err != nil {
			return nil, err
		}
		if err := setContent(ctx, t, htmlDocument); err != nil {
			return nil, err
		}
		result, err := cdp.Call(ctx, t, page.CaptureScreenshot, page.CaptureScreenshotParams{
			Format:                page.CaptureScreenshotFormatPng,
			CaptureBeyondViewport: new(true),
		})
		if err != nil {
			return nil, err
		}
		return result.Data, nil
	}
}

// setContent устанавливает HTML как содержимое главного фрейма вкладки.
func setContent(ctx context.Context, t *chromedp.Target, htmlDocument string) error {
	tree, err := cdp.Call(ctx, t, page.GetFrameTree, cdp.Empty{})
	if err != nil {
		return err
	}
	if tree.FrameTree == nil || tree.FrameTree.Frame == nil {
		return errors.New("главный фрейм страницы недоступен")
	}
	_, err = cdp.Call(ctx, t, page.SetDocumentContent, page.SetDocumentContentParams{
		FrameID: tree.FrameTree.Frame.ID,
		HTML:    htmlDocument,
	})
	return err
}

// localeLang возвращает код языка локали для атрибута lang итогового документа.
func localeLang(locale domain.ContentLocale) string {
	if locale.IsZero() {
		return "ru"
	}
	return locale.Language()
}
