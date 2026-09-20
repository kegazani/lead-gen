package web

import (
	"bytes"
	"embed"
	"net/http"

	"lead-gen/frontend/components"
	"lead-gen/frontend/public/internal/ports"
)

// Встроенные ресурсы edge: шаблоны страниц и оформление. Общий каркас страницы
// и фрагменты (layout, notice, field) живут в модуле frontend/components; здесь
// остаются только шаблоны "content" конкретных экранов edge. Внешних файлов у
// edge нет — образ самодостаточен, а разметка не может разойтись с бинарником.
var (
	//go:embed templates/*.html
	templateFS embed.FS
	//go:embed static/*
	staticFS embed.FS
)

// edgeStylesheets — внешние таблицы стилей edge, подключаемые каркасом
// страницы. Оформление — отдельный файл: CSP запрещает inline-стили.
var edgeStylesheets = []string{"/static/edge.css"}

// Имена страниц. Каждый файл объявляет шаблон "content"; каркас "layout" и
// общие фрагменты приходят из frontend/components. Шаблон разбирается на каждую
// страницу отдельно: несколько файлов объявляют "content", и в общем наборе они
// бы перекрыли друг друга.
const (
	pageLanding      = "templates/landing.html"
	pagePresentation = "templates/presentation.html"
	pageForm         = "templates/form.html"
	pageNotice       = "templates/notice.html"
)

// pageNames — все страницы edge.
var pageNames = []string{pageLanding, pagePresentation, pageForm, pageNotice}

// parsePages разбирает страницы edge вместе с общим каркасом и фрагментами.
// Ошибка разбора возвращается сразу — при сборке процесса, а не при запросе
// посетителя.
func parsePages() (*components.Set, error) {
	return components.New(templateFS, pageNames...)
}

// landingView — модель страницы лендинга.
type landingView struct {
	Page ports.LandingPage
}

// presentationView — модель страницы презентации.
type presentationView struct {
	Page ports.Presentation
}

// formView — модель страницы формы. Поля собраны компонентами: подпись,
// контрол и ошибка — одна единица разметки. Ошибки валидации разобраны в
// представление заранее (formFields), а не в шаблоне.
type formView struct {
	// Action — адрес приёма формы.
	Action string
	// Sent — заявка уже принята; страница показывает подтверждение вместо формы.
	Sent bool
	// SentNotice — уведомление об успешной отправке.
	SentNotice components.Notice
	// Failure — общая причина отказа, если ошибка не относится к одному полю.
	Failure string
	// Fields — поля формы с подписями, значениями и ошибками.
	Fields []components.Field
}

// formValues — введённые значения формы в том виде, в каком их возвращает
// разметка.
type formValues struct {
	Name    string
	Contact string
	Message string
	Consent bool
}

// pageView собирает данные страницы edge: каркас с заголовком и общим оформлением
// плюс модель конкретного экрана.
func pageView(title string, view any) components.Page {
	return components.Page{
		Layout: components.Layout{Title: title, Stylesheets: edgeStylesheets},
		View:   view,
	}
}

// render отдаёт страницу. Разметка собирается в буфер целиком и только потом
// уходит в ответ: частично отданный HTML на середине шаблона выглядел бы как
// сломанная страница вместо честного отказа.
func (h *handler) render(w http.ResponseWriter, status int, name string, data components.Page) {
	var buffer bytes.Buffer
	if err := h.pages.Render(&buffer, name, data); err != nil {
		h.log.Error("web: рендеринг страницы", "page", name, "error", err)
		http.Error(w, "внутренняя ошибка", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := buffer.WriteTo(w); err != nil {
		h.log.Warn("web: страница не доставлена", "page", name, "error", err)
	}
}

// renderNotice отдаёт страницу-уведомление. Наружу идёт текст, пригодный
// посетителю, а не формулировка владельца или внутренняя ошибка.
func (h *handler) renderNotice(w http.ResponseWriter, status int, heading, message string) {
	h.render(w, status, pageNotice, pageView(heading, components.Notice{Heading: heading, Message: message}))
}
