package components

import (
	"embed"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"strings"
)

// baseFS — общий каркас и фрагменты: layout (каркас страницы), notice
// (уведомление) и field (поле формы с ошибкой). Разметка встроена в бинарник,
// поэтому не может разойтись с кодом компонента.
//
//go:embed templates/*.html
var baseFS embed.FS

// staticFS — общее оформление экранов: таблицы стилей, разделяемые консолью,
// порталом и другими серверными экранами. Оформление лежит рядом с каркасом, а
// не в одном сервисе: разметка и стили — один слой, поэтому внешний вид экранов
// не расходится между сервисами.
//
//go:embed static/*
var staticFS embed.FS

// baseTemplates — файлы каркаса и общих фрагментов, подключаемые к каждой
// странице. Фрагменты переиспользуются страницами через {{ template }}.
// Вкладки, бейдж статуса и происхождение — тоже фрагменты полной страницы:
// они объявлены здесь, чтобы шаблон "content" консоли мог их вызвать, а
// функции Render* остались для гипермедиа-частичных ответов.
var baseTemplates = []string{
	"templates/layout.html",
	"templates/notice.html",
	"templates/field.html",
	"templates/tabs.html",
	"templates/status-badge.html",
	"templates/provenance.html",
	"templates/nav.html",
	"templates/live.html",
	"templates/verify-badge.html",
}

// LangRu — язык разметки по умолчанию: содержимое компонентов на русском.
const LangRu = "ru"

// ConsoleStylesheets — общие таблицы стилей экранов, подключаемые каркасом
// страницы. CSP экранов запрещает inline-стили, поэтому оформление — отдельный
// файл, который сервис раздаёт по /static/ из StaticFS.
var ConsoleStylesheets = []string{"/static/console.css"}

// ConsoleBootstrapScripts — общие скрипты, подключаемые синхронно в <head> до
// отрисовки страницы. theme.js применяет выбранную тему без вспышки и
// обслуживает переключатель темы из общего nav, поэтому должен выполниться
// раньше разметки.
var ConsoleBootstrapScripts = []string{"/static/theme.js"}

// ConsoleScripts — общие отложенные скрипты экранов консоли. console-a11y.js
// возвращает фокус при подмене фрагмента HTMX и объявляет изменения через
// область aria-live, поэтому подключается к каждому экрану, который использует
// гипермедиа-фрагменты. Имя не пересекается со скриптами экранов (у консоли
// lead-management есть свой console.js с другой ответственностью).
var ConsoleScripts = []string{"/static/console-a11y.js"}

// StaticFS отдаёт встроенное общее оформление. Корень — каталог static, поэтому
// монтируется на "GET /static/" со StripPrefix("/static/").
func StaticFS() fs.FS {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		// fs.Sub не падает на корректном embed: путь каталога проверен сборкой.
		panic(fmt.Sprintf("components: каталог оформления недоступен: %v", err))
	}
	return sub
}

// Layout — каркас страницы: язык документа, заголовок окна, внешние таблицы
// стилей, скрипты и скрипты-бутстрапы. Тело страницы задаёт шаблон "content"
// конкретной страницы.
type Layout struct {
	Lang        string
	Title       string
	Stylesheets []string
	Scripts     []string
	// BootstrapScripts — скрипты, подключаемые синхронно в <head> до отрисовки
	// страницы. Нужны для установки темы без вспышки. Обычные Scripts остаются
	// отложенными (defer) и на вспышку не влияют.
	BootstrapScripts []string
}

// Page — данные страницы: каркас, необязательная вертикальная навигация, модель
// экрана и необязательная внутриэкранная панель переходов раздела. Заголовок
// вынесен отдельно, потому что его видит и каркас, а модель — только шаблон самого
// экрана.
//
// Subnav — панель вторичных переходов (экраны одного раздела), выводимая в
// области содержимого, а не в общем сайдбаре: общий каркас остаётся одинаковым на
// всех консолях и не меняет высоту при переходах. nil — панели нет.
type Page struct {
	Layout  Layout
	Sidebar Nav
	Subnav  *Tabs
	View    any
}

// Notice — переиспользуемый фрагмент уведомления: отказ, ненайденная страница,
// подтверждение. Один компонент на все сообщения, требующие заголовка и текста.
type Notice struct {
	Heading string
	Message string
}

// NavItem — пункт вертикальной навигации (sidebar): подпись, адрес и флаг
// активности. Компонент не знает, куда ведут пункты и что за ними: это данные
// представления.
type NavItem struct {
	Label  string
	Href   string
	Active bool
}

// Nav — вертикальная навигация (sidebar) каркаса: пункты разделов консоли.
// Пустой список Items означает «страница без sidebar».
//
// Groups не выводится каркасом: вторичные переходы раздела живут во внутриэкранной
// панели (`Page.Subnav`), а не в общем сайдбаре — иначе сайдбар меняет состав и
// высоту при переходах между консолями. Поле сохранено для совместимости сборки
// потребителей и не влияет на разметку.
type Nav struct {
	Items  []NavItem
	Groups []NavGroup
}

// NavGroup — вторичная группа пунктов навигации. Не выводится каркасом (см. Nav).
// Вторичные переходы раздела — `Page.Subnav`.
type NavGroup struct {
	Label string
	Items []NavItem
}

// Field — поле формы: подпись, контрол и сообщение об ошибке. Ошибка приходит
// уже разобранной из представления: компонент не проверяет данные и не знает
// правил валидации — он лишь показывает то, что решил владелец.
//
// Type задаёт вид контрола: "" или "text" — однострочное поле, "textarea" —
// многострочное, "checkbox" — флажок. ID и Name — якорь подписи и имя поля в
// запросе; у флажка ID не требуется.
type Field struct {
	ID           string
	Name         string
	Label        string
	Type         string
	Value        string
	MaxLength    int
	Class        string
	Error        string
	Autocomplete string
	Rows         int
	Required     bool
	Checked      bool
}

// Set — разобранные страницы: каркас и общие фрагменты плюс шаблон "content"
// каждой страницы. Страницы разбираются по отдельности: несколько файлов
// объявляют "content", и в общем наборе они перекрыли бы друг друга.
type Set struct {
	pages    map[string]*template.Template
	messages *Messages
}

// New разбирает страницы из fs каталогом сообщений по умолчанию (ru).
func New(pages fs.FS, names ...string) (*Set, error) {
	return NewWithMessages(DefaultMessages(), pages, names...)
}

// NewWithMessages разбирает страницы из fs с заданным каталогом сообщений: для
// каждого имени файла страницы собирается отдельный набор «каркас + фрагменты +
// страница». Ошибка возвращается сразу — при сборке процесса, а не при первом
// запросе.
func NewWithMessages(messages *Messages, pages fs.FS, names ...string) (*Set, error) {
	set := &Set{pages: make(map[string]*template.Template, len(names)), messages: messages}
	for _, name := range names {
		parsed, err := template.New("layout").Funcs(templateFuncs(messages)).ParseFS(baseFS, baseTemplates...)
		if err != nil {
			return nil, fmt.Errorf("components: разбор каркаса: %w", err)
		}
		if parsed, err = parsed.ParseFS(pages, name); err != nil {
			return nil, fmt.Errorf("components: разбор страницы %s: %w", name, err)
		}
		set.pages[name] = parsed
	}
	return set, nil
}

// Render собирает страницу целиком в w. Потребитель передаёт буфер и только
// потом отдаёт ответ: частично отданный HTML на середине шаблона выглядел бы
// сломанной страницей вместо честного отказа.
func (s *Set) Render(w io.Writer, name string, page Page) error {
	parsed, ok := s.pages[name]
	if !ok {
		return fmt.Errorf("components: неизвестная страница %q", name)
	}
	if page.Layout.Lang == "" {
		page.Layout.Lang = LangRu
	}
	return parsed.ExecuteTemplate(w, "layout", page)
}

// RenderFragment исполняет именованный шаблон (фрагмент) набора страницы без
// каркаса layout. Нужен для гипермедиа-частичных ответов: вернуть только фрагмент
// (например, обновлённый журнал заданий), который HTMX подставит в уже открытую
// страницу. Фрагмент объявляется в том же файле страницы, что и "content".
func (s *Set) RenderFragment(w io.Writer, pageName, fragmentName string, data any) error {
	parsed, ok := s.pages[pageName]
	if !ok {
		return fmt.Errorf("components: неизвестная страница %q", pageName)
	}
	return parsed.ExecuteTemplate(w, fragmentName, data)
}

// FieldError — ошибка поля по имени, пустая строка при её отсутствии. Помогает
// разобрать карту ошибок валидации в модель представления.
func FieldError(errors map[string]string, name string) string {
	if errors == nil {
		return ""
	}
	return strings.TrimSpace(errors[name])
}

// templateFuncs собирает функции шаблонов общего слоя: доступ к каталогу
// сообщений (`text`, `label`) и локализованные форматы (`formatDate`, `formatDateTime`,
// `formatNumber`, `formatCurrency`, `formatTimeZone`). Благодаря этому шаблоны не
// содержат пользовательских строк и не форматируют значения сами.
func templateFuncs(messages *Messages) template.FuncMap {
	if messages == nil {
		messages = DefaultMessages()
	}
	formatter := NewFormatter(messages.Locale())
	return template.FuncMap{
		"text": func(key string) string {
			return messages.Text(MessageKey(key))
		},
		// label — читаемая подпись машинного кода по каталогу (см. Messages.Label).
		// Код принимается любого строкового типа (доменные статусы — именованные
		// строки), поэтому приводится через fmt.Sprint.
		"label": func(prefix string, code any) string {
			return messages.Label(prefix, fmt.Sprint(code))
		},
		"truncate":       TruncateText,
		"formatDate":     formatter.Date,
		"formatDateTime": formatter.DateTime,
		"formatNumber":   formatter.Number,
		"formatCurrency": formatter.Currency,
		"formatTimeZone": formatter.DateTimeInZone,
	}
}
