package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"lead-gen/frontend/components"
	"lead-gen/frontend/public/internal/ports"
)

// refPattern ограничивает ссылку в пути URL безопасным алфавитом. Ссылка
// непрозрачна для edge, но приходит из внешнего мира, поэтому она не должна
// ломать разметку, адрес действия формы или заголовок Location.
var refPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{1,64}$`)

// handleRootLanding отдаёт лендинг по корню домена, если он назначен в
// конфигурации. Корень не «главная страница платформы»: edge обслуживает
// страницы владельцев, и выбирать среди них главную он не вправе.
func (h *handler) handleRootLanding(w http.ResponseWriter, r *http.Request) {
	if h.config.DefaultLanding == "" {
		h.renderNotice(w, http.StatusNotFound, "Страница не найдена",
			"По этому адресу публичная страница не опубликована.")
		return
	}
	h.serveLanding(w, r, h.config.DefaultLanding)
}

// handleLanding отдаёт сгенерированную страницу лендинга.
func (h *handler) handleLanding(w http.ResponseWriter, r *http.Request) {
	ref, ok := h.requireRef(w, r)
	if !ok {
		return
	}
	h.serveLanding(w, r, ref)
}

// serveLanding запрашивает наполнение у владельца и рендерит страницу.
func (h *handler) serveLanding(w http.ResponseWriter, r *http.Request, ref ports.PageRef) {
	page, err := h.config.Landings.Landing(r.Context(), ref)
	if err != nil {
		h.renderSourceError(w, err, "лендинг")
		return
	}
	if page.Title == "" {
		page.Title = "Лендинг"
	}
	h.render(w, http.StatusOK, pageLanding, pageView(page.Title, landingView{Page: page}))
}

// handlePresentation отдаёт страницу просмотра презентации.
func (h *handler) handlePresentation(w http.ResponseWriter, r *http.Request) {
	ref, ok := h.requireRef(w, r)
	if !ok {
		return
	}
	page, err := h.config.Presentations.Presentation(r.Context(), ref)
	if err != nil {
		h.renderSourceError(w, err, "презентация")
		return
	}
	if page.Title == "" {
		page.Title = "Презентация"
	}
	h.render(w, http.StatusOK, pagePresentation, pageView(page.Title, presentationView{Page: page}))
}

// handleForm отдаёт форму обратной связи. Форма принадлежит edge: поля и
// проверки — часть публичной страницы, а не доменное правило. Владельцу уходит
// готовая заявка, а не разметка.
//
// Проверяется только форма ссылки, а не существование формы: реестра форм у
// edge нет — он не владеет ими, — поэтому ссылка формы работает как метка
// маршрута заявки, а решает о ней приёмник владельца. Приёмник, не знающий
// формы, отвечает ErrNotFound, и тогда посетитель получает 404, а не форму,
// которая навсегда останется неотправляемой.
func (h *handler) handleForm(w http.ResponseWriter, r *http.Request) {
	ref, ok := h.requireRef(w, r)
	if !ok {
		return
	}
	view := formView{Action: formAction(ref), Fields: formFields(formValues{}, nil)}
	if r.URL.Query().Get("sent") == "1" {
		view.Sent = true
		view.SentNotice = components.Notice{
			Heading: "Заявка отправлена",
			Message: "Спасибо. Мы передали заявку и свяжемся с вами.",
		}
	}
	h.renderForm(w, http.StatusOK, view)
}

// handleFormSubmit принимает заявку: проверяет поля, передаёт заявку приёмнику
// и отвечает редиректом (POST/redirect/GET), чтобы обновление страницы не
// отправило заявку второй раз.
func (h *handler) handleFormSubmit(w http.ResponseWriter, r *http.Request) {
	ref, ok := h.requireRef(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.log.Warn("web: форма не разобрана", "form", string(ref), "error", err)
		h.renderNotice(w, http.StatusBadRequest, "Форма не принята",
			"Не удалось разобрать отправленные данные. Обновите страницу и попробуйте снова.")
		return
	}

	submission := h.submissionFrom(r, ref)
	if problems := validateSubmission(submission); len(problems) > 0 {
		h.renderForm(w, http.StatusUnprocessableEntity, formView{
			Action:  formAction(ref),
			Fields:  formFields(valuesOf(submission), problems),
			Failure: "Проверьте поля формы.",
		})
		return
	}

	if err := h.config.Submissions.Submit(r.Context(), submission); err != nil {
		if ports.IsNotFound(err) {
			// Приёмник не знает такой формы: посетитель пришёл по устаревшей
			// ссылке, и отправлять ему форму заново нечего.
			h.renderNotice(w, http.StatusNotFound, "Форма не найдена",
				"Форма по этой ссылке больше не принимает заявки.")
			return
		}
		// Заявка не передана: значения возвращаются в форму, а причина в
		// журнал уходит без персональных данных — только ссылка формы.
		h.log.Error("web: заявка не передана", "form", string(ref), "error", err)
		h.renderForm(w, http.StatusServiceUnavailable, formView{
			Action:  formAction(ref),
			Fields:  formFields(valuesOf(submission), nil),
			Failure: "Заявка не отправлена. Попробуйте ещё раз через минуту.",
		})
		return
	}

	http.Redirect(w, r, formAction(ref)+"?sent=1", http.StatusSeeOther)
}

// handleTracking разрешает трекинг-переход и уводит посетителя по адресу
// владельца. Пикселей и метрик открытий нет: фиксируется только переход
// (architecture.md, §11, «Трекинг»).
func (h *handler) handleTracking(w http.ResponseWriter, r *http.Request) {
	ref, ok := h.requireRef(w, r)
	if !ok {
		return
	}
	resolved, err := h.config.Tracking.Resolve(r.Context(), ports.LinkRef(ref))
	if err != nil {
		h.renderSourceError(w, err, "трекинг-ссылка")
		return
	}
	target, ok := safeRedirectTarget(resolved)
	if !ok {
		// Адрес пришёл от владельца и не прошёл проверку: переходить по нему
		// нельзя — иначе edge стал бы открытым редиректом.
		h.log.Error("web: недопустимый адрес перехода", "link", string(ref))
		h.renderNotice(w, http.StatusServiceUnavailable, "Ссылка недоступна",
			"Переход по этой ссылке временно невозможен.")
		return
	}

	transition := ports.Transition{Link: ports.LinkRef(ref), Target: target, OccurredAt: h.now()}
	if err := h.config.Tracking.Record(r.Context(), transition); err != nil {
		// Учёт перехода не задерживает посетителя: адрес уже разрешён, и
		// отказ учёта — потеря статистики, а не отказ перехода.
		h.log.Warn("web: переход не записан", "link", string(ref), "error", err)
	}

	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, target, http.StatusFound)
}

// renderForm отдаёт страницу формы. Кэширование запрещено: страница несёт
// введённые посетителем персональные данные, и промежуточный кэш не должен их
// сохранять (architecture.md, §10).
func (h *handler) renderForm(w http.ResponseWriter, status int, view formView) {
	w.Header().Set("Cache-Control", "no-store")
	title := "Оставить заявку"
	if view.Sent {
		title = "Заявка отправлена"
	}
	h.render(w, status, pageForm, pageView(title, view))
}

// renderSourceError превращает ошибку владельца в ответ посетителю: «не
// найдено» — 404, недоступность владельца — 503. Формулировка владельца наружу
// не уходит: она адресована эксплуатации, а не посетителю.
func (h *handler) renderSourceError(w http.ResponseWriter, err error, resource string) {
	if ports.IsNotFound(err) {
		h.renderNotice(w, http.StatusNotFound, "Страница не найдена",
			"Проверьте адрес: страница могла быть удалена или ещё не опубликована.")
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		// Посетитель ушёл или бюджет запроса истёк. Ответа уже может не быть,
		// но факт отмечается: это отмена, а не отказ владельца.
		h.log.Warn("web: запрос к владельцу прерван", "resource", resource, "error", err)
	} else {
		h.log.Error("web: отказ владельца", "resource", resource, "error", err)
	}
	h.renderNotice(w, http.StatusServiceUnavailable, "Страница временно недоступна",
		"Мы уже знаем о проблеме. Попробуйте обновить страницу позже.")
}

// requireRef читает ссылку из пути и проверяет её допустимость. Недопустимая
// ссылка — это 404, а не 400: для посетителя разницы нет, а различать
// внутреннюю причину ему не нужно.
func (h *handler) requireRef(w http.ResponseWriter, r *http.Request) (ports.PageRef, bool) {
	ref := r.PathValue("ref")
	if !refPattern.MatchString(ref) {
		h.renderNotice(w, http.StatusNotFound, "Страница не найдена",
			"Проверьте адрес: страница могла быть удалена или ещё не опубликована.")
		return "", false
	}
	return ports.PageRef(ref), true
}

// submissionFrom собирает заявку из полей формы. Значения обрезаются по краям:
// пробел на конце адреса — не отказ посетителю, а шум разметки.
func (h *handler) submissionFrom(r *http.Request, ref ports.PageRef) ports.Submission {
	return ports.Submission{
		Form:       ports.FormRef(ref),
		Name:       strings.TrimSpace(r.PostFormValue("name")),
		Contact:    strings.TrimSpace(r.PostFormValue("contact")),
		Message:    strings.TrimSpace(r.PostFormValue("message")),
		Consent:    r.PostFormValue("consent") != "",
		ReceivedAt: h.now(),
	}
}

// formAction — адрес приёма формы для ссылки страницы.
func formAction(ref ports.PageRef) string {
	return "/f/" + url.PathEscape(string(ref))
}

// safeRedirectTarget проверяет адрес перехода, полученный от владельца: только
// абсолютный http/https или путь от корня. Без проверки ошибка или подмена
// данных у владельца превратила бы публичный эндпоинт в открытый редирект.
func safeRedirectTarget(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", false
	}
	switch {
	case parsed.Scheme == "" && parsed.Host == "":
		// Путь от корня, но не protocol-relative "//host": двойная косая
		// черта увела бы посетителя на чужой хост.
		if !strings.HasPrefix(parsed.Path, "/") || strings.HasPrefix(parsed.Path, "//") {
			return "", false
		}
		return parsed.String(), true
	case (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "":
		return parsed.String(), true
	default:
		return "", false
	}
}
