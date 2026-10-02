package httpapi_test

import (
	"context"
	"errors"
	"io"
	"iter"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	httpapi "lead-gen/backend/contexts/lead-management/internal/adapters/http"
	"lead-gen/backend/contexts/lead-management/internal/adapters/persistence/memory"
	"lead-gen/backend/contexts/lead-management/internal/app"
	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/backend/platform/jobs"
	"lead-gen/contracts/integration-platform/crawl"
	"lead-gen/contracts/integration-platform/forum"
	"lead-gen/contracts/integration-platform/llm"
	"lead-gen/contracts/integration-platform/mail"
	"lead-gen/contracts/integration-platform/search"
)

// testTime — фиксированный момент для детерминированных тестов.
var testTime = time.Date(2026, time.January, 2, 10, 0, 0, 0, time.UTC)

// fakeClock — детерминированные часы.
type fakeClock struct{ t time.Time }

// Now возвращает зафиксированный момент.
func (c fakeClock) Now() time.Time { return c.t }

// fakeClock реализует порт часов.
var _ ports.Clock = fakeClock{}

// mutableClock — часы, момент которых можно двигать вручную. Нужны для проверки
// истечения сессии по неактивности.
type mutableClock struct {
	mu sync.Mutex
	t  time.Time
}

// Now возвращает текущий (двигаемый) момент.
func (c *mutableClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// advance сдвигает момент вперёд на d.
func (c *mutableClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// fakeSearch — минимальный фейк порта поиска.
type fakeSearch struct{}

// Search возвращает пустой результат.
func (f *fakeSearch) Search(context.Context, search.Query) ([]search.Result, error) {
	return nil, nil
}

// fakeCrawl — минимальный фейк порта обхода.
type fakeCrawl struct{}

// Crawl возвращает пустой исход обхода.
func (f *fakeCrawl) Crawl(context.Context, crawl.Request) (crawl.CrawlOutcome, error) {
	return crawl.CrawlOutcome{}, nil
}

// Extract возвращает пустой список кандидатов.
func (f *fakeCrawl) Extract(context.Context, crawl.ExtractRequest) ([]crawl.ContactCandidate, error) {
	return nil, nil
}

// fakeLLM — минимальный фейк порта LLM.
type fakeLLM struct {
	suggestions []llm.Suggestion
	targets     []llm.Target
	replies     []llm.ConverseReply
	replyIdx    int
	replyErr    error
	// interpretation — заготовленная интерпретация: если задана, Interpret
	// возвращает её (позволяет проверить показ Reason при сбое).
	interpretation *llm.Interpretation
}

// Plan возвращает заготовленные цели, а без них — исходный запрос единственной целью.
func (f *fakeLLM) Plan(_ context.Context, req llm.PlanRequest) ([]llm.Target, llm.Usage, error) {
	if len(f.targets) > 0 {
		return f.targets, llm.Usage{}, nil
	}
	return []llm.Target{{Query: req.Query}}, llm.Usage{}, nil
}

func (f *fakeLLM) Suggest(_ context.Context, _ llm.SuggestRequest) ([]llm.Suggestion, llm.Usage, error) {
	return f.suggestions, llm.Usage{}, nil
}

// Converse отдаёт заготовленные реплики по порядку, а когда они кончились —
// повторяет последнюю. Без заготовок возвращает нейтральный уточняющий вопрос.
func (f *fakeLLM) Converse(_ context.Context, _ llm.ConverseRequest) (llm.ConverseReply, llm.Usage, error) {
	if f.replyErr != nil {
		return llm.ConverseReply{}, llm.Usage{}, f.replyErr
	}
	if len(f.replies) == 0 {
		return llm.ConverseReply{Text: "какой регион нужен?"}, llm.Usage{}, nil
	}
	i := f.replyIdx
	if i >= len(f.replies) {
		i = len(f.replies) - 1
	}
	f.replyIdx++
	return f.replies[i], llm.Usage{}, nil
}

// ConverseStream отдаёт заготовленную реплику потоком: единый фрагмент текста и
// завершающий кадр с ответом.
func (f *fakeLLM) ConverseStream(_ context.Context, req llm.ConverseRequest) iter.Seq2[llm.ConverseDelta, error] {
	return func(yield func(llm.ConverseDelta, error) bool) {
		reply, usage, err := f.Converse(context.Background(), req)
		if err != nil {
			yield(llm.ConverseDelta{}, err)
			return
		}
		if !yield(llm.ConverseDelta{Text: reply.Text}, nil) {
			return
		}
		yield(llm.ConverseDelta{Reply: &reply, Usage: usage}, nil)
	}
}

// Interpret возвращает заготовленную интерпретацию, а без неё — минимальную:
// целевой лид — сам запрос, без выдуманных критериев.
func (f *fakeLLM) Interpret(_ context.Context, req llm.InterpretRequest) (llm.Interpretation, llm.Usage, error) {
	if f.interpretation != nil {
		return *f.interpretation, llm.Usage{}, nil
	}
	return llm.Interpretation{TargetLead: req.Query, NeedSignals: []string{req.Query}}, llm.Usage{}, nil
}

// Assess возвращает нейтральную гипотезу по совпадению профиля без
// подтверждающего свидетельства.
func (f *fakeLLM) Assess(_ context.Context, req llm.AssessRequest) (llm.Assessment, llm.Usage, error) {
	return llm.Assessment{
		Verdict:    llm.VerdictHypothesis,
		SignalKind: llm.SignalProfile,
		Evidence: []llm.AssessmentEvidence{{
			Criterion:  req.Interpretation.TargetLead,
			Reasoning:  "совпадение по профилю, сигнал потребности не найден",
			SignalKind: llm.SignalProfile,
			Missing:    "прямое заявление о потребности",
		}},
	}, llm.Usage{}, nil
}

// fakeForum — минимальный фейк порта форумного источника.
type fakeForum struct {
	signals []forum.Signal
}

// SearchSignals возвращает заготовленные сигналы.
func (f *fakeForum) SearchSignals(_ context.Context, _ forum.Query) ([]forum.Signal, error) {
	return f.signals, nil
}

// fakeMail — фейк порта доставки почты: фиксирует отправленные письма и всегда
// успешен.
type fakeMail struct {
	sent []mail.Message
}

// Send принимает сообщение к отправке.
func (m *fakeMail) Send(_ context.Context, message mail.Message) error {
	m.sent = append(m.sent, message)
	return nil
}

// fakeMail реализует порт доставки почты.
var _ mail.Port = (*fakeMail)(nil)

// testEnv — собранный HTTP-сервер с in-memory адаптерами и фейками портов.
type testEnv struct {
	profiles    *memory.LeadProfileRepository
	contacts    *memory.ContactRepository
	jobs        *memory.SearchJobStore
	messages    *memory.OutboundMessageRepository
	templates   *memory.EmailTemplateRepository
	activities  *memory.LeadActivityStore
	assessments *memory.AssessmentStore
	mail        *fakeMail
	llm         *fakeLLM
	forum       *fakeForum
	server      *httpapi.Server
}

// newTestEnv собирает HTTP-сервер на in-memory репозиториях с оператором
// «operator / secret».
func newTestEnv() *testEnv {
	return newTestEnvWithAuth(httpapi.AuthConfig{Username: "operator", Password: "secret"})
}

// newTestEnvWithAuth собирает HTTP-сервер с заданной конфигурацией аутентификации
// (например, укороченным IdleTimeout и управляемыми часами).
func newTestEnvWithAuth(auth httpapi.AuthConfig) *testEnv {
	profiles := memory.NewLeadProfileRepository()
	contacts := memory.NewContactRepository()
	jobs := memory.NewSearchJobStore()
	messages := memory.NewOutboundMessageRepository()
	templates := memory.NewEmailTemplateRepository()
	activities := memory.NewLeadActivityStore()
	assessments := memory.NewAssessmentStore()
	mailPort := &fakeMail{}
	llmPort := &fakeLLM{}
	forumPort := &fakeForum{}

	runSearch := &app.RunSearch{
		Jobs:        jobs,
		Profiles:    profiles,
		Contacts:    contacts,
		Search:      &fakeSearch{},
		Crawl:       &fakeCrawl{},
		LLM:         llmPort,
		Clock:       fakeClock{t: testTime},
		Limits:      app.CrawlLimits{MaxPages: 25, MaxDepth: 3, MaxTimeSeconds: 120},
		Assessments: assessments,
	}
	showcase := &app.Showcase{
		Jobs:     jobs,
		Profiles: profiles,
		Contacts: contacts,
		Messages: messages,
	}
	changeStage := &app.ChangeLeadStage{
		Profiles: profiles,
		Activity: activities,
		Clock:    fakeClock{t: testTime},
	}
	snooze := &app.SnoozeLead{
		Profiles: profiles,
		Clock:    fakeClock{t: testTime},
	}
	sendToLead := &app.SendToLead{
		Profiles:  profiles,
		Contacts:  contacts,
		Messages:  messages,
		Templates: templates,
		Mail:      mailPort,
		From:      "operator@example.com",
		Clock:     fakeClock{t: testTime},
	}
	sendToSegment := &app.SendToSegment{
		Profiles:  profiles,
		Contacts:  contacts,
		Messages:  messages,
		Templates: templates,
		Mail:      mailPort,
		From:      "operator@example.com",
		Clock:     fakeClock{t: testTime},
	}
	templateLibrary := &app.Templates{
		Repository: templates,
		Profiles:   profiles,
		Contacts:   contacts,
		Clock:      fakeClock{t: testTime},
	}

	server := httpapi.NewServer(httpapi.Dependencies{
		RunSearch:     runSearch,
		Showcase:      showcase,
		ChangeStage:   changeStage,
		Snooze:        snooze,
		SendToLead:    sendToLead,
		SendToSegment: sendToSegment,
		Templates:     templateLibrary,
		SuggestNiches: &app.SuggestNiches{LLM: llmPort},
		Dialogue:      &app.ConverseDialogue{LLM: llmPort},
		ForumSignals:  &app.SearchForumSignals{Forum: forumPort},
		Auth:          auth,
	})

	return &testEnv{
		profiles:    profiles,
		contacts:    contacts,
		jobs:        jobs,
		messages:    messages,
		templates:   templates,
		activities:  activities,
		assessments: assessments,
		mail:        mailPort,
		llm:         llmPort,
		forum:       forumPort,
		server:      server,
	}
}

// noRedirectClient возвращает клиент, не следующий за редиректами, чтобы проверять
// коды 303 и заголовок Location.
func noRedirectClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// consoleClient — HTTP-клиент консоли: хранит cookie сессии и подставляет её в
// запросы. Редиректы не следуются, поэтому коды 303 и заголовок Location видны
// тесту напрямую.
type consoleClient struct {
	base    string
	session string
}

// newConsoleClient создаёт клиента для запущенного тестового сервера.
func newConsoleClient(ts *httptest.Server) *consoleClient {
	return &consoleClient{base: ts.URL}
}

// login авторизует оператора и сохраняет cookie сессии.
func (c *consoleClient) login(t *testing.T, username, password string) {
	t.Helper()
	resp := c.postForm(t, "/login", url.Values{"username": {username}, "password": {password}})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /login = %d, ожидался 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Fatalf("Location после входа = %q, ожидался /", loc)
	}
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "session" {
			c.session = cookie.Value
			return
		}
	}
	t.Fatalf("сервер не вернул cookie сессии")
}

// do выполняет запрос, подставляя cookie сессии и (для форм) тело формы.
func (c *consoleClient) do(t *testing.T, method, path string, form url.Values) *http.Response {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, c.base+path, body)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if c.session != "" {
		req.AddCookie(&http.Cookie{Name: "session", Value: c.session})
	}
	resp, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

// get выполняет GET-запрос.
func (c *consoleClient) get(t *testing.T, path string) *http.Response {
	return c.do(t, http.MethodGet, path, nil)
}

// postForm выполняет POST-запрос с формой.
func (c *consoleClient) postForm(t *testing.T, path string, form url.Values) *http.Response {
	return c.do(t, http.MethodPost, path, form)
}

// csrfForPage открывает страницу и извлекает из неё CSRF-токен (hidden-поле
// name="csrf").
func (c *consoleClient) csrfForPage(t *testing.T, path string) string {
	t.Helper()
	resp := c.get(t, path)
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("GET %s = %d, ожидался 200", path, resp.StatusCode)
	}
	html := readBody(t, resp)
	token := csrfFromHTML(html)
	if token == "" {
		t.Fatalf("CSRF-токен не найден на %s:\n%s", path, html)
	}
	return token
}

// csrfInputRe — скрытое поле CSRF-токена.
var csrfInputRe = regexp.MustCompile(`<input[^>]*name="csrf"[^>]*>`)

// valueRe — атрибут value внутри поля.
var valueRe = regexp.MustCompile(`value="([^"]*)"`)

// csrfFromHTML извлекает CSRF-токен из разметки формы.
func csrfFromHTML(html string) string {
	input := csrfInputRe.FindString(html)
	if input == "" {
		return ""
	}
	match := valueRe.FindStringSubmatch(input)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}

// readBody читает и закрывает тело ответа, возвращая его строкой.
func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("чтение тела: %v", err)
	}
	return string(data)
}

// seedCompany сохраняет карточку компании с фиксированным происхождением.
func seedCompany(t *testing.T, profiles *memory.LeadProfileRepository, id, name, description string, socials ...string) {
	t.Helper()
	prov := domain.Provenance{SourceURL: "https://" + id, ExtractedAt: testTime}
	svals := make([]domain.AttributedValue, 0, len(socials))
	for _, s := range socials {
		svals = append(svals, domain.AttributedValue{Value: s, Provenance: prov})
	}
	company, err := domain.CreateCompany(domain.CreateCompanyParams{
		Domain:      id,
		Name:        domain.AttributedValue{Value: name, Provenance: prov},
		Description: domain.AttributedValue{Value: description, Provenance: prov},
		Socials:     svals,
		CreatedAt:   testTime,
	})
	if err != nil {
		t.Fatalf("CreateCompany: %v", err)
	}
	if err := profiles.Save(context.Background(), company); err != nil {
		t.Fatalf("Save company: %v", err)
	}
}

// seedContact сохраняет контакт компании с фиксированным происхождением.
func seedContact(t *testing.T, contacts *memory.ContactRepository, companyID, email, phone, role string) {
	t.Helper()
	prov := domain.AttributeProvenance{SourceURL: "https://" + companyID, ExtractedAt: testTime, Method: domain.MethodRegex}
	contact, err := domain.CreateContact(domain.CreateContactParams{
		ID:        email,
		CompanyID: domain.CompanyID(companyID),
		Email:     &domain.ContactAttribute{Value: email, Provenance: prov},
		Phone:     &domain.ContactAttribute{Value: phone, Provenance: prov},
		Role:      &domain.ContactAttribute{Value: role, Provenance: prov},
		CreatedAt: testTime,
	})
	if err != nil {
		t.Fatalf("CreateContact: %v", err)
	}
	if err := contacts.Save(context.Background(), contact); err != nil {
		t.Fatalf("Save contact: %v", err)
	}
}

// seedMessage сохраняет отправленное сообщение лиду, чтобы в карточке появилась
// история переписки.
func seedMessage(t *testing.T, messages *memory.OutboundMessageRepository, leadID, recipient, template, body string) {
	t.Helper()
	message, err := domain.CreateOutboundMessage(domain.CreateOutboundMessageParams{
		ID:        "msg-" + leadID,
		LeadID:    leadID,
		Recipient: recipient,
		Template:  template,
		Body:      body,
		CreatedAt: testTime,
	})
	if err != nil {
		t.Fatalf("CreateOutboundMessage: %v", err)
	}
	message.MarkSent(testTime)
	if err := messages.Save(context.Background(), message); err != nil {
		t.Fatalf("Save message: %v", err)
	}
}

// seedTemplate сохраняет шаблон библиотеки с объявленными переменными, чтобы
// его можно было выбрать при отправке.
func seedTemplate(t *testing.T, templates *memory.EmailTemplateRepository, id, name, subject, body string, variables ...domain.MergeVariable) {
	t.Helper()
	template, err := domain.CreateTemplate(domain.CreateTemplateParams{
		ID:        id,
		Name:      name,
		Subject:   subject,
		Body:      body,
		Variables: variables,
		CreatedAt: testTime,
	})
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	if err := templates.Save(context.Background(), template); err != nil {
		t.Fatalf("Save template: %v", err)
	}
}

// TestHomePage проверяет, что после входа главный экран отдаёт форму поиска и
// журнал заданий.
func TestHomePage(t *testing.T) {
	env := newTestEnv()
	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")

	resp := client.get(t, "/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / = %d, ожидался 200", resp.StatusCode)
	}
	html := readBody(t, resp)
	if !strings.Contains(html, "name=\"query\"") {
		t.Fatalf("главный экран не содержит форму поиска:\n%s", html)
	}
	if !strings.Contains(html, "Журнал заданий") {
		t.Fatalf("главный экран не содержит журнал заданий:\n%s", html)
	}
	if !strings.Contains(html, "htmx.min.js") {
		t.Fatalf("главный экран не подключает HTMX-скрипт:\n%s", html)
	}
	if !strings.Contains(html, `name="budget"`) {
		t.Fatalf("главный экран не содержит поле бюджета кредитов:\n%s", html)
	}
}

// TestUnauthenticatedRedirectsToLogin проверяет, что без сессии защищённые
// маршруты перенаправляют на страницу входа.
func TestUnauthenticatedRedirectsToLogin(t *testing.T) {
	env := newTestEnv()
	ts := httptest.NewServer(env.server)
	defer ts.Close()

	resp, err := noRedirectClient().Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("GET / без сессии = %d, ожидался 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/login" {
		t.Fatalf("Location = %q, ожидался /login", loc)
	}
}

// TestLoginRejectsBadPassword проверяет, что неверный пароль даёт 401.
func TestLoginRejectsBadPassword(t *testing.T) {
	env := newTestEnv()
	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)

	resp := client.postForm(t, "/login", url.Values{"username": {"operator"}, "password": {"неверный"}})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("POST /login с неверным паролем = %d, ожидался 401", resp.StatusCode)
	}
	if html := readBody(t, resp); !strings.Contains(html, "Неверный логин или пароль") {
		t.Fatalf("страница входа не показывает ошибку:\n%s", html)
	}
}

// TestLogoutInvalidatesSession проверяет, что после /logout сессия невалидна.
func TestLogoutInvalidatesSession(t *testing.T) {
	env := newTestEnv()
	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")

	before := client.get(t, "/")
	if before.StatusCode != http.StatusOK {
		t.Fatalf("GET / до выхода = %d, ожидался 200", before.StatusCode)
	}
	before.Body.Close()

	out := client.get(t, "/logout")
	if out.StatusCode != http.StatusSeeOther {
		t.Fatalf("GET /logout = %d, ожидался 303", out.StatusCode)
	}
	if loc := out.Header.Get("Location"); loc != "/login" {
		t.Fatalf("Location после выхода = %q, ожидался /login", loc)
	}
	out.Body.Close()

	after := client.get(t, "/")
	if after.StatusCode != http.StatusSeeOther {
		t.Fatalf("GET / после выхода = %d, ожидался 303", after.StatusCode)
	}
	if loc := after.Header.Get("Location"); loc != "/login" {
		t.Fatalf("Location после выхода = %q, ожидался /login", loc)
	}
	after.Body.Close()
}

// TestCSRFRequired проверяет, что POST без CSRF-токена или с неверным даёт 403.
func TestCSRFRequired(t *testing.T) {
	env := newTestEnv()
	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")

	noToken := client.postForm(t, "/search", url.Values{"query": {"тест"}})
	if noToken.StatusCode != http.StatusForbidden {
		t.Fatalf("POST /search без CSRF = %d, ожидался 403", noToken.StatusCode)
	}
	noToken.Body.Close()

	badToken := client.postForm(t, "/search", url.Values{"query": {"тест"}, "csrf": {"неверный-токен"}})
	if badToken.StatusCode != http.StatusForbidden {
		t.Fatalf("POST /search с неверным CSRF = %d, ожидался 403", badToken.StatusCode)
	}
	badToken.Body.Close()
}

// TestSessionExpires проверяет, что после периода неактивности запрос редиректит
// на /login.
func TestSessionExpires(t *testing.T) {
	clock := &mutableClock{t: testTime}
	env := newTestEnvWithAuth(httpapi.AuthConfig{
		Username:    "operator",
		Password:    "secret",
		IdleTimeout: time.Minute,
		Now:         clock.Now,
	})
	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")

	fresh := client.get(t, "/")
	if fresh.StatusCode != http.StatusOK {
		t.Fatalf("GET / сразу после входа = %d, ожидался 200", fresh.StatusCode)
	}
	fresh.Body.Close()

	// Двигаем часы за пределы периода неактивности.
	clock.advance(2 * time.Minute)

	expired := client.get(t, "/")
	if expired.StatusCode != http.StatusSeeOther {
		t.Fatalf("GET / после истечения = %d, ожидался 303", expired.StatusCode)
	}
	if loc := expired.Header.Get("Location"); loc != "/login" {
		t.Fatalf("Location после истечения = %q, ожидался /login", loc)
	}
	expired.Body.Close()
}

// TestJobsFragment проверяет, что HTMX-фрагмент журнала отдаётся отдельно: без
// каркаса страницы, с атрибутом живого обновления при наличии незавершённого
// задания и кнопкой отмены.
func TestJobsFragment(t *testing.T) {
	env := newTestEnv()
	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")

	// Без активных заданий авто-обновление не включается.
	resp := client.get(t, "/jobs")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /jobs = %d, ожидался 200", resp.StatusCode)
	}
	html := readBody(t, resp)
	if strings.Contains(html, `hx-get="/jobs"`) {
		t.Fatalf("при отсутствии активных заданий фрагмент не должен опрашивать /jobs:\n%s", html)
	}
	if !strings.Contains(html, "Журнал заданий") {
		t.Fatalf("фрагмент журнала не содержит заголовок:\n%s", html)
	}
	if strings.Contains(html, "<html") {
		t.Fatalf("фрагмент не должен быть полной страницей:\n%s", html)
	}

	// С незавершённым заданием включаются опрос и кнопка отмены.
	if _, err := env.jobs.CreateSearchJob(context.Background(), "запрос", 0); err != nil {
		t.Fatalf("CreateSearchJob: %v", err)
	}
	running := client.get(t, "/jobs")
	body := readBody(t, running)
	if !strings.Contains(body, `hx-get="/jobs"`) || !strings.Contains(body, `hx-trigger="every 2s"`) {
		t.Fatalf("с активным заданием фрагмент должен опрашивать /jobs:\n%s", body)
	}
	if !strings.Contains(body, `action="/jobs/cancel"`) {
		t.Fatalf("с активным заданием фрагмент должен предлагать групповую отмену:\n%s", body)
	}
	if !strings.Contains(body, `name="ids"`) || !strings.Contains(body, "data-select-job") {
		t.Fatalf("с активным заданием фрагмент должен давать чекбоксы выбора заданий:\n%s", body)
	}
}

// TestSearchEnqueuesJob проверяет, что POST /search ставит задание и перенаправляет
// на главный экран, где задание видно со статусом queued.
func TestSearchEnqueuesJob(t *testing.T) {
	env := newTestEnv()
	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")
	csrf := client.csrfForPage(t, "/")

	resp := client.postForm(t, "/search", url.Values{"query": {"разработчики Go"}, "csrf": {csrf}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /search = %d, ожидался 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Fatalf("Location = %q, ожидался /", loc)
	}
	resp.Body.Close()

	// Журнал должен показывать поставленное задание с запросом и состоянием queued.
	home := client.get(t, "/")
	html := readBody(t, home)
	if !strings.Contains(html, "разработчики Go") {
		t.Fatalf("журнал не содержит запрос задания:\n%s", html)
	}
	if !strings.Contains(html, "status-queued") || !strings.Contains(html, "queued") {
		t.Fatalf("журнал не показывает состояние queued:\n%s", html)
	}
}

// TestSearchSavesInterpretation проверяет, что запуск с главной идёт интерпретируемым
// путём: критерии релевантности сохраняются в задании, а не теряются.
func TestSearchSavesInterpretation(t *testing.T) {
	env := newTestEnv()
	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")
	csrf := client.csrfForPage(t, "/")

	resp := client.postForm(t, "/search", url.Values{"query": {"разработчики Go"}, "csrf": {csrf}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /search = %d, ожидался 303", resp.StatusCode)
	}
	resp.Body.Close()

	jobsList, err := env.jobs.ListSearchJobs(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListSearchJobs: %v", err)
	}
	if len(jobsList) != 1 {
		t.Fatalf("задания после запуска: %+v", jobsList)
	}
	if jobsList[0].Interpretation == nil || jobsList[0].Interpretation.TargetLead != "разработчики Go" {
		t.Fatalf("критерии запуска с главной не сохранены: %+v", jobsList[0].Interpretation)
	}
}

// TestJobsFragmentCompactRow проверяет, что строка журнала компактна: раскрытие,
// короткий запрос и ссылка на экран задания, а полный запрос в строке не выводится.
func TestJobsFragmentCompactRow(t *testing.T) {
	env := newTestEnv()
	long := strings.Repeat("запрос ", 20)
	if _, err := env.jobs.CreateSearchJob(context.Background(), long, 0); err != nil {
		t.Fatalf("CreateSearchJob: %v", err)
	}

	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")

	body := readBody(t, client.get(t, "/jobs"))
	for _, want := range []string{"journal-row", "data-job-detail", "Открыть полностью", "/jobs/"} {
		if !strings.Contains(body, want) {
			t.Fatalf("компактная строка журнала не содержит %q:\n%s", want, body)
		}
	}
	if !strings.Contains(body, strings.TrimSpace(long)) {
		t.Fatalf("полный запрос должен быть доступен при раскрытии строки:\n%s", body)
	}

	// В компактной строке (summary) полного запроса быть не должно — только
	// сокращённое представление. Полный запрос живёт в раскрытии.
	summary := body
	if start := strings.Index(body, "journal-summary"); start >= 0 {
		if end := strings.Index(body[start:], "</summary>"); end >= 0 {
			summary = body[start : start+end]
		}
	}
	if strings.Contains(summary, strings.TrimSpace(long)) {
		t.Fatalf("компактная строка не должна показывать полный запрос:\n%s", summary)
	}
	if !strings.Contains(summary, "…") {
		t.Fatalf("компактная строка должна показывать сокращённый запрос:\n%s", summary)
	}
}

// TestJobScreen проверяет экран подробностей задания: полный запрос, найденные
// компании и ход выполнения по событиям жизненного цикла.
func TestJobScreen(t *testing.T) {
	env := newTestEnv()
	job, err := env.jobs.CreateSearchJob(context.Background(), "разработчики Go", 0)
	if err != nil {
		t.Fatalf("CreateSearchJob: %v", err)
	}
	if err := env.jobs.RecordResult(context.Background(), job.ID, []string{"a.example"}); err != nil {
		t.Fatalf("RecordResult: %v", err)
	}
	if err := env.jobs.RecordEvent(context.Background(), ports.SearchJobEvent{
		JobID:     job.ID,
		Type:      ports.EventPhase,
		CreatedAt: testTime,
		Progress:  &ports.SearchJobProgress{Phase: "crawling", PagesCrawled: 3, CompaniesFound: 1},
	}); err != nil {
		t.Fatalf("RecordEvent: %v", err)
	}

	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")

	resp := client.get(t, "/jobs/"+job.ID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /jobs/{id} = %d, ожидался 200", resp.StatusCode)
	}
	html := readBody(t, resp)
	for _, want := range []string{"разработчики Go", "Фаза: обход", "страниц 3", "/cards/a.example"} {
		if !strings.Contains(html, want) {
			t.Fatalf("экран задания не содержит %q:\n%s", want, html)
		}
	}
}

// TestJobScreenAutoRefresh проверяет, что экран незавершённого задания несёт
// htmx-опрос (состав пополняется без ручной перезагрузки), а терминальное задание
// опроса не получает.
func TestJobScreenAutoRefresh(t *testing.T) {
	env := newTestEnv()
	running, err := env.jobs.CreateSearchJob(context.Background(), "агентства", 0)
	if err != nil {
		t.Fatalf("CreateSearchJob: %v", err)
	}

	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")

	runningHTML := readBody(t, client.get(t, "/jobs/"+running.ID))
	if !strings.Contains(runningHTML, "hx-get=\"/jobs/"+running.ID+"\"") || !strings.Contains(runningHTML, "every 2s") {
		t.Fatalf("экран незавершённого задания не несёт опрос:\n%s", runningHTML)
	}

	doneStore := goldenJobs{job: ports.SearchJob{
		ID:        "job-done",
		Query:     "завершённое задание",
		Status:    jobs.StatusDone,
		CreatedAt: testTime,
		UpdatedAt: testTime,
	}}
	doneServer := httpapi.NewServer(httpapi.Dependencies{
		Showcase: &app.Showcase{Jobs: doneStore},
		Auth:     httpapi.AuthConfig{Username: "operator", Password: "secret"},
	})
	doneTS := httptest.NewServer(doneServer)
	defer doneTS.Close()
	doneClient := newConsoleClient(doneTS)
	doneClient.login(t, "operator", "secret")

	doneHTML := readBody(t, doneClient.get(t, "/jobs/job-done"))
	if strings.Contains(doneHTML, "hx-get=\"/jobs/job-done\"") {
		t.Fatalf("терминальное задание не должно опрашиваться:\n%s", doneHTML)
	}
}

// TestJobScreenShowsEmails проверяет, что экран задания показывает email найденных
// компаний и подсвечивает компанию без адреса, не выдавая отсутствие письма за
// ошибку чтения.
func TestJobScreenShowsEmails(t *testing.T) {
	env := newTestEnv()
	seedCompany(t, env.profiles, "a.example", "С Email", "Описание")
	seedContact(t, env.contacts, "a.example", "hello@a.example", "", "основатель")
	seedCompany(t, env.profiles, "b.example", "Без Email", "Описание")
	job, err := env.jobs.CreateSearchJob(context.Background(), "агентства", 0)
	if err != nil {
		t.Fatalf("CreateSearchJob: %v", err)
	}
	if err := env.jobs.RecordResult(context.Background(), job.ID, []string{"a.example", "b.example"}); err != nil {
		t.Fatalf("RecordResult: %v", err)
	}

	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")

	html := readBody(t, client.get(t, "/jobs/"+job.ID))
	for _, want := range []string{
		"hello@a.example",
		"is-no-email",
		"нет email",
		"b.example — нет email",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("экран задания не содержит %q:\n%s", want, html)
		}
	}
}

// failingContacts — контактный репозиторий, падающий при пакетном чтении: нужен,
// чтобы проверить, что ошибка чтения email не выдаётся за «нет email».
type failingContacts struct{ ports.ContactRepository }

func (failingContacts) ListByCompanies(context.Context, []domain.CompanyID) ([]*domain.Contact, error) {
	return nil, errors.New("чтение контактов недоступно")
}

// TestJobScreenShowsEmailUnavailable проверяет, что ошибка пакетного чтения email
// показывается явной пометкой «email недоступен», а не выдаётся за «нет email».
func TestJobScreenShowsEmailUnavailable(t *testing.T) {
	store := goldenJobs{job: ports.SearchJob{
		ID:        "job-fail",
		Query:     "агентства",
		Status:    jobs.StatusDone,
		ResultRef: []string{"a.example"},
		CreatedAt: testTime,
		UpdatedAt: testTime,
	}}
	server := httpapi.NewServer(httpapi.Dependencies{
		Showcase: &app.Showcase{Jobs: store, Contacts: failingContacts{}},
		Auth:     httpapi.AuthConfig{Username: "operator", Password: "secret"},
	})
	ts := httptest.NewServer(server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")

	html := readBody(t, client.get(t, "/jobs/job-fail"))
	if !strings.Contains(html, "email недоступен") {
		t.Fatalf("экран задания не помечает email недоступным:\n%s", html)
	}
	if strings.Contains(html, "нет email") {
		t.Fatalf("экран задания выдаёт ошибку чтения за «нет email»:\n%s", html)
	}
}

// TestJobScreenWithoutEvents проверяет, что задание без событий показывает
// состояние и пометку об отсутствии хода выполнения, не завершаясь ошибкой.
// Хранилище задаётся фиксированным: memory-адаптер сам пишет событие enqueued,
// поэтому «задание без событий» задаётся подставным хранилищем.
func TestJobScreenWithoutEvents(t *testing.T) {
	store := goldenJobs{job: ports.SearchJob{
		ID:        "job-empty",
		Query:     "запрос без событий",
		Status:    jobs.StatusQueued,
		CreatedAt: testTime,
		UpdatedAt: testTime,
	}}
	server := httpapi.NewServer(httpapi.Dependencies{
		Showcase: &app.Showcase{Jobs: store},
		Auth:     httpapi.AuthConfig{Username: "operator", Password: "secret"},
	})
	ts := httptest.NewServer(server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")

	html := readBody(t, client.get(t, "/jobs/job-empty"))
	if !strings.Contains(html, "Ход выполнения не записан") {
		t.Fatalf("экран задания без событий не содержит пометку:\n%s", html)
	}
	if !strings.Contains(html, "status-queued") {
		t.Fatalf("экран задания без событий не показывает состояние:\n%s", html)
	}
}

// TestJobScreenNotFound проверяет, что неизвестный идентификатор даёт нейтральное
// уведомление 404 без внутренних деталей.
func TestJobScreenNotFound(t *testing.T) {
	env := newTestEnv()
	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")

	resp := client.get(t, "/jobs/unknown-job")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /jobs/unknown-job = %d, ожидался 404", resp.StatusCode)
	}
	html := readBody(t, resp)
	if !strings.Contains(html, "не найдено") {
		t.Fatalf("уведомление о ненайденном задании:\n%s", html)
	}
}

// TestCardsList проверяет, что список карточек перечисляет сохранённые карточки в
// виде плиток по умолчанию, несёт переключатель представления, показывает email в
// разметке строк и выделяет компанию без email.
func TestCardsList(t *testing.T) {
	env := newTestEnv()
	seedCompany(t, env.profiles, "example.com", "Пример Компании", "Описание компании")
	seedContact(t, env.contacts, "example.com", "hello@example.com", "+7 999 123-45-67", "основатель")
	seedCompany(t, env.profiles, "phone.example.com", "Только Телефон", "Без адреса")

	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")

	resp := client.get(t, "/cards")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cards = %d, ожидался 200", resp.StatusCode)
	}
	html := readBody(t, resp)
	for _, want := range []string{
		"Пример Компании",
		"hello@example.com",
		"data-cards-view=\"tiles\"",
		"data-view-set=\"tiles\"",
		"data-view-set=\"rows\"",
		"/cards/example.com",
		"is-no-email",
		"нет email",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("список карточек не содержит %q:\n%s", want, html)
		}
	}
}

// TestCardsCompositionIndependentOfView проверяет две независимые оси списка
// карточек: состав результатов (по умолчанию «Подходящие», вторая группа —
// переключением) и представление «плитки/строки». Каждая ось ведёт свои атрибуты
// контейнера и своё переключение: смена одной не сбрасывает другую.
func TestCardsCompositionIndependentOfView(t *testing.T) {
	env := newTestEnv()
	seedCompany(t, env.profiles, "example.com", "Пример Компании", "Описание компании")

	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")

	html := readBody(t, client.get(t, "/cards"))
	for _, want := range []string{
		"data-cards-view=\"tiles\"",
		"data-cards-composition=\"fits\"",
		"data-view-set=\"tiles\"",
		"data-view-set=\"rows\"",
		"data-composition-set=\"fits\"",
		"data-composition-set=\"other\"",
		"composition-switch",
		"view-switch",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("список карточек не несёт независимую ось %q:\n%s", want, html)
		}
	}
}

// TestCardsJobScopedComposition проверяет, что список в контексте задания несёт
// его оценки: «Подходящие» по умолчанию, гипотезы и несоответствия — во второй
// группе с подписью. Неизвестное задание даёт 404.
func TestCardsJobScopedComposition(t *testing.T) {
	env := newTestEnv()
	seedCompany(t, env.profiles, "fit.example", "Подходящая", "Описание")
	seedCompany(t, env.profiles, "hypo.example", "Гипотеза", "Описание")
	seedCompany(t, env.profiles, "none.example", "Без оценки", "Описание")
	job, err := env.jobs.CreateSearchJob(context.Background(), "запрос задания", 0)
	if err != nil {
		t.Fatalf("CreateSearchJob: %v", err)
	}
	if err := env.jobs.RecordResult(context.Background(), job.ID, []string{"fit.example", "hypo.example", "none.example"}); err != nil {
		t.Fatalf("RecordResult: %v", err)
	}
	seedAssessment(t, env.assessments, job.ID, "fit.example", domain.VerdictFits, domain.SignalDirect, []domain.Evidence{{
		Criterion: "критерий", Fact: "факт", SourceURL: "https://fit.example/s", Reasoning: "связь", SignalKind: domain.SignalDirect,
	}})
	seedAssessment(t, env.assessments, job.ID, "hypo.example", domain.VerdictHypothesis, domain.SignalProfile, []domain.Evidence{{
		Criterion: "критерий", Fact: "профиль", SignalKind: domain.SignalProfile, Missing: "прямое заявление",
	}})

	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")

	html := readBody(t, client.get(t, "/cards?job="+url.QueryEscape(job.ID)))
	for _, want := range []string{
		"data-group=\"fits\"",
		"data-group=\"other\"",
		"verdict-hypothesis",
		"Гипотеза",
		"fit.example",
		"hypo.example",
		"Не оценено",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("список задания не содержит %q:\n%s", want, html)
		}
	}

	missing := client.get(t, "/cards?job=unknown")
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /cards?job=unknown = %d, ожидался 404", missing.StatusCode)
	}
	missing.Body.Close()
}

// TestCardAssessmentScopedToJob проверяет запрет переноса: одна компания имеет
// разные оценки в двух заданиях, и карточка показывает вердикт того задания, в
// контексте которого открыта. Без задания чужая оценка не подставляется.
// TestCardsGlobalComposition проверяет, что общая витрина без контекста задания
// делит карточки по последней оценке: «Подходит» — в группе по умолчанию,
// «Не подходит» — во второй группе, а компания без оценок — во второй группе с
// пометкой «Не оценено». Оценки берутся по всем заданиям, а не только из одного.
func TestCardsGlobalComposition(t *testing.T) {
	env := newTestEnv()
	seedCompany(t, env.profiles, "fit.example", "Подходящая", "Описание")
	seedCompany(t, env.profiles, "unsuitable.example", "Не подходит", "Описание")
	seedCompany(t, env.profiles, "none.example", "Без оценки", "Описание")
	seedAssessment(t, env.assessments, "job-1", "fit.example", domain.VerdictFits, domain.SignalDirect, []domain.Evidence{{
		Criterion: "критерий", Fact: "факт", SourceURL: "https://fit.example/s", Reasoning: "связь", SignalKind: domain.SignalDirect,
	}})
	seedAssessment(t, env.assessments, "job-2", "unsuitable.example", domain.VerdictUnsuitable, domain.SignalProfile, []domain.Evidence{{
		Criterion: "критерий", Fact: "факт", SourceURL: "https://unsuitable.example/s", Reasoning: "связь", SignalKind: domain.SignalProfile, Mismatch: "не та отрасль",
	}})

	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")

	html := readBody(t, client.get(t, "/cards"))
	for _, want := range []string{
		"data-group=\"fits\"",
		"data-group=\"other\"",
		"verdict-unsuitable",
		"Не подходит",
		"Не оценено",
		"fit.example",
		"unsuitable.example",
		"none.example",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("общая витрина не содержит %q:\n%s", want, html)
		}
	}
}

func TestCardAssessmentScopedToJob(t *testing.T) {
	env := newTestEnv()
	seedCompany(t, env.profiles, "example.com", "Пример Компании", "Описание")
	seedAssessment(t, env.assessments, "job-a", "example.com", domain.VerdictHypothesis, domain.SignalProfile, []domain.Evidence{{
		Criterion: "критерий A", Fact: "профиль", SignalKind: domain.SignalProfile, Missing: "прямое заявление",
	}})
	seedAssessment(t, env.assessments, "job-b", "example.com", domain.VerdictUnsuitable, domain.SignalProfile, []domain.Evidence{{
		Criterion: "критерий B", Fact: "факт", SignalKind: domain.SignalProfile, Mismatch: "несоответствие запросу",
	}})

	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")

	jobA := readBody(t, client.get(t, "/cards/example.com?job=job-a"))
	if !strings.Contains(jobA, "verdict-hypothesis") || strings.Contains(jobA, "verdict-unsuitable") {
		t.Fatalf("карточка в задании A показала не свою оценку:\n%s", jobA)
	}

	jobB := readBody(t, client.get(t, "/cards/example.com?job=job-b"))
	if !strings.Contains(jobB, "verdict-unsuitable") || strings.Contains(jobB, "verdict-hypothesis") {
		t.Fatalf("карточка в задании B показала не свою оценку:\n%s", jobB)
	}

	// Без контекста задания оценка не подставляется как свойство компании.
	general := readBody(t, client.get(t, "/cards/example.com"))
	if strings.Contains(general, "verdict-hypothesis") || strings.Contains(general, "verdict-unsuitable") {
		t.Fatalf("общая карточка подставила оценку из задания:\n%s", general)
	}
	if !strings.Contains(general, "Оценка привязана к заданию") {
		t.Fatalf("общая карточка не объясняет отсутствие оценки:\n%s", general)
	}
}

// TestAssistantInterpretationSeparateFromPlan проверяет, что критерии
// релевантности и технический план показаны отдельными блоками экрана
// согласования (search-strategy, требование интерпретации запроса).
func TestAssistantInterpretationSeparateFromPlan(t *testing.T) {
	env := newTestEnv()
	env.llm.replies = []llm.ConverseReply{{
		Text:    "Собираю план.",
		Targets: []llm.Target{{Query: "логистические компании B2B"}},
		Ready:   true,
	}}

	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")
	csrf := client.csrfForPage(t, "/")

	resp := client.postForm(t, "/assistant", url.Values{
		"action":  {"turn"},
		"message": {"ищем B2B-логистов"},
		"history": {""},
		"csrf":    {csrf},
	})
	html := readBody(t, resp)
	for _, want := range []string{"data-interpretation-section", "data-plan-section", "data-interpretation=\"target-lead\"", "data-plan-targets"} {
		if !strings.Contains(html, want) {
			t.Fatalf("экран согласования не содержит блок %q:\n%s", want, html)
		}
	}
	interp := strings.Index(html, "data-interpretation-section")
	plan := strings.Index(html, "data-plan-section")
	if interp < 0 || plan < 0 || interp > plan {
		t.Fatalf("интерпретация и план не являются отдельными блоками: interp=%d plan=%d", interp, plan)
	}
	if !strings.Contains(html, "ищем B2B-логистов") {
		t.Fatalf("интерпретация не показывает целевого лида:\n%s", html)
	}
}

// TestCard проверяет отрисовку карточки и 404 для неизвестного идентификатора.
func TestCard(t *testing.T) {
	env := newTestEnv()
	seedCompany(t, env.profiles, "example.com", "Пример Компании", "Описание компании")
	seedContact(t, env.contacts, "example.com", "hello@example.com", "+7 999 123-45-67", "основатель")

	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")

	resp := client.get(t, "/cards/example.com")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cards/example.com = %d, ожидался 200", resp.StatusCode)
	}
	html := readBody(t, resp)
	for _, want := range []string{"Пример Компании", "Описание компании", "hello@example.com", "999 123-45-67", "основатель"} {
		if !strings.Contains(html, want) {
			t.Fatalf("карточка не содержит %q:\n%s", want, html)
		}
	}

	// Неизвестный идентификатор — 404.
	missing := client.get(t, "/cards/unknown.com")
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /cards/unknown.com = %d, ожидался 404", missing.StatusCode)
	}
	missing.Body.Close()
}

// TestCardShowsStatusAndHistory проверяет, что карточка показывает стадию и
// историю переписки.
func TestCardShowsStatusAndHistory(t *testing.T) {
	env := newTestEnv()
	seedCompany(t, env.profiles, "example.com", "Пример Компании", "Описание компании")
	seedContact(t, env.contacts, "example.com", "hello@example.com", "+7 999 123-45-67", "основатель")
	seedMessage(t, env.messages, "example.com", "hello@example.com", "intro", "Здравствуйте!")

	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")

	resp := client.get(t, "/cards/example.com")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cards/example.com = %d, ожидался 200", resp.StatusCode)
	}
	html := readBody(t, resp)
	if !strings.Contains(html, "not warmed") {
		t.Fatalf("карточка не показывает стадию «not warmed»:\n%s", html)
	}
	if !strings.Contains(html, "intro") {
		t.Fatalf("карточка не показывает шаблон письма в истории:\n%s", html)
	}
	if !strings.Contains(html, "hello@example.com") {
		t.Fatalf("карточка не показывает получателя в истории:\n%s", html)
	}
}

// TestFunnel проверяет, что воронка содержит подписи стадий и лидов.
func TestFunnel(t *testing.T) {
	env := newTestEnv()
	seedCompany(t, env.profiles, "example.com", "Пример Компании", "Описание компании")

	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")

	resp := client.get(t, "/funnel")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /funnel = %d, ожидался 200", resp.StatusCode)
	}
	html := readBody(t, resp)
	for _, label := range []string{"Не прогрет", "Письмо отправлено", "Прогрев", "Холодный"} {
		if !strings.Contains(html, label) {
			t.Fatalf("воронка не содержит подпись стадии %q:\n%s", label, html)
		}
	}
	if !strings.Contains(html, "Пример Компании") {
		t.Fatalf("воронка не содержит лида:\n%s", html)
	}
}

// TestChangeStage проверяет перенос лида между стадиями воронки.
func TestChangeStage(t *testing.T) {
	env := newTestEnv()
	seedCompany(t, env.profiles, "example.com", "Пример Компании", "Описание компании")

	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")
	csrf := client.csrfForPage(t, "/funnel")

	resp := client.postForm(t, "/funnel/example.com/stage", url.Values{"stage": {"warming"}, "csrf": {csrf}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /funnel/example.com/stage = %d, ожидался 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/funnel" {
		t.Fatalf("Location = %q, ожидался /funnel", loc)
	}
	resp.Body.Close()
}

// TestSendToLead проверяет отправку письма лиду по шаблону библиотеки с
// фейковым портом почты.
func TestSendToLead(t *testing.T) {
	env := newTestEnv()
	seedCompany(t, env.profiles, "example.com", "Пример Компании", "Описание компании")
	seedContact(t, env.contacts, "example.com", "hello@example.com", "+7 999 123-45-67", "основатель")
	seedTemplate(t, env.templates, "intro", "Приветствие", "Тема для {{company}}", "Здравствуйте, {{company}}!",
		domain.MergeVariable{Key: domain.MergeVariableCompany, Fallback: "ваша компания"},
	)

	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")
	csrf := client.csrfForPage(t, "/cards/example.com")

	resp := client.postForm(t, "/cards/example.com/send", url.Values{
		"template": {"intro"},
		"key":      {"send-key-1"},
		"csrf":     {csrf},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /cards/example.com/send = %d, ожидался 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/cards/example.com" {
		t.Fatalf("Location = %q, ожидался /cards/example.com", loc)
	}
	resp.Body.Close()

	if len(env.mail.sent) != 1 {
		t.Fatalf("письмо не доставлено: sent = %d, ожидалось 1", len(env.mail.sent))
	}
	if env.mail.sent[0].Body != "Здравствуйте, Пример Компании!" {
		t.Fatalf("тело письма = %q", env.mail.sent[0].Body)
	}
}

// TestSendForm проверяет, что экран отправки содержит выбор шаблона и список
// шаблонов библиотеки.
func TestSendForm(t *testing.T) {
	env := newTestEnv()
	seedTemplate(t, env.templates, "intro", "Приветствие", "Тема для {{company}}", "Здравствуйте, {{company}}!",
		domain.MergeVariable{Key: domain.MergeVariableCompany, Fallback: "ваша компания"},
	)

	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")

	resp := client.get(t, "/send")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /send = %d, ожидался 200", resp.StatusCode)
	}
	html := readBody(t, resp)
	for _, want := range []string{`name="template"`, "Приветствие", "Библиотека шаблонов"} {
		if !strings.Contains(html, want) {
			t.Fatalf("экран отправки не содержит %q:\n%s", want, html)
		}
	}
}

// TestHideCard проверяет скрытие карточки и перенаправление на таблицу.
func TestHideCard(t *testing.T) {
	env := newTestEnv()
	seedCompany(t, env.profiles, "example.com", "Пример Компании", "Описание компании")

	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")
	csrf := client.csrfForPage(t, "/cards/example.com")

	resp := client.postForm(t, "/cards/example.com/hide", url.Values{"csrf": {csrf}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /cards/example.com/hide = %d, ожидался 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/cards" {
		t.Fatalf("Location = %q, ожидался /cards", loc)
	}
	resp.Body.Close()

	// Скрытая карточка больше не отображается в таблице.
	cardsResp := client.get(t, "/cards")
	if html := readBody(t, cardsResp); strings.Contains(html, "Пример Компании") {
		t.Fatalf("скрытая карточка всё ещё видна в таблице:\n%s", html)
	}
}

// TestDeleteCard проверяет безвозвратное удаление карточки и перенаправление.
func TestDeleteCard(t *testing.T) {
	env := newTestEnv()
	seedCompany(t, env.profiles, "example.com", "Пример Компании", "Описание компании")

	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")
	csrf := client.csrfForPage(t, "/cards/example.com")

	resp := client.postForm(t, "/cards/example.com/delete", url.Values{"csrf": {csrf}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /cards/example.com/delete = %d, ожидался 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/cards" {
		t.Fatalf("Location = %q, ожидался /cards", loc)
	}
	resp.Body.Close()

	// Удалённая карточка недоступна.
	cardResp := client.get(t, "/cards/example.com")
	if cardResp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /cards/example.com = %d, ожидался 404", cardResp.StatusCode)
	}
	cardResp.Body.Close()
}

// TestAssistantScreen проверяет экран ассистента: ход диалога даёт реплику агента
// и наполняет план, а подтверждение ставит задание с планом в очередь поиска.
func TestAssistantScreen(t *testing.T) {
	env := newTestEnv()
	env.llm.targets = []llm.Target{{Query: "логистические компании"}}
	env.llm.replies = []llm.ConverseReply{{Text: "Какой регион нужен?", Targets: []llm.Target{{Query: "логистические компании"}}}}

	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")
	csrf := client.csrfForPage(t, "/")

	// Ход диалога → реплика агента и черновик плана.
	resp := client.postForm(t, "/assistant", url.Values{
		"action":  {"turn"},
		"message": {"ищем логистов"},
		"history": {""},
		"csrf":    {csrf},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /assistant (turn) = %d, ожидался 200", resp.StatusCode)
	}
	html := readBody(t, resp)
	if !strings.Contains(html, "Какой регион нужен?") {
		t.Fatalf("реплика агента не отображена:\n%s", html)
	}
	if !strings.Contains(html, "логистические компании") {
		t.Fatalf("план обхода не отображён:\n%s", html)
	}

	// Подтверждение → задание с планом в очереди.
	history := `[{"role":"user","content":"ищем логистов"}]`
	resp = client.postForm(t, "/assistant", url.Values{
		"action":         {"launch"},
		"history":        {history},
		"maxDepth":       {"2"},
		"maxTimeSeconds": {"90"},
		"csrf":           {csrf},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /assistant (launch) = %d, ожидался 303", resp.StatusCode)
	}
	resp.Body.Close()

	jobs, err := env.jobs.ListSearchJobs(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListSearchJobs: %v", err)
	}
	if len(jobs) != 1 || jobs[0].Query != "ищем логистов" {
		t.Fatalf("задания после запуска: %+v", jobs)
	}
}

// TestAssistantLaunchWithoutTurn проверяет, что набранный текст подтверждается
// как ещё один ход оператора — запуск работает без предварительного «Отправить».
func TestAssistantLaunchWithoutTurn(t *testing.T) {
	env := newTestEnv()
	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")
	csrf := client.csrfForPage(t, "/")

	resp := client.postForm(t, "/assistant", url.Values{
		"action":         {"launch"},
		"history":        {""},
		"message":        {"ищем логистов"},
		"maxDepth":       {"2"},
		"maxTimeSeconds": {"90"},
		"csrf":           {csrf},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /assistant (launch) = %d, ожидался 303", resp.StatusCode)
	}
	resp.Body.Close()

	jobs, err := env.jobs.ListSearchJobs(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListSearchJobs: %v", err)
	}
	if len(jobs) != 1 || jobs[0].Query != "ищем логистов" {
		t.Fatalf("задания после запуска: %+v", jobs)
	}
}

// TestAssistantLaunchUsesPlanTargets проверяет, что запуск из ассистента с непустым
// планом создаёт задание с именно этими целями, без перевывода из текста запроса.
func TestAssistantLaunchUsesPlanTargets(t *testing.T) {
	env := newTestEnv()
	// LLM-планирование вернуло бы другой запрос — план должен быть взят из формы.
	env.llm.targets = []llm.Target{{Query: "перевыведённая цель"}}

	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")
	csrf := client.csrfForPage(t, "/")

	history := `[{"role":"user","content":"ищем логистов"}]`
	plan := `[{"query":"seed-запрос"},{"url":"https://start.example"}]`
	resp := client.postForm(t, "/assistant", url.Values{
		"action":         {"launch"},
		"history":        {history},
		"plan":           {plan},
		"maxDepth":       {"2"},
		"maxTimeSeconds": {"90"},
		"csrf":           {csrf},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /assistant (launch) = %d, ожидался 303", resp.StatusCode)
	}
	resp.Body.Close()

	jobsList, err := env.jobs.ListSearchJobs(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListSearchJobs: %v", err)
	}
	if len(jobsList) != 1 {
		t.Fatalf("задания после запуска: %+v", jobsList)
	}
	job, err := env.jobs.FindByID(context.Background(), jobsList[0].ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	payload, err := ports.DecodeSearchJobPayload(job.Payload)
	if err != nil {
		t.Fatalf("DecodeSearchJobPayload: %v", err)
	}
	if payload.Plan == nil || len(payload.Plan.Targets) != 2 ||
		payload.Plan.Targets[0].Query != "seed-запрос" || payload.Plan.Targets[1].URL != "https://start.example" {
		t.Fatalf("план задания не совпал с целями формы: %+v", payload.Plan)
	}
}

// TestAssistantLaunchRejectsInvalidPlan проверяет, что недоверенная форма плана
// отклоняется с показом причины и не создаёт задание.
func TestAssistantLaunchRejectsInvalidPlan(t *testing.T) {
	cases := []struct {
		name string
		plan string
	}{
		{"неразбираемая форма", "не json"},
		{"нарушено ровно одно из query/url", `[{"query":"a","url":"https://x.example"}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv()
			ts := httptest.NewServer(env.server)
			defer ts.Close()
			client := newConsoleClient(ts)
			client.login(t, "operator", "secret")
			csrf := client.csrfForPage(t, "/")

			history := `[{"role":"user","content":"ищем логистов"}]`
			resp := client.postForm(t, "/assistant", url.Values{
				"action":         {"launch"},
				"history":        {history},
				"plan":           {tc.plan},
				"maxDepth":       {"2"},
				"maxTimeSeconds": {"90"},
				"csrf":           {csrf},
			})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("POST /assistant (launch) = %d, ожидался 200 с ошибкой", resp.StatusCode)
			}
			html := readBody(t, resp)
			if !strings.Contains(html, "План запуска некорректен") {
				t.Fatalf("нет сообщения о некорректном плане:\n%s", html)
			}

			jobsList, err := env.jobs.ListSearchJobs(context.Background(), 10)
			if err != nil {
				t.Fatalf("ListSearchJobs: %v", err)
			}
			if len(jobsList) != 0 {
				t.Fatalf("задание не должно создаваться при некорректном плане: %+v", jobsList)
			}
		})
	}
}

// TestAssistantProbeLaunchUsesDefaults проверяет запуск короткой проверочной
// сессии из консоли: без явных полей применяются дефолты (глубина 1, время 60 с,
// бюджет 5 кредитов), а лимиты и бюджет сохраняются в задании.
func TestAssistantProbeLaunchUsesDefaults(t *testing.T) {
	env := newTestEnv()
	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")
	csrf := client.csrfForPage(t, "/")

	history := `[{"role":"user","content":"ищем логистов"}]`
	resp := client.postForm(t, "/assistant", url.Values{
		"action":  {"probe"},
		"history": {history},
		"csrf":    {csrf},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /assistant (probe) = %d, ожидался 303", resp.StatusCode)
	}
	resp.Body.Close()

	jobsList, err := env.jobs.ListSearchJobs(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListSearchJobs: %v", err)
	}
	if len(jobsList) != 1 {
		t.Fatalf("задания после запуска: %+v", jobsList)
	}
	if jobsList[0].MaxCredits != app.DefaultProbeMaxCredits {
		t.Fatalf("бюджет короткой сессии = %d, ожидался %d", jobsList[0].MaxCredits, app.DefaultProbeMaxCredits)
	}
	job, err := env.jobs.FindByID(context.Background(), jobsList[0].ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	payload, err := ports.DecodeSearchJobPayload(job.Payload)
	if err != nil {
		t.Fatalf("DecodeSearchJobPayload: %v", err)
	}
	if payload.Plan == nil {
		t.Fatal("план задания отсутствует")
	}
	if payload.Plan.Limits.MaxDepth != app.DefaultProbeDepth || payload.Plan.Limits.MaxTimeSeconds != app.DefaultProbeTimeSeconds {
		t.Fatalf("лимиты короткой сессии = %+v", payload.Plan.Limits)
	}
}

// TestAssistantProbeRejectsZeroBudget проверяет, что короткая сессия без явного
// положительного бюджета не ставится в очередь (дефолт не подменяет ноль).
func TestAssistantProbeRejectsZeroBudget(t *testing.T) {
	env := newTestEnv()
	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")
	csrf := client.csrfForPage(t, "/")

	history := `[{"role":"user","content":"ищем логистов"}]`
	resp := client.postForm(t, "/assistant", url.Values{
		"action":          {"probe"},
		"history":         {history},
		"probeMaxCredits": {"0"},
		"csrf":            {csrf},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /assistant (probe, 0 бюджет) = %d, ожидался 200", resp.StatusCode)
	}
	resp.Body.Close()

	jobsList, err := env.jobs.ListSearchJobs(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListSearchJobs: %v", err)
	}
	if len(jobsList) != 0 {
		t.Fatalf("задание не должно ставиться: %+v", jobsList)
	}
}

// TestAssistantLaunchRejectsInvalidLimits проверяет, что некорректные лимиты из
// формы оператора отвергаются без постановки задания.
func TestAssistantLaunchRejectsInvalidLimits(t *testing.T) {
	env := newTestEnv()
	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")
	csrf := client.csrfForPage(t, "/")

	resp := client.postForm(t, "/assistant", url.Values{
		"action":   {"launch"},
		"history":  {`[{"role":"user","content":"ищем логистов"}]`},
		"message":  {"нужны ещё и перевозчики"},
		"maxDepth": {"-3"},
		"csrf":     {csrf},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /assistant (launch) = %d, ожидался 200 с ошибкой", resp.StatusCode)
	}
	html := readBody(t, resp)
	if !strings.Contains(html, "не может быть отрицательным") {
		t.Fatalf("нет сообщения о некорректном лимите:\n%s", html)
	}
	// Экран сохраняет прежний диалог и набранный оператором текст.
	if !strings.Contains(html, "ищем логистов") {
		t.Fatalf("прежний диалог потерян при отказе лимитов:\n%s", html)
	}
	if !strings.Contains(html, "нужны ещё и перевозчики") {
		t.Fatalf("набранный текст не вернулся в поле ответа:\n%s", html)
	}

	jobs, err := env.jobs.ListSearchJobs(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListSearchJobs: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("задание не должно создаваться при некорректных лимитах: %+v", jobs)
	}
}

// TestAssistantDialogueIsStateless проверяет, что сервер не восстанавливает
// историю диалога: запрос без истории не видит прежних ходов.
func TestAssistantDialogueIsStateless(t *testing.T) {
	env := newTestEnv()
	env.llm.replies = []llm.ConverseReply{{Text: "уточните регион"}}

	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")
	csrf := client.csrfForPage(t, "/")

	first := client.postForm(t, "/assistant", url.Values{
		"action":  {"turn"},
		"message": {"первый секретный ход"},
		"history": {""},
		"csrf":    {csrf},
	})
	readBody(t, first)

	second := client.postForm(t, "/assistant", url.Values{
		"action":  {"turn"},
		"message": {"второй ход"},
		"history": {""},
		"csrf":    {csrf},
	})
	html := readBody(t, second)
	if strings.Contains(html, "первый секретный ход") {
		t.Fatalf("сервер восстановил историю диалога:\n%s", html)
	}
}

// TestForumSignalsScreen проверяет экран форумных сигналов: запрос → список
// обезличенных сигналов; материал с прямым идентификатором исключается.
func TestForumSignalsScreen(t *testing.T) {
	env := newTestEnv()
	env.forum.signals = []forum.Signal{
		{Topic: "Чистая тема", SourceURL: "https://reddit.com/t/1", Date: testTime},
		{Topic: "С адресом", Summary: "пишите на ivan@example.com", SourceURL: "https://reddit.com/t/2", Date: testTime},
	}

	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")

	// Форма без запроса отдаёт 200.
	resp := client.get(t, "/forum-signals")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /forum-signals = %d, ожидался 200", resp.StatusCode)
	}
	resp.Body.Close()

	resp = client.get(t, "/forum-signals?q="+url.QueryEscape("боли агентств"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /forum-signals?q = %d, ожидался 200", resp.StatusCode)
	}
	html := readBody(t, resp)
	if !strings.Contains(html, "Чистая тема") {
		t.Fatalf("сигнал не отображён:\n%s", html)
	}
	if strings.Contains(html, "ivan@example.com") {
		t.Fatalf("материал с прямым идентификатором не исключён:\n%s", html)
	}
}

// TestBulkCancelJob проверяет, что POST /jobs/cancel отменяет все выбранные
// задания одной операцией и перенаправляет на главную.
func TestBulkCancelJob(t *testing.T) {
	env := newTestEnv()
	ctx := context.Background()
	first, err := env.jobs.CreateSearchJob(ctx, "первый", 0)
	if err != nil {
		t.Fatalf("CreateSearchJob: %v", err)
	}
	second, err := env.jobs.CreateSearchJob(ctx, "второй", 0)
	if err != nil {
		t.Fatalf("CreateSearchJob: %v", err)
	}

	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")
	csrf := client.csrfForPage(t, "/")

	resp := client.postForm(t, "/jobs/cancel", url.Values{"csrf": {csrf}, "ids": {first.ID, second.ID}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /jobs/cancel = %d, ожидался 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Fatalf("Location = %q, ожидался /", loc)
	}
	resp.Body.Close()

	list, err := env.jobs.ListSearchJobs(ctx, 10)
	if err != nil {
		t.Fatalf("ListSearchJobs: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("в журнале %d заданий, ожидалось 2", len(list))
	}
	for _, job := range list {
		if string(job.Status) != "cancelled" {
			t.Fatalf("статус задания %s = %q, ожидался cancelled", job.ID, job.Status)
		}
	}
}

// TestBulkCancelEmptySelection проверяет, что отмена без единого отмеченного
// задания ничего не меняет и сообщает об этом оператору.
func TestBulkCancelEmptySelection(t *testing.T) {
	env := newTestEnv()
	job, err := env.jobs.CreateSearchJob(context.Background(), "запрос", 0)
	if err != nil {
		t.Fatalf("CreateSearchJob: %v", err)
	}

	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")
	csrf := client.csrfForPage(t, "/")

	resp := client.postForm(t, "/jobs/cancel", url.Values{"csrf": {csrf}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST /jobs/cancel = %d, ожидался 400", resp.StatusCode)
	}
	if html := readBody(t, resp); !strings.Contains(html, "Ничего не выбрано") {
		t.Fatalf("уведомление о пустом выборе отсутствует:\n%s", html)
	}

	list, err := env.jobs.ListSearchJobs(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListSearchJobs: %v", err)
	}
	if len(list) != 1 || list[0].ID != job.ID || string(list[0].Status) != "queued" {
		t.Fatalf("задание изменилось при пустом выборе: %+v", list)
	}
}

// TestBulkCancelWithTerminalJob проверяет, что при частично терминальном выборе
// остальные задания отменяются, исход терминального не меняется, и операция не
// завершается сбоем.
func TestBulkCancelWithTerminalJob(t *testing.T) {
	env := newTestEnv()
	ctx := context.Background()
	finished, err := env.jobs.CreateSearchJob(ctx, "завершённое", 0)
	if err != nil {
		t.Fatalf("CreateSearchJob: %v", err)
	}
	// Терминальное задание проходит путь взятия: прямой переход queued -> done недопустим.
	if _, err := env.jobs.TakeQueued(ctx); err != nil {
		t.Fatalf("TakeQueued: %v", err)
	}
	if err := env.jobs.UpdateStatus(ctx, finished.ID, jobs.StatusRunning, jobs.StatusDone, "", ""); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	queued, err := env.jobs.CreateSearchJob(ctx, "в очереди", 0)
	if err != nil {
		t.Fatalf("CreateSearchJob: %v", err)
	}

	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")
	csrf := client.csrfForPage(t, "/")

	resp := client.postForm(t, "/jobs/cancel", url.Values{"csrf": {csrf}, "ids": {finished.ID, queued.ID}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /jobs/cancel = %d, ожидался 303", resp.StatusCode)
	}
	resp.Body.Close()

	list, err := env.jobs.ListSearchJobs(ctx, 10)
	if err != nil {
		t.Fatalf("ListSearchJobs: %v", err)
	}
	byID := make(map[string]string, len(list))
	for _, job := range list {
		byID[job.ID] = string(job.Status)
	}
	if byID[queued.ID] != "cancelled" {
		t.Fatalf("статус queued-задания = %q, ожидался cancelled", byID[queued.ID])
	}
	if byID[finished.ID] != "done" {
		t.Fatalf("статус терминального задания = %q, ожидался done", byID[finished.ID])
	}
}

// TestCancelJob проверяет, что POST /jobs/{id}/cancel отменяет задание и
// перенаправляет на главную.
func TestCancelJob(t *testing.T) {
	env := newTestEnv()
	job, err := env.jobs.CreateSearchJob(context.Background(), "запрос", 0)
	if err != nil {
		t.Fatalf("CreateSearchJob: %v", err)
	}

	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")
	csrf := client.csrfForPage(t, "/")

	resp := client.postForm(t, "/jobs/"+job.ID+"/cancel", url.Values{"csrf": {csrf}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST cancel = %d, ожидался 303", resp.StatusCode)
	}

	list, err := env.jobs.ListSearchJobs(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListSearchJobs: %v", err)
	}
	if len(list) != 1 || string(list[0].Status) != "cancelled" {
		t.Fatalf("статус задания = %+v, ожидался cancelled", list)
	}
	if list[0].Error != "" {
		t.Fatalf("отменённое задание не должно нести текст ошибки: %q", list[0].Error)
	}
}

// TestSearchRejectsInvalidBudget проверяет отказ на неположительный или
// нечисловой бюджет запроса до постановки в очередь.
func TestSearchRejectsInvalidBudget(t *testing.T) {
	env := newTestEnv()
	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")
	csrf := client.csrfForPage(t, "/")

	for _, budget := range []string{"0", "-5", "abc"} {
		resp := client.postForm(t, "/search", url.Values{"query": {"q"}, "budget": {budget}, "csrf": {csrf}})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("POST /search budget=%q = %d, ожидался 400", budget, resp.StatusCode)
		}
		resp.Body.Close()
	}

	// Пустой бюджет допустим (значение по умолчанию).
	resp := client.postForm(t, "/search", url.Values{"query": {"q"}, "budget": {""}, "csrf": {csrf}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /search без бюджета = %d, ожидался 303", resp.StatusCode)
	}
}

// TestSearchAcceptsBudget проверяет, что заданный бюджет сохраняется в задании.
func TestSearchAcceptsBudget(t *testing.T) {
	env := newTestEnv()
	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")
	csrf := client.csrfForPage(t, "/")

	resp := client.postForm(t, "/search", url.Values{"query": {"q"}, "budget": {"12"}, "csrf": {csrf}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /search с бюджетом = %d, ожидался 303", resp.StatusCode)
	}

	list, err := env.jobs.ListSearchJobs(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListSearchJobs: %v", err)
	}
	if len(list) != 1 || list[0].MaxCredits != 12 {
		t.Fatalf("бюджет задания не сохранён: %+v", list)
	}
}

// TestAssistantStreamEmitsFrames проверяет, что POST /assistant/stream отдаёт
// поток серверных событий: заголовок text/event-stream и кадры delta/done с
// текстом реплики.
func TestAssistantStreamEmitsFrames(t *testing.T) {
	env := newTestEnv()
	env.llm.replies = []llm.ConverseReply{{Text: "Какой регион нужен?"}}

	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")
	csrf := client.csrfForPage(t, "/assistant")

	resp := client.postForm(t, "/assistant/stream", url.Values{
		"message": {"ищем логистов"},
		"history": {""},
		"plan":    {""},
		"csrf":    {csrf},
	})
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, ожидался text/event-stream", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q, ожидался no-store", cc)
	}
	body := readBody(t, resp)
	for _, want := range []string{"event: delta", "event: plan", "event: done", "Какой регион нужен?"} {
		if !strings.Contains(body, want) {
			t.Fatalf("поток не содержит %q:\n%s", want, body)
		}
	}
}

// TestAssistantStreamEmptyMessage проверяет, что пустой ответ отдаётся кадром
// ошибки, а не пустым потоком.
func TestAssistantStreamEmptyMessage(t *testing.T) {
	env := newTestEnv()
	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")
	csrf := client.csrfForPage(t, "/assistant")

	resp := client.postForm(t, "/assistant/stream", url.Values{
		"message": {"   "},
		"csrf":    {csrf},
	})
	defer resp.Body.Close()

	body := readBody(t, resp)
	if !strings.Contains(body, "event: error") || !strings.Contains(body, "Введите ответ") {
		t.Fatalf("ожидался кадр ошибки о пустом ответе:\n%s", body)
	}
}

// TestAssistantStreamErrorKeepsHistory проверяет, что при неуспехе генерации кадр
// ошибки несёт актуальную историю с уже добавленным ходом оператора.
func TestAssistantStreamErrorKeepsHistory(t *testing.T) {
	env := newTestEnv()
	env.llm.replyErr = errors.New("сбой модели")

	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")
	csrf := client.csrfForPage(t, "/assistant")

	resp := client.postForm(t, "/assistant/stream", url.Values{
		"message": {"ищем логистов"},
		"history": {""},
		"csrf":    {csrf},
	})
	defer resp.Body.Close()

	body := readBody(t, resp)
	if !strings.Contains(body, "event: error") {
		t.Fatalf("ожидался кадр ошибки потока:\n%s", body)
	}
	if !strings.Contains(body, "history") || !strings.Contains(body, "ищем логистов") {
		t.Fatalf("кадр ошибки не содержит историю с ходом оператора:\n%s", body)
	}
}

// TestCardsGroupsShowUnsuitable проверяет вторую группу состава результатов:
// «Неподходящие и гипотезы» содержат и гипотезу, и «Не подходит» с явной
// подписью, а группа «Подходящие» остаётся составом по умолчанию и не теряется.
func TestCardsGroupsShowUnsuitable(t *testing.T) {
	env := newTestEnv()
	seedCompany(t, env.profiles, "fit.example", "Подходящая", "Описание")
	seedCompany(t, env.profiles, "hypo.example", "Гипотеза", "Описание")
	seedCompany(t, env.profiles, "no.example", "Не подходит", "Описание")
	job, err := env.jobs.CreateSearchJob(context.Background(), "запрос задания", 0)
	if err != nil {
		t.Fatalf("CreateSearchJob: %v", err)
	}
	if err := env.jobs.RecordResult(context.Background(), job.ID, []string{"fit.example", "hypo.example", "no.example"}); err != nil {
		t.Fatalf("RecordResult: %v", err)
	}
	seedAssessment(t, env.assessments, job.ID, "fit.example", domain.VerdictFits, domain.SignalDirect, []domain.Evidence{{
		Criterion: "критерий", Fact: "факт", SourceURL: "https://fit.example/s", Reasoning: "связь", SignalKind: domain.SignalDirect,
	}})
	seedAssessment(t, env.assessments, job.ID, "hypo.example", domain.VerdictHypothesis, domain.SignalProfile, []domain.Evidence{{
		Criterion: "критерий", Fact: "профиль", SignalKind: domain.SignalProfile, Missing: "прямое заявление",
	}})
	seedAssessment(t, env.assessments, job.ID, "no.example", domain.VerdictUnsuitable, domain.SignalProfile, []domain.Evidence{{
		Criterion: "критерий", Fact: "факт", SignalKind: domain.SignalProfile, Mismatch: "несоответствие запросу",
	}})

	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")

	html := readBody(t, client.get(t, "/cards?job="+url.QueryEscape(job.ID)))
	for _, want := range []string{
		`data-cards-composition="fits"`,
		`href="/cards/fit.example" data-group="fits"`,
		`href="/cards/hypo.example" data-group="other"`,
		`href="/cards/no.example" data-group="other"`,
		"verdict-hypothesis",
		"verdict-unsuitable",
		"Гипотеза",
		"Не подходит",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("вторая группа состава не содержит %q:\n%s", want, html)
		}
	}
}

// TestSearchQueuesQuotaJob проверяет, что форма «Новый поиск» разбирает целевое
// число компаний, лимит результатов и ручные цели и сохраняет их в задании.
func TestSearchQueuesQuotaJob(t *testing.T) {
	env := newTestEnv()
	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")
	csrf := client.csrfForPage(t, "/")

	resp := client.postForm(t, "/search", url.Values{
		"query":           {"разработчики Go"},
		"targetCompanies": {"100"},
		"maxResults":      {"25"},
		"manualTargets":   {"логистические компании\nhttps://start.example"},
		"csrf":            {csrf},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /search = %d, ожидался 303", resp.StatusCode)
	}
	resp.Body.Close()

	jobsList, err := env.jobs.ListSearchJobs(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListSearchJobs: %v", err)
	}
	if len(jobsList) != 1 {
		t.Fatalf("задания после запуска: %+v", jobsList)
	}
	job, err := env.jobs.FindByID(context.Background(), jobsList[0].ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	payload, err := ports.DecodeSearchJobPayload(job.Payload)
	if err != nil {
		t.Fatalf("DecodeSearchJobPayload: %v", err)
	}
	if payload.TargetCompanies != 100 || payload.MaxResults != 25 {
		t.Fatalf("цель/лимит поиска не сохранены: targetCompanies=%d maxResults=%d", payload.TargetCompanies, payload.MaxResults)
	}
	if payload.Plan == nil || len(payload.Plan.Targets) != 2 ||
		payload.Plan.Targets[0].Query != "логистические компании" || payload.Plan.Targets[1].URL != "https://start.example" {
		t.Fatalf("ручные цели не сохранены дословно: %+v", payload.Plan)
	}
}

// TestSearchRejectsInvalidQuota проверяет, что отрицательное целевое число
// компаний и некорректная ручная цель дают понятный отказ без постановки задания.
func TestSearchRejectsInvalidQuota(t *testing.T) {
	cases := []struct {
		name string
		form url.Values
	}{
		{"отрицательное число компаний", url.Values{"targetCompanies": {"-1"}}},
		{"некорректная ручная цель", url.Values{"manualTargets": {`[{"query":"a","url":"https://x.example"}]`}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv()
			ts := httptest.NewServer(env.server)
			defer ts.Close()
			client := newConsoleClient(ts)
			client.login(t, "operator", "secret")
			csrf := client.csrfForPage(t, "/")

			form := url.Values{"query": {"разработчики Go"}, "csrf": {csrf}}
			for key, values := range tc.form {
				form[key] = values
			}
			resp := client.postForm(t, "/search", form)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("POST /search = %d, ожидался 400", resp.StatusCode)
			}
			resp.Body.Close()

			jobsList, err := env.jobs.ListSearchJobs(context.Background(), 10)
			if err != nil {
				t.Fatalf("ListSearchJobs: %v", err)
			}
			if len(jobsList) != 0 {
				t.Fatalf("задание не должно создаваться при некорректном вводе: %+v", jobsList)
			}
		})
	}
}

// TestAssistantLaunchPassesQuota проверяет, что редактируемые поля панели плана
// (целевое число, лимит поиска и ручные цели) доходят до постановки задания.
func TestAssistantLaunchPassesQuota(t *testing.T) {
	env := newTestEnv()
	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")
	csrf := client.csrfForPage(t, "/")

	resp := client.postForm(t, "/assistant", url.Values{
		"action":          {"launch"},
		"history":         {`[{"role":"user","content":"ищем логистов"}]`},
		"targetCompanies": {"50"},
		"maxResults":      {"10"},
		"manualTargets":   {"seed-запрос"},
		"csrf":            {csrf},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /assistant (launch) = %d, ожидался 303", resp.StatusCode)
	}
	resp.Body.Close()

	jobsList, err := env.jobs.ListSearchJobs(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListSearchJobs: %v", err)
	}
	if len(jobsList) != 1 {
		t.Fatalf("задания после запуска: %+v", jobsList)
	}
	job, err := env.jobs.FindByID(context.Background(), jobsList[0].ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	payload, err := ports.DecodeSearchJobPayload(job.Payload)
	if err != nil {
		t.Fatalf("DecodeSearchJobPayload: %v", err)
	}
	if payload.TargetCompanies != 50 || payload.MaxResults != 10 {
		t.Fatalf("параметры панели плана не дошли: targetCompanies=%d maxResults=%d", payload.TargetCompanies, payload.MaxResults)
	}
	if payload.Plan == nil || len(payload.Plan.Targets) != 1 || payload.Plan.Targets[0].Query != "seed-запрос" {
		t.Fatalf("ручные цели не применены: %+v", payload.Plan)
	}
}

// TestJobsFragmentShowsTargetProgress проверяет, что журнал показывает «найдено X
// из N», фазу добора и причину остановки задания с целью.
func TestJobsFragmentShowsTargetProgress(t *testing.T) {
	env := newTestEnv()
	job, err := env.jobs.CreateSearchJob(context.Background(), "квота", 0)
	if err != nil {
		t.Fatalf("CreateSearchJob: %v", err)
	}
	// Перевод в running: для queued журнал показывает позицию в очереди, а не
	// прогресс; снимок цели проверяется на выполняющемся задании.
	if _, err := env.jobs.TakeQueued(context.Background()); err != nil {
		t.Fatalf("TakeQueued: %v", err)
	}
	if err := env.jobs.RecordProgress(context.Background(), job.ID, ports.SearchJobProgress{
		Phase:           app.PhaseExpanding,
		PagesCrawled:    12,
		CompaniesFound:  3,
		TargetCompanies: 10,
		TargetReached:   false,
		StopReason:      "цели исчерпаны",
	}); err != nil {
		t.Fatalf("RecordProgress: %v", err)
	}

	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")

	html := readBody(t, client.get(t, "/jobs"))
	for _, want := range []string{"компаний 3 из 10", "добор целей", "остановлено: цели исчерпаны"} {
		if !strings.Contains(html, want) {
			t.Fatalf("журнал не содержит %q:\n%s", want, html)
		}
	}
}
