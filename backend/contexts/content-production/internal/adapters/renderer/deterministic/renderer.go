// Package deterministic — детерминированный адаптер порта рендеринга для среза.
//
// НАЗВАНИЕ ЧЕСТНОЕ: это заглушка, а не движок производства контента. Адаптер не
// обращается к внешнему движку (LLM, генератору презентаций и лендингов) и не
// содержит лингвистических моделей: он детерминированно раскладывает исходный
// текст версии на блоки по формату. Реальный движок за capability linguistics,
// presentation-generator и landing-page-generator подключается отдельным
// адаптером того же порта ports.Renderer без изменения домена и app.
//
// Адаптер ничего не выдумывает сверх входа: заголовок — первая строка, блоки —
// абзацы, пункты слайда — предложения. Поэтому результат воспроизводим и пригоден
// для тестов.
package deterministic

import (
	"context"
	"strings"

	"lead-gen/backend/contexts/content-production/internal/domain"
	"lead-gen/backend/contexts/content-production/internal/ports"
)

// Renderer — детерминированная реализация ports.Renderer.
type Renderer struct{}

// Renderer реализует порт рендеринга.
var _ ports.Renderer = Renderer{}

// New возвращает детерминированный адаптер рендеринга.
func New() Renderer { return Renderer{} }

// Render раскладывает текст версии на наполнение нужного формата. Пустой текст и
// неизвестный формат — отказ движка (ErrRenderRejected).
func (Renderer) Render(ctx context.Context, req ports.RenderRequest) (ports.RenderResult, error) {
	if err := ctx.Err(); err != nil {
		return ports.RenderResult{}, err
	}
	body := strings.TrimSpace(req.Body)
	if body == "" {
		return ports.RenderResult{}, ports.ErrRenderRejected
	}
	switch req.Format {
	case domain.RenderFormatLandingPage:
		return renderLanding(body)
	case domain.RenderFormatPresentation:
		return renderPresentation(body)
	case domain.RenderFormatDocument:
		return ports.RenderResult{Title: titleOf(body), Body: body}, nil
	default:
		return ports.RenderResult{}, ports.ErrRenderRejected
	}
}

// renderLanding строит наполнение лендинга: заголовок — первая строка, вводный
// абзац — первый содержательный абзац, блоки — содержательные абзацы текста.
func renderLanding(body string) (ports.RenderResult, error) {
	title, content := splitBody(body)
	if len(content) == 0 {
		return ports.RenderResult{}, ports.ErrRenderRejected
	}
	sections := make([]domain.LandingSection, 0, len(content))
	for _, paragraph := range content {
		section, err := domain.NewLandingSection(firstLine(paragraph), paragraph)
		if err != nil {
			return ports.RenderResult{}, ports.ErrRenderRejected
		}
		sections = append(sections, section)
	}
	return ports.RenderResult{
		Title:    title,
		Headline: title,
		Intro:    content[0],
		Sections: sections,
	}, nil
}

// renderPresentation строит презентацию: слайд на содержательный абзац, пункты
// слайда — предложения абзаца.
func renderPresentation(body string) (ports.RenderResult, error) {
	title, content := splitBody(body)
	if len(content) == 0 {
		return ports.RenderResult{}, ports.ErrRenderRejected
	}
	slides := make([]domain.Slide, 0, len(content))
	for _, paragraph := range content {
		bullets := sentences(paragraph)
		if len(bullets) == 0 {
			continue
		}
		slide, err := domain.NewSlide(firstLine(paragraph), bullets)
		if err != nil {
			return ports.RenderResult{}, ports.ErrRenderRejected
		}
		slides = append(slides, slide)
	}
	if len(slides) == 0 {
		return ports.RenderResult{}, ports.ErrRenderRejected
	}
	return ports.RenderResult{Title: title, Slides: slides}, nil
}

// splitBody отделяет заголовок (первую непустую строку) от содержательных
// абзацев. Абзац, совпадающий с заголовком, не дублируется в наполнении.
func splitBody(body string) (string, []string) {
	title := titleOf(body)
	content := paragraphs(body)
	if len(content) > 0 && firstLine(content[0]) == title {
		content = content[1:]
	}
	return title, content
}

// titleOf возвращает заголовок артефакта — первую непустую строку.
func titleOf(body string) string {
	line := firstLine(body)
	if line == "" {
		return "Контент"
	}
	return line
}

// firstLine возвращает первую непустую строку, обрезая служебные символы.
func firstLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if trimmed := strings.TrimSpace(strings.TrimLeft(line, "#-* \t")); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// paragraphs разбивает текст на абзацы по пустым строкам.
func paragraphs(body string) []string {
	raw := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n\n")
	out := make([]string, 0, len(raw))
	for _, paragraph := range raw {
		if trimmed := strings.TrimSpace(paragraph); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// sentences разбивает абзац на предложения по завершающей пунктуации. Разделители
// детерминированы: точка, восклицательный и вопросительный знаки с последующим
// пробелом.
func sentences(paragraph string) []string {
	normalized := strings.NewReplacer(". ", ".\n", "! ", "!\n", "? ", "?\n").Replace(paragraph)
	out := make([]string, 0)
	for _, sentence := range strings.Split(normalized, "\n") {
		if trimmed := strings.TrimSpace(sentence); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
