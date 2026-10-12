package httpapi

import (
	"embed"
	"errors"
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
// страницы. Оформление общее для серверных экранов и живёт в frontend/components
// (CSP консоли запрещает inline-стили); здесь — только ссылка на него и
// собственное расширение состава карточек консоли лидов (static/cards.css).
var consoleStylesheets = append(append([]string{}, components.ConsoleStylesheets...), "/static/cards.css")

// consoleScripts — внешние скрипты консоли. HTMX даёт гипермедиа-частичные
// обновления (живой журнал заданий без перезагрузки), console.js сохраняет выбор
// заданий и переносит лида перетаскиванием, assistant.js выводит ответ ассистента
// потоком; скрипты — отдельные файлы, так как CSP разрешает скрипты только с того
// же источника.
var consoleScripts = append([]string{"/static/htmx.min.js", "/static/console.js", "/static/assistant.js"}, components.ConsoleScripts...)

// consoleBootstrapScripts — скрипты, подключаемые синхронно в <head> до
// отрисовки страницы. theme.js ставит выбранную тему без вспышки, поэтому он не
// может быть отложенным. Скрипт общий для серверных экранов и лежит в
// frontend/components.
var consoleBootstrapScripts = components.ConsoleBootstrapScripts

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
	return components.NewWithMessages(consoleMessages(),
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
	return template.New("export-root").Funcs(exportTextFuncs()).ParseFS(templateFS, pageLeadsExport)
}

// exportTextFuncs повторяет функцию text общего слоя для автономного шаблона выгрузки:
// файл разбирается отдельно от каркаса страниц, поэтому каталог сообщений нужно
// подключить явно. Текст выгрузки также живёт в каталоге.
func exportTextFuncs() template.FuncMap {
	return template.FuncMap{
		"text": func(key string) string {
			return consoleMessages().Text(components.MessageKey(key))
		},
	}
}

// staticFiles раздаёт встроенные статические файлы консоли вместе с общим
// оформлением компонентов одним деревом: адрес /static/* не зависит от того,
// чей файл отдаётся — общий console.css/theme.js или скрипты самой консоли.
func staticFiles() (http.Handler, error) {
	local, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, err
	}
	return http.FileServerFS(staticLayers{layers: []fs.FS{components.StaticFS(), local}}), nil
}

// staticLayers объединяет несколько встроенных каталогов в одно дерево: имя
// ищется по слоям по порядку, первый найденный файл побеждает. Отсутствие файла
// во всех слоях — несуществующий путь, а не ошибка чтения.
type staticLayers struct{ layers []fs.FS }

func (s staticLayers) Open(name string) (fs.File, error) {
	for _, layer := range s.layers {
		file, err := layer.Open(name)
		if err == nil {
			return file, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}
