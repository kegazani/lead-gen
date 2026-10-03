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

// Page — данные страницы: каркас, необязательная вертикальная навигация и модель
// экрана. Заголовок вынесен отдельно, потому что его видит и каркас, а модель —
// только шаблон самого экрана.
type Page struct {
	Layout  Layout
	Sidebar Nav
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

// Nav — вертикальная навигация (sidebar) каркаса. Пустой список Items означает
// «страница без sidebar».
type Nav struct {
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
	pages map[string]*template.Template
}

// New разбирает страницы из fs: для каждого имени файла страницы собирается
// отдельный набор «каркас + фрагменты + страница». Ошибка возвращается сразу —
// при сборке процесса, а не при первом запросе.
func New(pages fs.FS, names ...string) (*Set, error) {
	set := &Set{pages: make(map[string]*template.Template, len(names))}
	for _, name := range names {
		parsed, err := template.New("layout").ParseFS(baseFS, baseTemplates...)
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
