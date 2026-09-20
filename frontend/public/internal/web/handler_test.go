package web_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"lead-gen/contracts/content-production/renderedartifact"
	"lead-gen/contracts/engagement-observation/readsignal"
	"lead-gen/contracts/outreach-orchestration/formsubmission"
	"lead-gen/frontend/public/internal/ports"
	"lead-gen/frontend/public/internal/web"
)

// fixedTime — момент, который edge проставляет в заявке и переходе. Время
// подменяется, чтобы проверять именно проставленное значение, а не текущее.
var fixedTime = time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)

const (
	demoRef      = ports.PageRef("demo")
	demoForm     = "demo"
	demoLink     = ports.LinkRef("demo")
	demoTarget   = "https://example.com/landing"
	demoHeadline = "Заголовок демонстрационного лендинга"
)

// landingSource — тестовый источник наполнения лендингов.
type landingSource struct {
	pages   map[ports.PageRef]ports.LandingPage
	err     error
	panics  bool
	refused ports.PageRef
}

func (s *landingSource) Landing(_ context.Context, ref ports.PageRef) (ports.LandingPage, error) {
	if s.panics {
		panic("источник наполнения сломан")
	}
	if s.err != nil {
		s.refused = ref
		return ports.LandingPage{}, s.err
	}
	page, ok := s.pages[ref]
	if !ok {
		return ports.LandingPage{}, renderedartifact.ErrNotFound
	}
	return page, nil
}

// presentationSource — тестовый источник наполнения презентаций.
type presentationSource struct {
	decks map[ports.PageRef]ports.Presentation
	err   error
}

func (s *presentationSource) Presentation(_ context.Context, ref ports.PageRef) (ports.Presentation, error) {
	if s.err != nil {
		return ports.Presentation{}, s.err
	}
	deck, ok := s.decks[ref]
	if !ok {
		return ports.Presentation{}, renderedartifact.ErrNotFound
	}
	return deck, nil
}

// submissionSink — тестовый приёмник заявок.
type submissionSink struct {
	received []ports.Submission
	err      error
}

func (s *submissionSink) Submit(_ context.Context, submission ports.Submission) error {
	s.received = append(s.received, submission)
	return s.err
}

// trackingResolver — тестовый разрешитель трекинг-переходов.
type trackingResolver struct {
	targets     map[ports.LinkRef]string
	transitions []ports.Transition
	resolveErr  error
	recordErr   error
}

func (r *trackingResolver) Resolve(_ context.Context, ref ports.LinkRef) (string, error) {
	if r.resolveErr != nil {
		return "", r.resolveErr
	}
	target, ok := r.targets[ref]
	if !ok {
		return "", readsignal.ErrNotFound
	}
	return target, nil
}

func (r *trackingResolver) Record(_ context.Context, transition ports.Transition) error {
	r.transitions = append(r.transitions, transition)
	return r.recordErr
}

// handlerWith собирает handler на тестовых портах. Через mutate проверяются
// ветви отказа и настройка корня домена.
func handlerWith(t *testing.T, mutate func(*web.Config)) (http.Handler, *submissionSink, *trackingResolver) {
	t.Helper()
	sink := &submissionSink{}
	tracking := &trackingResolver{targets: map[ports.LinkRef]string{demoLink: demoTarget}}
	config := web.Config{
		Landings: &landingSource{pages: map[ports.PageRef]ports.LandingPage{demoRef: demoLanding()}},
		Presentations: &presentationSource{
			decks: map[ports.PageRef]ports.Presentation{demoRef: demoPresentation()},
		},
		Submissions: sink,
		Tracking:    tracking,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:         func() time.Time { return fixedTime },
	}
	if mutate != nil {
		mutate(&config)
	}
	handler, err := web.New(config)
	if err != nil {
		t.Fatalf("web.New: неожиданная ошибка: %v", err)
	}
	return handler, sink, tracking
}

// serve выполняет запрос к обработчику. Переданная форма кодируется как
// x-www-form-urlencoded.
func serve(t *testing.T, handler http.Handler, method, target string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	request := httptest.NewRequest(method, target, body)
	if form != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

// validForm — корректно заполненная форма обратной связи.
func validForm() url.Values {
	return url.Values{
		"name":    {"Иван Петров"},
		"contact": {"ivan@example.com"},
		"message": {"Прошу связаться со мной"},
		"consent": {"yes"},
	}
}

func demoLanding() ports.LandingPage {
	return ports.LandingPage{
		Ref:      demoRef,
		Title:    "Тестовый лендинг",
		Headline: demoHeadline,
		Intro:    "Вводный абзац лендинга",
		Sections: []ports.Section{
			{Heading: "Раздел первый", Body: "Текст первого раздела"},
			{Heading: "Раздел второй", Body: "Текст второго раздела"},
		},
		FormRef: ports.PageFormRef(demoForm),
	}
}

func demoPresentation() ports.Presentation {
	return ports.Presentation{
		Ref:   demoRef,
		Title: "Тестовая презентация",
		Slides: []ports.Slide{
			{Heading: "Слайд первый", Bullets: []string{"Пункт один", "Пункт два"}},
			{Heading: "Слайд второй", Bullets: []string{"Пункт три"}},
		},
	}
}

func TestLandingRendered(t *testing.T) {
	handler, _, _ := handlerWith(t, nil)
	recorder := serve(t, handler, http.MethodGet, "/l/demo", nil)

	if recorder.Code != http.StatusOK {
		t.Fatalf("код ответа: получено %d, ожидалось %d", recorder.Code, http.StatusOK)
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "text/html; charset=utf-8" {
		t.Errorf("Content-Type: получено %q", contentType)
	}
	body := recorder.Body.String()
	for _, expected := range []string{
		demoHeadline,
		"Вводный абзац лендинга",
		"Раздел первый",
		"Текст второго раздела",
		`href="/f/demo"`,
		`<html lang="ru">`,
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("в разметке лендинга нет %q:\n%s", expected, body)
		}
	}
}

func TestLandingWithoutFormHasNoCallToAction(t *testing.T) {
	handler, _, _ := handlerWith(t, func(config *web.Config) {
		page := demoLanding()
		page.FormRef = ""
		config.Landings = &landingSource{pages: map[ports.PageRef]ports.LandingPage{demoRef: page}}
	})
	body := serve(t, handler, http.MethodGet, "/l/demo", nil).Body.String()

	if strings.Contains(body, "/f/") {
		t.Errorf("на странице без формы осталась ссылка на форму:\n%s", body)
	}
}

func TestLandingEscapesOwnerContent(t *testing.T) {
	handler, _, _ := handlerWith(t, func(config *web.Config) {
		page := demoLanding()
		page.Headline = `<script>alert("внедрение")</script>`
		config.Landings = &landingSource{pages: map[ports.PageRef]ports.LandingPage{demoRef: page}}
	})
	body := serve(t, handler, http.MethodGet, "/l/demo", nil).Body.String()

	if strings.Contains(body, "<script>") {
		t.Errorf("наполнение владельца попало в разметку без экранирования:\n%s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("ожидалось экранированное наполнение владельца:\n%s", body)
	}
}

func TestLandingUnknownRefIsNotFound(t *testing.T) {
	handler, _, _ := handlerWith(t, nil)
	recorder := serve(t, handler, http.MethodGet, "/l/unknown", nil)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("код ответа: получено %d, ожидалось %d", recorder.Code, http.StatusNotFound)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "Страница не найдена") {
		t.Errorf("нет страницы «не найдено»:\n%s", body)
	}
}

func TestLandingMalformedRefIsNotFound(t *testing.T) {
	handler, _, _ := handlerWith(t, nil)
	// Ссылка вне безопасного алфавита не доходит до владельца.
	recorder := serve(t, handler, http.MethodGet, "/l/bad!ref", nil)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("код ответа: получено %d, ожидалось %d", recorder.Code, http.StatusNotFound)
	}
}

func TestLandingOwnerFailureIsServiceUnavailable(t *testing.T) {
	handler, _, _ := handlerWith(t, func(config *web.Config) {
		config.Landings = &landingSource{err: errors.New("владелец недоступен")}
	})
	recorder := serve(t, handler, http.MethodGet, "/l/demo", nil)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("код ответа: получено %d, ожидалось %d", recorder.Code, http.StatusServiceUnavailable)
	}
	if body := recorder.Body.String(); strings.Contains(body, "владелец недоступен") {
		t.Errorf("формулировка владельца ушла посетителю:\n%s", body)
	}
}

func TestLandingCancelledRequestIsServiceUnavailable(t *testing.T) {
	handler, _, _ := handlerWith(t, func(config *web.Config) {
		config.Landings = &landingSource{err: context.Canceled}
	})
	recorder := serve(t, handler, http.MethodGet, "/l/demo", nil)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("код ответа: получено %d, ожидалось %d", recorder.Code, http.StatusServiceUnavailable)
	}
}

func TestLandingPanicIsRecovered(t *testing.T) {
	handler, _, _ := handlerWith(t, func(config *web.Config) {
		config.Landings = &landingSource{panics: true}
	})
	recorder := serve(t, handler, http.MethodGet, "/l/demo", nil)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("код ответа: получено %d, ожидалось %d", recorder.Code, http.StatusInternalServerError)
	}
	if body := recorder.Body.String(); strings.Contains(body, "источник наполнения сломан") {
		t.Errorf("подробность паники ушла посетителю:\n%s", body)
	}
}

func TestPresentationRendered(t *testing.T) {
	handler, _, _ := handlerWith(t, nil)
	recorder := serve(t, handler, http.MethodGet, "/p/demo", nil)

	if recorder.Code != http.StatusOK {
		t.Fatalf("код ответа: получено %d, ожидалось %d", recorder.Code, http.StatusOK)
	}
	body := recorder.Body.String()
	for _, expected := range []string{"Тестовая презентация", "Слайд первый", "Пункт один", "Слайд второй", "Пункт три"} {
		if !strings.Contains(body, expected) {
			t.Errorf("в разметке презентации нет %q:\n%s", expected, body)
		}
	}
}

func TestPresentationUnknownRefIsNotFound(t *testing.T) {
	handler, _, _ := handlerWith(t, nil)
	recorder := serve(t, handler, http.MethodGet, "/p/unknown", nil)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("код ответа: получено %d, ожидалось %d", recorder.Code, http.StatusNotFound)
	}
}

func TestFormPageRenderedWithoutCaching(t *testing.T) {
	handler, _, _ := handlerWith(t, nil)
	recorder := serve(t, handler, http.MethodGet, "/f/demo", nil)

	if recorder.Code != http.StatusOK {
		t.Fatalf("код ответа: получено %d, ожидалось %d", recorder.Code, http.StatusOK)
	}
	if cache := recorder.Header().Get("Cache-Control"); cache != "no-store" {
		t.Errorf("Cache-Control страницы формы: получено %q, ожидалось %q", cache, "no-store")
	}
	body := recorder.Body.String()
	for _, expected := range []string{`action="/f/demo"`, `name="name"`, `name="contact"`, `name="message"`, `name="consent"`} {
		if !strings.Contains(body, expected) {
			t.Errorf("в форме нет %q:\n%s", expected, body)
		}
	}
}

func TestFormSubmitSuccessRedirectsAndDelivers(t *testing.T) {
	handler, sink, _ := handlerWith(t, nil)
	recorder := serve(t, handler, http.MethodPost, "/f/demo", validForm())

	if recorder.Code != http.StatusSeeOther {
		t.Fatalf("код ответа: получено %d, ожидалось %d", recorder.Code, http.StatusSeeOther)
	}
	if location := recorder.Header().Get("Location"); location != "/f/demo?sent=1" {
		t.Fatalf("Location: получено %q", location)
	}
	if len(sink.received) != 1 {
		t.Fatalf("заявок передано: %d, ожидалось 1", len(sink.received))
	}
	received := sink.received[0]
	if received.Form != ports.FormRef(demoForm) {
		t.Errorf("форма заявки: получено %q", received.Form)
	}
	if received.Name != "Иван Петров" || received.Contact != "ivan@example.com" || received.Message != "Прошу связаться со мной" {
		t.Errorf("поля заявки переданы неверно: %+v", received)
	}
	if !received.Consent {
		t.Error("согласие не передано")
	}
	if !received.ReceivedAt.Equal(fixedTime) {
		t.Errorf("момент приёма: получено %v, ожидалось %v", received.ReceivedAt, fixedTime)
	}

	// Подтверждение — отдельный GET: обновление страницы не отправляет заявку заново.
	confirmation := serve(t, handler, http.MethodGet, "/f/demo?sent=1", nil)
	if confirmation.Code != http.StatusOK {
		t.Fatalf("код подтверждения: получено %d", confirmation.Code)
	}
	if body := confirmation.Body.String(); !strings.Contains(body, "Заявка отправлена") {
		t.Errorf("нет подтверждения приёма заявки:\n%s", body)
	}
	if len(sink.received) != 1 {
		t.Fatalf("подтверждение отправило заявку повторно: %d", len(sink.received))
	}
}

func TestFormSubmitTrimsValues(t *testing.T) {
	handler, sink, _ := handlerWith(t, nil)
	form := validForm()
	form.Set("name", "  Иван  ")
	form.Set("contact", " ivan@example.com ")
	serve(t, handler, http.MethodPost, "/f/demo", form)

	if len(sink.received) != 1 {
		t.Fatalf("заявок передано: %d, ожидалось 1", len(sink.received))
	}
	if sink.received[0].Name != "Иван" || sink.received[0].Contact != "ivan@example.com" {
		t.Errorf("значения не обрезаны по краям: %+v", sink.received[0])
	}
}

func TestFormSubmitValidationErrors(t *testing.T) {
	handler, sink, _ := handlerWith(t, nil)
	recorder := serve(t, handler, http.MethodPost, "/f/demo", url.Values{})

	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("код ответа: получено %d, ожидалось %d", recorder.Code, http.StatusUnprocessableEntity)
	}
	if len(sink.received) != 0 {
		t.Fatalf("неполная заявка передана приёмнику: %+v", sink.received)
	}
	body := recorder.Body.String()
	for _, expected := range []string{"Укажите имя", "Укажите почту или телефон", "Без согласия"} {
		if !strings.Contains(body, expected) {
			t.Errorf("нет сообщения об ошибке %q:\n%s", expected, body)
		}
	}
	if cache := recorder.Header().Get("Cache-Control"); cache != "no-store" {
		t.Errorf("Cache-Control ответа с ошибкой: получено %q", cache)
	}
}

func TestFormSubmitInvalidContactRejected(t *testing.T) {
	handler, sink, _ := handlerWith(t, nil)
	form := validForm()
	form.Set("contact", "не контакт")
	recorder := serve(t, handler, http.MethodPost, "/f/demo", form)

	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("код ответа: получено %d, ожидалось %d", recorder.Code, http.StatusUnprocessableEntity)
	}
	if len(sink.received) != 0 {
		t.Fatalf("заявка с неконтактным значением передана: %+v", sink.received)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "не почта и не телефон") {
		t.Errorf("нет сообщения о формате связи:\n%s", body)
	}
}

func TestFormSubmitWithoutConsentRejected(t *testing.T) {
	handler, sink, _ := handlerWith(t, nil)
	form := validForm()
	form.Del("consent")
	recorder := serve(t, handler, http.MethodPost, "/f/demo", form)

	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("код ответа: получено %d, ожидалось %d", recorder.Code, http.StatusUnprocessableEntity)
	}
	if len(sink.received) != 0 {
		t.Fatalf("заявка без согласия передана: %+v", sink.received)
	}
}

func TestFormSubmitTooLongMessageRejected(t *testing.T) {
	handler, sink, _ := handlerWith(t, nil)
	form := validForm()
	form.Set("message", strings.Repeat("я", 2001))
	recorder := serve(t, handler, http.MethodPost, "/f/demo", form)

	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("код ответа: получено %d, ожидалось %d", recorder.Code, http.StatusUnprocessableEntity)
	}
	if len(sink.received) != 0 {
		t.Fatalf("слишком длинное сообщение передано: %d", len(sink.received))
	}
}

func TestFormSubmitReturnsEnteredValuesOnError(t *testing.T) {
	handler, _, _ := handlerWith(t, nil)
	form := url.Values{
		"name":    {"Иван"},
		"contact": {"ivan@example.com"},
		"message": {"Текст обращения"},
	}
	body := serve(t, handler, http.MethodPost, "/f/demo", form).Body.String()

	for _, expected := range []string{`value="Иван"`, `value="ivan@example.com"`, "Текст обращения"} {
		if !strings.Contains(body, expected) {
			t.Errorf("введённое значение %q не возвращено в форму:\n%s", expected, body)
		}
	}
}

func TestFormSubmitSinkFailureIsServiceUnavailable(t *testing.T) {
	handler, _, _ := handlerWith(t, func(config *web.Config) {
		config.Submissions = &submissionSink{err: errors.New("приёмник недоступен")}
	})
	recorder := serve(t, handler, http.MethodPost, "/f/demo", validForm())

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("код ответа: получено %d, ожидалось %d", recorder.Code, http.StatusServiceUnavailable)
	}
	if !strings.Contains(recorder.Body.String(), "Заявка не отправлена") {
		t.Errorf("нет сообщения о неудаче передачи:\n%s", recorder.Body.String())
	}
}

func TestFormSubmitMalformedRefIsNotFound(t *testing.T) {
	handler, sink, _ := handlerWith(t, nil)
	recorder := serve(t, handler, http.MethodPost, "/f/bad!ref", validForm())

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("код ответа: получено %d, ожидалось %d", recorder.Code, http.StatusNotFound)
	}
	if len(sink.received) != 0 {
		t.Fatalf("заявка ушла со ссылкой формы вне безопасного алфавита: %+v", sink.received)
	}
}

func TestFormSubmitUnknownFormIsNotFound(t *testing.T) {
	// Приёмник владельца — единственный, кто знает, существует ли форма.
	handler, _, _ := handlerWith(t, func(config *web.Config) {
		config.Submissions = &submissionSink{err: formsubmission.ErrNotFound}
	})
	recorder := serve(t, handler, http.MethodPost, "/f/unknown", validForm())

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("код ответа: получено %d, ожидалось %d", recorder.Code, http.StatusNotFound)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "Форма не найдена") {
		t.Errorf("нет страницы о неизвестной форме:\n%s", body)
	}
}

func TestTrackingRedirectsAndRecordsTransition(t *testing.T) {
	handler, _, tracking := handlerWith(t, nil)
	recorder := serve(t, handler, http.MethodGet, "/t/demo", nil)

	if recorder.Code != http.StatusFound {
		t.Fatalf("код ответа: получено %d, ожидалось %d", recorder.Code, http.StatusFound)
	}
	if location := recorder.Header().Get("Location"); location != demoTarget {
		t.Fatalf("Location: получено %q, ожидалось %q", location, demoTarget)
	}
	if cache := recorder.Header().Get("Cache-Control"); cache != "no-store" {
		t.Errorf("Cache-Control перехода: получено %q", cache)
	}
	if len(tracking.transitions) != 1 {
		t.Fatalf("переходов записано: %d, ожидалось 1", len(tracking.transitions))
	}
	recorded := tracking.transitions[0]
	if recorded.Link != demoLink || recorded.Target != demoTarget {
		t.Errorf("переход записан неверно: %+v", recorded)
	}
	if !recorded.OccurredAt.Equal(fixedTime) {
		t.Errorf("момент перехода: получено %v, ожидалось %v", recorded.OccurredAt, fixedTime)
	}
}

func TestTrackingUnknownLinkIsNotFound(t *testing.T) {
	handler, _, _ := handlerWith(t, nil)
	recorder := serve(t, handler, http.MethodGet, "/t/unknown", nil)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("код ответа: получено %d, ожидалось %d", recorder.Code, http.StatusNotFound)
	}
}

func TestTrackingOwnerFailureIsServiceUnavailable(t *testing.T) {
	handler, _, _ := handlerWith(t, func(config *web.Config) {
		config.Tracking = &trackingResolver{resolveErr: errors.New("владелец ссылок недоступен")}
	})
	recorder := serve(t, handler, http.MethodGet, "/t/demo", nil)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("код ответа: получено %d, ожидалось %d", recorder.Code, http.StatusServiceUnavailable)
	}
	if location := recorder.Header().Get("Location"); location != "" {
		t.Errorf("отказ разрешения увёл посетителя по адресу %q", location)
	}
}

func TestTrackingRecordFailureStillRedirects(t *testing.T) {
	handler, _, _ := handlerWith(t, func(config *web.Config) {
		config.Tracking = &trackingResolver{
			targets:   map[ports.LinkRef]string{demoLink: demoTarget},
			recordErr: errors.New("учёт недоступен"),
		}
	})
	recorder := serve(t, handler, http.MethodGet, "/t/demo", nil)

	if recorder.Code != http.StatusFound {
		t.Fatalf("код ответа: получено %d, ожидалось %d", recorder.Code, http.StatusFound)
	}
	if location := recorder.Header().Get("Location"); location != demoTarget {
		t.Errorf("Location: получено %q", location)
	}
}

func TestTrackingRejectsUnsafeTargets(t *testing.T) {
	for _, target := range []string{
		"javascript:alert(1)",
		"//evil.example/path",
		"data:text/html,<script>alert(1)</script>",
		"",
		"ftp://example.com/file",
	} {
		t.Run(target, func(t *testing.T) {
			handler, _, _ := handlerWith(t, func(config *web.Config) {
				config.Tracking = &trackingResolver{targets: map[ports.LinkRef]string{demoLink: target}}
			})
			recorder := serve(t, handler, http.MethodGet, "/t/demo", nil)

			if recorder.Code != http.StatusServiceUnavailable {
				t.Fatalf("код ответа: получено %d, ожидалось %d", recorder.Code, http.StatusServiceUnavailable)
			}
			if location := recorder.Header().Get("Location"); location != "" {
				t.Errorf("недопустимый адрес %q отдан в Location", location)
			}
		})
	}
}

func TestTrackingAllowsRelativeRootedTarget(t *testing.T) {
	handler, _, _ := handlerWith(t, func(config *web.Config) {
		config.Tracking = &trackingResolver{targets: map[ports.LinkRef]string{demoLink: "/owner/page?from=mail"}}
	})
	recorder := serve(t, handler, http.MethodGet, "/t/demo", nil)

	if recorder.Code != http.StatusFound {
		t.Fatalf("код ответа: получено %d, ожидалось %d", recorder.Code, http.StatusFound)
	}
	if location := recorder.Header().Get("Location"); location != "/owner/page?from=mail" {
		t.Errorf("Location: получено %q", location)
	}
}

func TestRootLandingServesConfiguredLanding(t *testing.T) {
	handler, _, _ := handlerWith(t, func(config *web.Config) {
		config.DefaultLanding = demoRef
	})
	recorder := serve(t, handler, http.MethodGet, "/", nil)

	if recorder.Code != http.StatusOK {
		t.Fatalf("код ответа: получено %d, ожидалось %d", recorder.Code, http.StatusOK)
	}
	if body := recorder.Body.String(); !strings.Contains(body, demoHeadline) {
		t.Errorf("корень не отдал лендинг:\n%s", body)
	}
}

func TestRootWithoutConfiguredLandingIsNotFound(t *testing.T) {
	handler, _, _ := handlerWith(t, nil)
	recorder := serve(t, handler, http.MethodGet, "/", nil)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("код ответа: получено %d, ожидалось %d", recorder.Code, http.StatusNotFound)
	}
}

func TestLandingMethodNotAllowed(t *testing.T) {
	handler, _, _ := handlerWith(t, nil)
	recorder := serve(t, handler, http.MethodPost, "/l/demo", nil)

	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("код ответа: получено %d, ожидалось %d", recorder.Code, http.StatusMethodNotAllowed)
	}
}

func TestSecurityHeadersPresent(t *testing.T) {
	handler, _, _ := handlerWith(t, nil)
	recorder := serve(t, handler, http.MethodGet, "/l/demo", nil)

	header := recorder.Header()
	if policy := header.Get("Content-Security-Policy"); policy == "" || !strings.Contains(policy, "default-src 'none'") {
		t.Errorf("CSP: получено %q", policy)
	}
	if header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("X-Content-Type-Options: получено %q", header.Get("X-Content-Type-Options"))
	}
	if header.Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("Referrer-Policy: получено %q", header.Get("Referrer-Policy"))
	}
}

func TestStaticStylesheetServed(t *testing.T) {
	handler, _, _ := handlerWith(t, nil)
	recorder := serve(t, handler, http.MethodGet, "/static/edge.css", nil)

	if recorder.Code != http.StatusOK {
		t.Fatalf("код ответа: получено %d, ожидалось %d", recorder.Code, http.StatusOK)
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.Contains(contentType, "text/css") {
		t.Errorf("Content-Type: получено %q", contentType)
	}
	if body := recorder.Body.String(); !strings.Contains(body, ".page") {
		t.Errorf("отдано не оформление edge:\n%s", body)
	}
}

func TestHealthProbe(t *testing.T) {
	handler, _, _ := handlerWith(t, nil)
	recorder := serve(t, handler, http.MethodGet, "/healthz", nil)

	if recorder.Code != http.StatusOK {
		t.Fatalf("код ответа: получено %d, ожидалось %d", recorder.Code, http.StatusOK)
	}
	if body := recorder.Body.String(); body != "ok\n" {
		t.Errorf("тело пробы: получено %q", body)
	}
}

func TestNewRequiresPorts(t *testing.T) {
	cases := map[string]web.Config{
		"без источника лендингов": {
			Presentations: &presentationSource{},
			Submissions:   &submissionSink{},
			Tracking:      &trackingResolver{},
		},
		"без источника презентаций": {
			Landings:    &landingSource{},
			Submissions: &submissionSink{},
			Tracking:    &trackingResolver{},
		},
		"без приёмника заявок": {
			Landings:      &landingSource{},
			Presentations: &presentationSource{},
			Tracking:      &trackingResolver{},
		},
		"без разрешителя переходов": {
			Landings:      &landingSource{},
			Presentations: &presentationSource{},
			Submissions:   &submissionSink{},
		},
	}
	for name, config := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := web.New(config); err == nil {
				t.Fatal("ожидалась ошибка сборки handler")
			}
		})
	}
}
