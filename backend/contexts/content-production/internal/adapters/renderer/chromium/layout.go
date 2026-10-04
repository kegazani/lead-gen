package chromium

import (
	"html"
	"strings"

	"lead-gen/backend/contexts/content-production/internal/domain"
	"lead-gen/backend/contexts/content-production/internal/ports"
)

// Раскладка версии контента в наполнение формата и сборка самодостаточного
// HTML-документа. Движок (headless Chromium) верстает именно этот документ,
// поэтому структура наполнения и физический артефакт происходят из одного
// источника, а не расходятся.

// landingLayout — наполнение лендинга, подготовленное к рендеру.
type landingLayout struct {
	title    string
	headline string
	intro    string
	sections []domain.LandingSection
}

func (l landingLayout) result() ports.RenderResult {
	return ports.RenderResult{
		Title:    l.title,
		Headline: l.headline,
		Intro:    l.intro,
		Sections: l.sections,
	}
}

// presentationLayout — наполнение презентации, подготовленное к рендеру.
type presentationLayout struct {
	title  string
	slides []domain.Slide
}

func (l presentationLayout) result() ports.RenderResult {
	return ports.RenderResult{Title: l.title, Slides: l.slides}
}

// documentLayout — наполнение документа, подготовленное к рендеру.
type documentLayout struct {
	title string
	body  string
}

func (l documentLayout) result() ports.RenderResult {
	return ports.RenderResult{Title: l.title, Body: l.body}
}

// layoutLanding строит наполнение лендинга: заголовок — первая строка, вводный
// абзац — первый содержательный абзац, блоки — содержательные абзацы текста.
func layoutLanding(body string) (landingLayout, error) {
	title, content := splitBody(body)
	if len(content) == 0 {
		return landingLayout{}, ports.ErrRenderRejected
	}
	sections := make([]domain.LandingSection, 0, len(content))
	for _, paragraph := range content {
		section, err := domain.NewLandingSection(firstLine(paragraph), paragraph)
		if err != nil {
			return landingLayout{}, ports.ErrRenderRejected
		}
		sections = append(sections, section)
	}
	return landingLayout{title: title, headline: title, intro: content[0], sections: sections}, nil
}

// layoutPresentation строит презентацию: слайд на содержательный абзац, пункты
// слайда — предложения абзаца.
func layoutPresentation(body string) (presentationLayout, error) {
	title, content := splitBody(body)
	if len(content) == 0 {
		return presentationLayout{}, ports.ErrRenderRejected
	}
	slides := make([]domain.Slide, 0, len(content))
	for _, paragraph := range content {
		bullets := sentences(paragraph)
		if len(bullets) == 0 {
			continue
		}
		slide, err := domain.NewSlide(firstLine(paragraph), bullets)
		if err != nil {
			return presentationLayout{}, ports.ErrRenderRejected
		}
		slides = append(slides, slide)
	}
	if len(slides) == 0 {
		return presentationLayout{}, ports.ErrRenderRejected
	}
	return presentationLayout{title: title, slides: slides}, nil
}

// layoutDocument строит документ: тело — исходный текст, заголовок — первая
// непустая строка.
func layoutDocument(body string) (documentLayout, error) {
	text := strings.TrimSpace(body)
	if text == "" {
		return documentLayout{}, ports.ErrRenderRejected
	}
	return documentLayout{title: titleOf(text), body: text}, nil
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

// composeLandingHTML собирает самодостаточный HTML-документ лендинга.
func composeLandingHTML(lang string, layout landingLayout) string {
	var b strings.Builder
	writeHead(&b, lang, layout.title, landingStyle)
	b.WriteString("<body class=\"landing\"><header class=\"hero\">")
	writeText(&b, "h1", firstNonEmpty(layout.headline, layout.title))
	if layout.intro != "" {
		writeText(&b, "p", layout.intro)
	}
	b.WriteString("</header><main>")
	for _, section := range layout.sections {
		b.WriteString("<section class=\"card\">")
		if heading := section.Heading(); heading != "" {
			writeText(&b, "h2", heading)
		}
		writeText(&b, "p", section.Body())
		b.WriteString("</section>")
	}
	b.WriteString("</main></body></html>")
	return b.String()
}

// composePresentationHTML собирает самодостаточный HTML-документ презентации:
// один слайд на страницу печати.
func composePresentationHTML(lang string, layout presentationLayout) string {
	var b strings.Builder
	writeHead(&b, lang, layout.title, presentationStyle)
	b.WriteString("<body class=\"deck\">")
	for _, slide := range layout.slides {
		b.WriteString("<section class=\"slide\">")
		if heading := slide.Heading(); heading != "" {
			writeText(&b, "h2", heading)
		}
		b.WriteString("<ul>")
		for _, bullet := range slide.Bullets() {
			writeText(&b, "li", bullet)
		}
		b.WriteString("</ul></section>")
	}
	b.WriteString("</body></html>")
	return b.String()
}

// composeDocumentHTML собирает самодостаточный HTML-документ документа.
func composeDocumentHTML(lang string, layout documentLayout) string {
	var b strings.Builder
	writeHead(&b, lang, layout.title, documentStyle)
	b.WriteString("<body class=\"document\"><article>")
	writeText(&b, "h1", layout.title)
	for _, paragraph := range paragraphs(layout.body) {
		writeText(&b, "p", paragraph)
	}
	b.WriteString("</article></body></html>")
	return b.String()
}

// writeHead пишет начальные теги документа, встроенный стиль и заголовок окна.
func writeHead(b *strings.Builder, lang, title, style string) {
	b.WriteString("<!doctype html><html lang=\"")
	b.WriteString(html.EscapeString(lang))
	b.WriteString("\"><head><meta charset=\"utf-8\"><title>")
	b.WriteString(html.EscapeString(title))
	b.WriteString("</title><style>")
	b.WriteString(style)
	b.WriteString("</style></head>")
}

// writeText пишет тег с экранированным текстовым содержимым.
func writeText(b *strings.Builder, tag, text string) {
	b.WriteString("<")
	b.WriteString(tag)
	b.WriteString(">")
	b.WriteString(html.EscapeString(text))
	b.WriteString("</")
	b.WriteString(tag)
	b.WriteString(">")
}

// firstNonEmpty возвращает первую непустую строку.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// Общие стили самодостаточны: без внешних ресурсов и шрифтов, чтобы рендер не
// зависел от сети.
const baseStyle = "html,body{margin:0;padding:0}" +
	"body{font-family:-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;color:#1a1a1a;line-height:1.5}" +
	"h1,h2{line-height:1.2}"

const documentStyle = baseStyle +
	"@page{size:A4;margin:2cm}" +
	"article{max-width:100%}" +
	"article h1{font-size:24pt;margin:0 0 16pt}" +
	"article p{font-size:11pt;margin:0 0 12pt}"

const landingStyle = baseStyle +
	"@page{size:A4;margin:1.5cm}" +
	".landing .hero{background:#f4f6fb;padding:32pt;margin-bottom:24pt}" +
	".landing .hero h1{font-size:28pt;margin:0 0 12pt}" +
	".landing .hero p{font-size:12pt;margin:0}" +
	".landing .card{margin:0 0 18pt;padding:16pt;border:1px solid #e2e6ef;border-radius:6pt}" +
	".landing .card h2{font-size:16pt;margin:0 0 8pt}" +
	".landing .card p{font-size:11pt;margin:0}"

const presentationStyle = baseStyle +
	"@page{size:13.333in 7.5in;margin:0}" +
	".deck .slide{box-sizing:border-box;width:13.333in;height:7.5in;padding:0.8in 1in;page-break-after:always;break-after:page}" +
	".deck .slide:last-child{page-break-after:auto;break-after:auto}" +
	".deck h2{font-size:30pt;margin:0 0 24pt}" +
	".deck ul{margin:0;padding-left:24pt}" +
	".deck li{font-size:18pt;margin:0 0 12pt}"
