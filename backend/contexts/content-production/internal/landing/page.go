// Package landing собирает публикуемую страницу лендинга из наполнения:
// самодостаточный HTML-документ с инлайн-CSS и архив HTML/CSS для размещения на
// хостинге клиента (capability landing-page-generator, задачи 6.1 и 7.1–7.5).
//
// Страница не обращается к внешним ресурсам: стили встроены, шрифты — системные,
// изображений и скриптов нет. Разметка адаптивна (viewport и media-запрос), а
// метаданные (title, description, lang) заполнены. Значения из материалов
// клиента экранируются: они данные, а не разметка.
package landing

import (
	"archive/zip"
	"bytes"
	"fmt"
	"html"
	"strings"

	"lead-gen/backend/contexts/content-production/internal/domain"
)

// neutralPrimary и neutralAccent — нейтральные цвета оформления, применяемые,
// когда бренд-комплект не предоставлен.
const (
	neutralPrimary = "#334155"
	neutralAccent  = "#0f172a"
	neutralFont    = "-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif"
)

// Options — необязательные составляющие страницы: оформление, реквизиты
// компании и форма заявки. Нулевые значения опускают соответствующие блоки.
type Options struct {
	// Description — метаописание страницы.
	Description string
	// Brand — бренд-комплект клиента; нулевой — нейтральное оформление.
	Brand domain.BrandKit
	// Company — реквизиты и контакты компании; нулевой — блок контактов опущен.
	Company domain.CompanyProfile
	// Form — форма заявки; нулевая — блок заявки опущен.
	Form domain.LeadForm
}

// Page — собранная страница лендинга.
type Page struct {
	content domain.LandingContent
	lang    string
	options Options
}

// New собирает страницу из наполнения. Пустой язык заменяется на "ru".
func New(content domain.LandingContent, lang string, options Options) Page {
	normalized := strings.TrimSpace(lang)
	if normalized == "" {
		normalized = "ru"
	}
	return Page{content: content, lang: normalized, options: options}
}

// Title возвращает заголовок страницы (title и главный заголовок).
func (p Page) Title() string { return p.content.Title() }

// Description возвращает метаописание страницы.
func (p Page) Description() string { return strings.TrimSpace(p.options.Description) }

// Language возвращает язык страницы для атрибута lang.
func (p Page) Language() string { return p.lang }

// StyleSheet возвращает CSS страницы. Внешних ресурсов CSS не требует.
func (p Page) StyleSheet() string {
	brand := p.options.Brand
	primary := brand.PrimaryColor()
	if primary == "" {
		primary = neutralPrimary
	}
	accent := brand.AccentColor()
	if accent == "" {
		accent = neutralAccent
	}
	font := brand.FontFamily()
	if font == "" {
		font = neutralFont
	}
	var b strings.Builder
	b.WriteString(":root{")
	b.WriteString("--primary:")
	b.WriteString(primary)
	b.WriteString(";--accent:")
	b.WriteString(accent)
	b.WriteString(";--font:")
	b.WriteString(font)
	b.WriteString("}")
	b.WriteString(styleSheet)
	return b.String()
}

// Document возвращает самодостаточный HTML-документ с инлайн-стилями: его можно
// открыть локально без доступа к сети.
func (p Page) Document() string {
	var b strings.Builder
	p.writeHead(&b, "")
	p.writeBody(&b)
	b.WriteString("</body></html>")
	return b.String()
}

// Archive возвращает архив размещения: index.html со ссылкой на styles.css и
// сам styles.css. Распакованный архив открывается локально без доработок.
func (p Page) Archive() ([]byte, error) {
	var index strings.Builder
	p.writeHead(&index, "styles.css")
	p.writeBody(&index)
	index.WriteString("</body></html>")

	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	if err := writeZipFile(writer, "index.html", index.String()); err != nil {
		return nil, err
	}
	if err := writeZipFile(writer, "styles.css", p.StyleSheet()); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("landing: закрытие архива: %w", err)
	}
	return buf.Bytes(), nil
}

// writeHead пишет начальные теги документа. Непустой styleHref подключает
// внешний файл стилей (архив размещения); пустой — встраивает CSS инлайн.
func (p Page) writeHead(b *strings.Builder, styleHref string) {
	b.WriteString("<!doctype html><html lang=\"")
	b.WriteString(html.EscapeString(p.lang))
	b.WriteString("\"><head><meta charset=\"utf-8\">")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">")
	b.WriteString("<title>")
	b.WriteString(html.EscapeString(p.Title()))
	b.WriteString("</title>")
	if description := p.Description(); description != "" {
		b.WriteString("<meta name=\"description\" content=\"")
		b.WriteString(html.EscapeString(description))
		b.WriteString("\">")
	}
	if styleHref != "" {
		b.WriteString("<link rel=\"stylesheet\" href=\"")
		b.WriteString(html.EscapeString(styleHref))
		b.WriteString("\">")
	} else {
		b.WriteString("<style>")
		b.WriteString(p.StyleSheet())
		b.WriteString("</style>")
	}
	b.WriteString("</head><body class=\"landing\">")
}

// writeBody пишет содержимое страницы: шапку, блоки наполнения, контакты и форму.
func (p Page) writeBody(b *strings.Builder) {
	content := p.content
	b.WriteString("<header class=\"hero\"><div class=\"wrap\">")
	if logo := p.options.Brand.LogoText(); logo != "" {
		writeText(b, "p", "logo", logo)
	}
	writeText(b, "h1", "headline", firstNonEmpty(content.Headline(), content.Title()))
	if intro := content.Intro(); intro != "" {
		writeText(b, "p", "intro", intro)
	}
	if !p.options.Form.IsZero() {
		b.WriteString("<a class=\"cta\" href=\"#request\">Оставить заявку</a>")
	}
	b.WriteString("</div></header>")

	b.WriteString("<main class=\"wrap\">")
	for _, section := range content.Sections() {
		b.WriteString("<section class=\"card\">")
		if heading := section.Heading(); heading != "" {
			writeText(b, "h2", "", heading)
		}
		writeText(b, "p", "", section.Body())
		b.WriteString("</section>")
	}
	b.WriteString("</main>")

	p.writeCompany(b)
	p.writeForm(b)
}

// writeCompany пишет блок реквизитов и общих контактов. Персональные контакты и
// имена сотрудников в наполнение не попадают.
func (p Page) writeCompany(b *strings.Builder) {
	company := p.options.Company
	if company.IsZero() {
		return
	}
	b.WriteString("<section class=\"contacts wrap\"><h2>Контакты</h2>")
	writeText(b, "p", "company", company.Name())
	if address := company.Address(); address != "" {
		writeText(b, "p", "address", address)
	}
	if contacts := company.GeneralContacts(); len(contacts) > 0 {
		b.WriteString("<ul class=\"contact-list\">")
		for _, contact := range contacts {
			writeText(b, "li", string(contact.Kind()), contact.Value())
		}
		b.WriteString("</ul>")
	}
	b.WriteString("</section>")
}

// writeForm пишет форму заявки минимального состава. Данные уходят в приёмник
// клиента (action); платформа заявку не сохраняет.
func (p Page) writeForm(b *strings.Builder) {
	form := p.options.Form
	if form.IsZero() {
		return
	}
	b.WriteString("<section id=\"request\" class=\"request wrap\"><h2>Оставить заявку</h2>")
	b.WriteString("<form method=\"post\" action=\"")
	b.WriteString(html.EscapeString(form.Action()))
	b.WriteString("\">")
	fields := form.Fields()
	soleContact := soleContactField(fields)
	for _, field := range fields {
		p.writeField(b, field, soleContact)
	}
	b.WriteString("<button type=\"submit\">Отправить</button>")
	b.WriteString("</form></section>")
}

// writeField пишет одно поле формы. Имя и единственный контакт обязательны —
// без них заявку нельзя связать с заявителем.
func (p Page) writeField(b *strings.Builder, field domain.LeadFormField, soleContact domain.LeadFormField) {
	name := string(field)
	b.WriteString("<label class=\"field\"><span>")
	b.WriteString(html.EscapeString(field.Label()))
	b.WriteString("</span>")
	required := field == domain.LeadFormFieldName || field == soleContact
	switch field {
	case domain.LeadFormFieldMessage:
		b.WriteString("<textarea name=\"")
		b.WriteString(name)
		b.WriteString("\" rows=\"4\"></textarea>")
	default:
		b.WriteString("<input type=\"")
		b.WriteString(inputType(field))
		b.WriteString("\" name=\"")
		b.WriteString(name)
		b.WriteString("\"")
		if required {
			b.WriteString(" required")
		}
		b.WriteString(">")
	}
	b.WriteString("</label>")
}

// inputType возвращает тип input для поля формы.
func inputType(field domain.LeadFormField) string {
	switch field {
	case domain.LeadFormFieldEmail:
		return "email"
	case domain.LeadFormFieldPhone:
		return "tel"
	default:
		return "text"
	}
}

// soleContactField возвращает единственное контактное поле формы (почту или
// телефон), если оно только одно. Такое поле обязательно.
func soleContactField(fields []domain.LeadFormField) domain.LeadFormField {
	contact := domain.LeadFormField("")
	count := 0
	for _, field := range fields {
		if field == domain.LeadFormFieldEmail || field == domain.LeadFormFieldPhone {
			contact = field
			count++
		}
	}
	if count == 1 {
		return contact
	}
	return ""
}

// writeText пишет тег с экранированным текстом и необязательным классом.
func writeText(b *strings.Builder, tag, class, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	b.WriteString("<")
	b.WriteString(tag)
	if class != "" {
		b.WriteString(" class=\"")
		b.WriteString(html.EscapeString(class))
		b.WriteString("\"")
	}
	b.WriteString(">")
	b.WriteString(html.EscapeString(text))
	b.WriteString("</")
	b.WriteString(tag)
	b.WriteString(">")
}

// writeZipFile добавляет в архив один файл.
func writeZipFile(writer *zip.Writer, name, content string) error {
	file, err := writer.Create(name)
	if err != nil {
		return fmt.Errorf("landing: файл архива %q: %w", name, err)
	}
	if _, err := file.Write([]byte(content)); err != nil {
		return fmt.Errorf("landing: запись файла архива %q: %w", name, err)
	}
	return nil
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

// styleSheet — адаптивное нейтральное оформление. Внешних ресурсов нет;
// мобильная ширина не даёт горизонтальной прокрутки.
const styleSheet = "*{box-sizing:border-box}" +
	"html,body{margin:0;padding:0}" +
	"body{font-family:var(--font);color:#111827;background:#fff;line-height:1.6}" +
	".wrap{width:100%;max-width:960px;margin:0 auto;padding:0 1rem}" +
	".hero{background:var(--primary);color:#fff;padding:3.5rem 0}" +
	".hero .logo{margin:0 0 .5rem;font-size:.95rem;letter-spacing:.08em;text-transform:uppercase;opacity:.85}" +
	".hero h1{margin:0 0 1rem;font-size:2.2rem;line-height:1.2}" +
	".hero .intro{margin:0 0 1.5rem;font-size:1.1rem;max-width:42rem}" +
	".hero .cta{display:inline-block;background:#fff;color:var(--accent);text-decoration:none;padding:.7rem 1.4rem;border-radius:.4rem;font-weight:600}" +
	"main{padding:2rem 0}" +
	".card{border:1px solid #e5e7eb;border-radius:.6rem;padding:1.2rem 1.4rem;margin:0 0 1rem}" +
	".card h2{margin:0 0 .6rem;font-size:1.3rem}" +
	".card p{margin:0}" +
	".contacts{padding:1.5rem 0 2.5rem}" +
	".contact-list{list-style:none;margin:.5rem 0 0;padding:0}" +
	".contact-list li{margin:0 0 .3rem}" +
	".request{padding:1.5rem 0 3rem}" +
	".request form{display:flex;flex-direction:column;gap:.8rem;max-width:32rem}" +
	".field{display:flex;flex-direction:column;gap:.3rem;font-size:.95rem}" +
	".field input,.field textarea{font:inherit;padding:.6rem .7rem;border:1px solid #cbd5e1;border-radius:.4rem;width:100%}" +
	".request button{align-self:flex-start;background:var(--primary);color:#fff;border:0;border-radius:.4rem;padding:.7rem 1.6rem;font:inherit;font-weight:600;cursor:pointer}" +
	"@media (max-width:600px){" +
	".hero{padding:2.2rem 0}" +
	".hero h1{font-size:1.6rem}" +
	".hero .intro{font-size:1rem}" +
	"}"
