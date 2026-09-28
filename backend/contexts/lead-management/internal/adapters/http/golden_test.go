package httpapi_test

import (
	"bytes"
	"context"
	"flag"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	httpapi "lead-gen/backend/contexts/lead-management/internal/adapters/http"
	"lead-gen/backend/contexts/lead-management/internal/adapters/persistence/memory"
	"lead-gen/backend/contexts/lead-management/internal/app"
	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/backend/platform/jobs"
	"lead-gen/contracts/integration-platform/forum"
	"lead-gen/contracts/integration-platform/llm"
)

// tokenRe — случайные токены (ключ идемпотентности и CSRF) в разметке. Они
// генерируются на каждый рендер, поэтому в золотом файле заменяются на TOKEN.
var tokenRe = regexp.MustCompile(`[0-9a-f]{64}`)

// normalizeTokens заменяет случайные токены в разметке на TOKEN, чтобы золотой
// файл не зависел от ключа идемпотентности и CSRF-токена конкретного запуска.
func normalizeTokens(data []byte) []byte {
	return tokenRe.ReplaceAll(data, []byte("TOKEN"))
}

// update — при true перезаписывает золотой файл рендера карточки.
var update = flag.Bool("update", false, "перезаписать золотой файл рендера карточки")

// seedAssessment сохраняет оценку компании в задании через доменный конструктор:
// свидетельства проходят инварианты вердикта до попадания в золотой рендер.
func seedAssessment(t *testing.T, store *memory.AssessmentStore, jobID, companyID string, verdict domain.AssessmentVerdict, signal domain.SignalKind, evidence []domain.Evidence) {
	t.Helper()
	assessment, err := domain.CreateLeadAssessment(domain.CreateLeadAssessmentParams{
		JobID:      jobID,
		CompanyID:  companyID,
		Verdict:    verdict,
		SignalKind: signal,
		Evidence:   evidence,
		CreatedAt:  testTime,
	})
	if err != nil {
		t.Fatalf("CreateLeadAssessment(%s/%s): %v", jobID, companyID, err)
	}
	if err := store.Save(context.Background(), assessment); err != nil {
		t.Fatalf("Save(%s/%s): %v", jobID, companyID, err)
	}
}

// newCardServer собирает сервер с одной фиксированной карточкой для золотого
// рендера. Все входные данные детерминированы: строки зафиксированы, время — из
// testTime. Одна и та же компания имеет разные оценки в двух заданиях — золотые
// рендеры карточки показывают, что оценка не переносится между заданиями.
func newCardServer(t *testing.T) *httpapi.Server {
	t.Helper()
	profiles := memory.NewLeadProfileRepository()
	contacts := memory.NewContactRepository()
	jobs := memory.NewSearchJobStore()
	messages := memory.NewOutboundMessageRepository()
	templates := memory.NewEmailTemplateRepository()
	assessments := memory.NewAssessmentStore()

	seedCompany(t, profiles, "example.com", "Пример Компании", "Описание компании",
		"https://www.linkedin.com/company/example")
	seedContact(t, contacts, "example.com", "hello@example.com", "+7 999 123-45-67", "основатель")
	seedTemplate(t, templates, "intro", "Приветствие", "Тема для {{company}}", "Здравствуйте, {{company}}!",
		domain.MergeVariable{Key: domain.MergeVariableCompany, Fallback: "ваша компания"},
	)

	// Гипотеза: совпадение по профилю, сигнала потребности нет — не хватает
	// прямого заявления.
	seedAssessment(t, assessments, "job-hypothesis", "example.com", domain.VerdictHypothesis, domain.SignalProfile, []domain.Evidence{{
		Criterion:  "агентства автоматизации B2B-продаж",
		Fact:       "профиль: B2B-агентство из отраслевого каталога",
		SourceURL:  "https://example.com/about",
		Reasoning:  "профиль совпадает, но заявленной потребности в автоматизации нет",
		SignalKind: domain.SignalProfile,
		Missing:    "прямое заявление о потребности в автоматизации продаж",
	}})
	// Не подходит: установлено несоответствие обязательному ограничению запроса.
	seedAssessment(t, assessments, "job-unsuitable", "example.com", domain.VerdictUnsuitable, domain.SignalProfile, []domain.Evidence{{
		Criterion:  "только компании с собственным отделом продаж",
		Fact:       "внешние продажи через партнёров",
		SourceURL:  "https://example.com/services",
		Reasoning:  "обязательное ограничение запроса не выполнено",
		SignalKind: domain.SignalProfile,
		Mismatch:   "нет собственного отдела продаж — обязательное ограничение запроса",
	}})

	runSearch := &app.RunSearch{
		Jobs:        jobs,
		Profiles:    profiles,
		Contacts:    contacts,
		Search:      &fakeSearch{},
		Crawl:       &fakeCrawl{},
		LLM:         &fakeLLM{},
		Clock:       fakeClock{t: testTime},
		Limits:      app.CrawlLimits{MaxPages: 25, MaxDepth: 3, MaxTimeSeconds: 120},
		Assessments: assessments,
	}
	showcase := &app.Showcase{Jobs: jobs, Profiles: profiles, Contacts: contacts, Messages: messages}
	templateLibrary := &app.Templates{Repository: templates, Profiles: profiles, Contacts: contacts, Clock: fakeClock{t: testTime}}
	return httpapi.NewServer(httpapi.Dependencies{
		RunSearch: runSearch,
		Showcase:  showcase,
		Templates: templateLibrary,
		Auth:      httpapi.AuthConfig{Username: "operator", Password: "secret"},
	})
}

// TestCardRenderGolden сравнивает отрисовку фиксированной карточки в контексте
// задания-гипотезы с золотым файлом. С флагом -update золотой файл
// перезаписывается текущим выводом.
func TestCardRenderGolden(t *testing.T) {
	renderGoldenPage(t, newCardServer(t), "/cards/example.com?job=job-hypothesis", "card.golden")
}

// TestCardRenderGoldenUnsuitable проверяет раскрытие «Почему эта оценка?» для
// состояния «Не подходит» — второй обязательный случай карточки.
func TestCardRenderGoldenUnsuitable(t *testing.T) {
	renderGoldenPage(t, newCardServer(t), "/cards/example.com?job=job-unsuitable", "card-unsuitable.golden")
}

// newForumSignalsServer собирает сервер с зафиксированными сигналами для
// золотого рендера экрана форумных сигналов. Все входные данные детерминированы.
func newForumSignalsServer(t *testing.T) *httpapi.Server {
	t.Helper()
	profiles := memory.NewLeadProfileRepository()
	contacts := memory.NewContactRepository()
	jobs := memory.NewSearchJobStore()
	messages := memory.NewOutboundMessageRepository()

	runSearch := &app.RunSearch{
		Jobs:     jobs,
		Profiles: profiles,
		Contacts: contacts,
		Search:   &fakeSearch{},
		Crawl:    &fakeCrawl{},
		LLM:      &fakeLLM{},
		Clock:    fakeClock{t: testTime},
		Limits:   app.CrawlLimits{MaxPages: 25, MaxDepth: 3, MaxTimeSeconds: 120},
	}
	showcase := &app.Showcase{Jobs: jobs, Profiles: profiles, Contacts: contacts, Messages: messages}
	forumSignals := &app.SearchForumSignals{Forum: &fakeForum{signals: []forum.Signal{
		{Topic: "Тема 1", Summary: "Выжимка 1", SourceURL: "https://reddit.com/t/1", Date: testTime},
		{Topic: "Тема 2", SourceURL: "https://news.ycombinator.com/item?id=1", Date: testTime},
	}}}
	return httpapi.NewServer(httpapi.Dependencies{
		RunSearch:    runSearch,
		Showcase:     showcase,
		ForumSignals: forumSignals,
		Auth:         httpapi.AuthConfig{Username: "operator", Password: "secret"},
	})
}

// TestForumSignalsRenderGolden сравнивает отрисовку экрана форумных сигналов с
// золотым файлом. С флагом -update золотой файл перезаписывается.
func TestForumSignalsRenderGolden(t *testing.T) {
	server := newForumSignalsServer(t)

	loginReq := httptest.NewRequest(http.MethodPost, "/login",
		strings.NewReader("username=operator&password=secret"))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginRec := httptest.NewRecorder()
	server.ServeHTTP(loginRec, loginReq)

	req := httptest.NewRequest(http.MethodGet, "/forum-signals?q=saas", nil)
	for _, cookie := range loginRec.Result().Cookies() {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /forum-signals?q=saas = %d, ожидался 200", rec.Code)
	}
	got := normalizeTokens(rec.Body.Bytes())

	golden := filepath.Join("testdata", "forum-signals.golden")
	if *update {
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			t.Fatalf("создание testdata: %v", err)
		}
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatalf("запись золотого файла: %v", err)
		}
		return
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("чтение золотого файла %s: %v; запустите тест с -update для генерации", golden, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("рендер форумных сигналов не совпадает с золотым файлом %s\n--- получено ---\n%s\n--- ожидалось ---\n%s", golden, got, want)
	}
}

// renderGoldenPage сравнивает отрисовку страницы по адресу с золотым файлом. С
// флагом -update золотой файл перезаписывается. Токены нормализуются.
func renderGoldenPage(t *testing.T, server *httpapi.Server, path, goldenName string) {
	t.Helper()

	loginReq := httptest.NewRequest(http.MethodPost, "/login",
		strings.NewReader("username=operator&password=secret"))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginRec := httptest.NewRecorder()
	server.ServeHTTP(loginRec, loginReq)

	req := httptest.NewRequest(http.MethodGet, path, nil)
	for _, cookie := range loginRec.Result().Cookies() {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, ожидался 200", path, rec.Code)
	}
	got := normalizeTokens(rec.Body.Bytes())

	golden := filepath.Join("testdata", goldenName)
	if *update {
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			t.Fatalf("создание testdata: %v", err)
		}
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatalf("запись золотого файла: %v", err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("чтение золотого файла %s: %v; запустите тест с -update для генерации", golden, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("рендер %s не совпадает с золотым файлом %s\n--- получено ---\n%s\n--- ожидалось ---\n%s", path, golden, got, want)
	}
}

// goldenJobs — фиксированное хранилище заданий для золотого рендера экрана
// задания: идентификатор, время и события детерминированы. Встроенный интерфейс
// оставлен nil: используются только методы витрины.
type goldenJobs struct {
	ports.SearchJobStore
	job    ports.SearchJob
	events []ports.SearchJobEvent
}

// ListSearchJobs возвращает единственное фиксированное задание.
func (g goldenJobs) ListSearchJobs(context.Context, int) ([]ports.SearchJob, error) {
	return []ports.SearchJob{g.job}, nil
}

// ListEvents возвращает фиксированный журнал событий задания.
func (g goldenJobs) ListEvents(context.Context, string) ([]ports.SearchJobEvent, error) {
	return g.events, nil
}

// newJobServer собирает сервер с одним фиксированным заданием и его оценками для
// золотого рендера экрана подробностей: распределение и основания строятся только
// по оценкам этого задания.
func newJobServer(t *testing.T) *httpapi.Server {
	t.Helper()
	store := goldenJobs{
		job: ports.SearchJob{
			ID:        "job-1",
			Query:     "агентства автоматизации B2B",
			Status:    jobs.StatusDone,
			ResultRef: []string{"example.com", "alpha.example"},
			CreatedAt: testTime,
			UpdatedAt: testTime,
			Progress:  &ports.SearchJobProgress{Phase: "finalizing", PagesCrawled: 12, CompaniesFound: 2, TokensSpent: 340},
		},
		events: []ports.SearchJobEvent{
			{JobID: "job-1", Type: ports.EventEnqueued, CreatedAt: testTime},
			{JobID: "job-1", Type: ports.EventPhase, CreatedAt: testTime, Progress: &ports.SearchJobProgress{Phase: "crawling", PagesCrawled: 10, CompaniesFound: 2}},
			{JobID: "job-1", Type: ports.EventDone, CreatedAt: testTime, Progress: &ports.SearchJobProgress{Phase: "finalizing", PagesCrawled: 12, CompaniesFound: 2, TokensSpent: 340}},
		},
	}

	assessments := memory.NewAssessmentStore()
	seedAssessment(t, assessments, "job-1", "example.com", domain.VerdictFits, domain.SignalIndirect, []domain.Evidence{{
		Criterion:  "агентства автоматизации B2B-продаж",
		Fact:       "компания наняла руководителя отдела продаж",
		SourceURL:  "https://example.com/careers",
		Reasoning:  "рост отдела продаж указывает на потребность в автоматизации",
		SignalKind: domain.SignalIndirect,
	}})
	seedAssessment(t, assessments, "job-1", "alpha.example", domain.VerdictHypothesis, domain.SignalProfile, []domain.Evidence{{
		Criterion:  "агентства автоматизации B2B-продаж",
		Fact:       "профиль соответствует отрасли",
		SourceURL:  "https://alpha.example/about",
		Reasoning:  "совпадение по профилю без сигнала потребности",
		SignalKind: domain.SignalProfile,
		Missing:    "прямое заявление о потребности",
	}})

	showcase := &app.Showcase{
		Jobs:     store,
		Profiles: memory.NewLeadProfileRepository(),
		Contacts: memory.NewContactRepository(),
		Messages: memory.NewOutboundMessageRepository(),
	}
	return httpapi.NewServer(httpapi.Dependencies{
		RunSearch: &app.RunSearch{Assessments: assessments},
		Showcase:  showcase,
		Auth:      httpapi.AuthConfig{Username: "operator", Password: "secret"},
	})
}

// newCardsListServer собирает сервер с двумя фиксированными карточками для
// золотого рендера списка: у одной есть email, у другой контактов нет. Все
// входные данные детерминированы.
func newCardsListServer(t *testing.T) *httpapi.Server {
	t.Helper()
	profiles := memory.NewLeadProfileRepository()
	contacts := memory.NewContactRepository()
	jobs := memory.NewSearchJobStore()
	messages := memory.NewOutboundMessageRepository()

	seedCompany(t, profiles, "example.com", "Пример Компании", "Описание компании с email",
		"https://www.linkedin.com/company/example")
	seedContact(t, contacts, "example.com", "hello@example.com", "+7 999 123-45-67", "основатель")
	seedCompany(t, profiles, "phone.example.com", "Только Телефон", "Описание компании без email")

	showcase := &app.Showcase{Jobs: jobs, Profiles: profiles, Contacts: contacts, Messages: messages}
	return httpapi.NewServer(httpapi.Dependencies{
		Showcase: showcase,
		Auth:     httpapi.AuthConfig{Username: "operator", Password: "secret"},
	})
}

// TestCardsRenderGolden сравнивает отрисовку списка карточек с золотым файлом. С
// флагом -update золотой файл перезаписывается текущим выводом.
func TestCardsRenderGolden(t *testing.T) {
	renderGoldenPage(t, newCardsListServer(t), "/cards", "cards.golden")
}

// TestDashboardRenderGolden сравнивает отрисовку главной с золотым файлом: форма
// «Новый поиск» несёт целевое число компаний, лимит поиска и ручные цели.
func TestDashboardRenderGolden(t *testing.T) {
	renderGoldenPage(t, newCardsListServer(t), "/", "dashboard.golden")
}

// TestJobRenderGolden сравнивает отрисовку экрана задания с золотым файлом. С
// флагом -update золотой файл перезаписывается текущим выводом.
func TestJobRenderGolden(t *testing.T) {
	renderGoldenPage(t, newJobServer(t), "/jobs/job-1", "job.golden")
}

// TestSendRenderGolden сравнивает отрисовку экрана отправки с золотым файлом.
// Экран встраивает общий список шаблонов, поэтому проверяет и его разметку.
func TestSendRenderGolden(t *testing.T) {
	renderGoldenPage(t, newCardServer(t), "/send", "send.golden")
}

// TestTemplateEditRenderGolden сравнивает отрисовку экрана правки шаблона с
// золотым файлом. Экран содержит каталог переменных и объявленный набор.
func TestTemplateEditRenderGolden(t *testing.T) {
	renderGoldenPage(t, newCardServer(t), "/templates/intro/edit", "template-edit.golden")
}

// TestTemplateFormErrorRenderGolden проверяет, что серверная ошибка
// необъявленного плейсхолдера показывается у формы. С флагом -update золотой файл
// перезаписывается.
func TestTemplateFormErrorRenderGolden(t *testing.T) {
	server := newCardServer(t)

	loginReq := httptest.NewRequest(http.MethodPost, "/login",
		strings.NewReader("username=operator&password=secret"))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginRec := httptest.NewRecorder()
	server.ServeHTTP(loginRec, loginReq)
	cookies := loginRec.Result().Cookies()

	formReq := httptest.NewRequest(http.MethodGet, "/templates", nil)
	for _, cookie := range cookies {
		formReq.AddCookie(cookie)
	}
	formRec := httptest.NewRecorder()
	server.ServeHTTP(formRec, formReq)
	csrf := csrfFromHTML(formRec.Body.String())
	if csrf == "" {
		t.Fatalf("не найден CSRF-токен:\n%s", formRec.Body.String())
	}

	body := url.Values{
		"name":             {"Плохой шаблон"},
		"subject":          {"Тема"},
		"body":             {"Привет, {{unknown}}!"},
		"var_company":      {"yes"},
		"fallback_company": {"ваша компания"},
		"csrf":             {csrf},
	}
	req := httptest.NewRequest(http.MethodPost, "/templates", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST с необъявленным плейсхолдером = %d, ожидался 400", rec.Code)
	}
	got := normalizeTokens(rec.Body.Bytes())

	golden := filepath.Join("testdata", "template-error.golden")
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatalf("запись золотого файла: %v", err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("чтение золотого файла %s: %v; запустите тест с -update", golden, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("рендер ошибки формы не совпадает с %s:\n--- получено ---\n%s", golden, got)
	}
}

// newAssistantServer собирает сервер экрана ассистента с фиксированным ответом
// диалога: критерии релевантности и технический план приходят из
// детерминированных фейков.
func newAssistantServer(t *testing.T) *httpapi.Server {
	t.Helper()
	profiles := memory.NewLeadProfileRepository()
	jobs := memory.NewSearchJobStore()
	llmPort := &fakeLLM{
		targets: []llm.Target{{Query: "логистические компании B2B"}},
		// Сбой вывода критериев: оператор видит причину вместо общего текста о
		// неопределённости.
		interpretation: &llm.Interpretation{Uncertainty: true, Reason: "провайдер интерпретации недоступен"},
		replies: []llm.ConverseReply{{
			Text:    "Собираю план: ищем B2B-логистов.",
			Targets: []llm.Target{{Query: "логистические компании B2B"}},
			Ready:   true,
		}},
	}
	runSearch := &app.RunSearch{
		Jobs:     jobs,
		Profiles: profiles,
		Contacts: memory.NewContactRepository(),
		Search:   &fakeSearch{},
		Crawl:    &fakeCrawl{},
		LLM:      llmPort,
		Clock:    fakeClock{t: testTime},
		Limits:   app.CrawlLimits{MaxPages: 25, MaxDepth: 3, MaxTimeSeconds: 120},
	}
	return httpapi.NewServer(httpapi.Dependencies{
		RunSearch: runSearch,
		Showcase: &app.Showcase{
			Jobs:     jobs,
			Profiles: profiles,
			Contacts: memory.NewContactRepository(),
			Messages: memory.NewOutboundMessageRepository(),
		},
		Dialogue: &app.ConverseDialogue{LLM: llmPort},
		Auth:     httpapi.AuthConfig{Username: "operator", Password: "secret"},
	})
}

// TestAssistantRenderGolden сравнивает отрисовку экрана ассистента после хода
// диалога с золотым файлом: критерии релевантности и технический план показаны
// отдельными блоками. С флагом -update золотой файл перезаписывается.
func TestAssistantRenderGolden(t *testing.T) {
	server := newAssistantServer(t)

	loginReq := httptest.NewRequest(http.MethodPost, "/login",
		strings.NewReader("username=operator&password=secret"))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginRec := httptest.NewRecorder()
	server.ServeHTTP(loginRec, loginReq)
	cookies := loginRec.Result().Cookies()

	// CSRF-токен берётся из отрисованной формы ассистента.
	formReq := httptest.NewRequest(http.MethodGet, "/assistant", nil)
	for _, cookie := range cookies {
		formReq.AddCookie(cookie)
	}
	formRec := httptest.NewRecorder()
	server.ServeHTTP(formRec, formReq)
	csrf := csrfFromHTML(formRec.Body.String())

	body := url.Values{
		"action":         {"turn"},
		"message":        {"ищем B2B-логистов"},
		"history":        {""},
		"plan":           {""},
		"csrf":           {csrf},
		"maxDepth":       {"3"},
		"maxTimeSeconds": {"120"},
	}
	req := httptest.NewRequest(http.MethodPost, "/assistant", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /assistant = %d, ожидался 200", rec.Code)
	}
	got := normalizeTokens(rec.Body.Bytes())

	golden := filepath.Join("testdata", "assistant.golden")
	if *update {
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			t.Fatalf("создание testdata: %v", err)
		}
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatalf("запись золотого файла: %v", err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("чтение золотого файла %s: %v; запустите тест с -update для генерации", golden, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("рендер ассистента не совпадает с золотым файлом %s\n--- получено ---\n%s\n--- ожидалось ---\n%s", golden, got, want)
	}
}
