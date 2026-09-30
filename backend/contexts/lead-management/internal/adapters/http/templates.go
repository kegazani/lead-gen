package httpapi

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"

	"lead-gen/frontend/components"
)

// Встроенные ресурсы консоли: шаблоны страниц и оформление. Каркас страницы и
// общие фрагменты (layout, notice, field, tabs, status-badge, provenance, nav)
// живут в модуле frontend/components; здесь остаются только шаблоны "content"
// экранов консоли. Внешних файлов у консоли нет — разметка не может разойтись с
// бинарником.
var (
	//go:embed templates/*.html
	templateFS embed.FS

	//go:embed static/*
	staticFS embed.FS
)

// consoleStylesheets — внешние таблицы стилей консоли, подключаемые каркасом
// страницы. Оформление — отдельный файл: CSP консоли запрещает inline-стили.
var consoleStylesheets = []string{"/static/console.css"}

// consoleScripts — внешние скрипты консоли. HTMX даёт гипермедиа-частичные
// обновления (живой журнал заданий без перезагрузки), console.js сохраняет выбор
// заданий и переносит лида перетаскиванием, assistant.js выводит ответ ассистента
// потоком; скрипты — отдельные файлы, так как CSP разрешает скрипты только с того
// же источника.
var consoleScripts = []string{"/static/htmx.min.js", "/static/console.js", "/static/assistant.js"}

// consoleBootstrapScripts — скрипты, подключаемые синхронно в <head> до
// отрисовки страницы. theme.js ставит выбранную тему без вспышки, поэтому он не
// может быть отложенным.
var consoleBootstrapScripts = []string{"/static/theme.js"}

// Имена страниц консоли. Каждый файл объявляет шаблон "content"; каркас "layout"
// и общие фрагменты приходят из frontend/components.
const (
	pageLogin        = "templates/login.html"
	pageDashboard    = "templates/dashboard.html"
	pageJob          = "templates/job.html"
	pageCards        = "templates/cards.html"
	pageLeads        = "templates/leads.html"
	pageLeadsExport  = "templates/leads-export.html"
	pageCard         = "templates/card.html"
	pageFunnel       = "templates/funnel.html"
	pageSend         = "templates/send.html"
	pageTemplates    = "templates/templates.html"
	pageAssistant    = "templates/assistant.html"
	pageForumSignals = "templates/forum-signals.html"
	pageNotice       = "templates/notice.html"
)

// Общие фрагменты библиотеки шаблонов. Они объявлены в шаблоне
// templates/templates.html и переиспользуются всеми экранами: список и поиск
// встраиваются в /send и карточку лида, поэтому разметка списка не дублируется.
const (
	fragmentTemplateList   = "template-list"
	fragmentTemplatePicker = "template-picker"
	fragmentEmailPreview   = "email-preview"
	fragmentValueFields    = "variable-values"
)

// parsePages разбирает страницы консоли вместе с каркасом и фрагментами. Ошибка
// разбора возвращается сразу — при сборке процесса, а не при первом запросе.
func parsePages() (*components.Set, error) {
	return components.New(
		templateFS,
		pageLogin,
		pageDashboard,
		pageJob,
		pageCards,
		pageLeads,
		pageCard,
		pageFunnel,
		pageSend,
		pageTemplates,
		pageAssistant,
		pageForumSignals,
		pageNotice,
	)
}

// parseLeadsExport разбирает автономный шаблон выгрузки лидов: полный HTML-документ
// со встроенными стилями, без каркаса и статики консоли. Файл передают отдельно от
// сервиса, поэтому он не должен ссылаться на /static.
func parseLeadsExport() (*template.Template, error) {
	return template.New("export-root").ParseFS(templateFS, pageLeadsExport)
}

// staticFiles раздаёт встроенные статические файлы консоли. Отдельный каталог, а
// не раздача исходников: наружу уходит только оформление.
func staticFiles() (http.Handler, error) {
	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, err
	}
	return http.FileServerFS(static), nil
}
