package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
)

// consoleSession — клиент консоли с cookie сессии и CSRF-токеном.
type consoleSession struct {
	client *http.Client
	csrf   string
}

// loginConsole входит в консоль рассылок и возвращает клиент с сессией и
// CSRF-токеном. Вход — обычная HTML-форма, поэтому проверяется реальный приём.
func loginConsole(t *testing.T, fx *testFixture) *consoleSession {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	client := &http.Client{
		Jar: jar,
		// Редиректы не следуем: проверяем и код, и Location.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.PostForm(fx.server.URL+"/login", url.Values{
		"username": {"operator-1"},
		"password": {"secret"},
	})
	if err != nil {
		t.Fatalf("POST /login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("вход: got %d, want %d", resp.StatusCode, http.StatusSeeOther)
	}
	base, _ := url.Parse(fx.server.URL)
	var cookieValue string
	for _, cookie := range jar.Cookies(base) {
		if cookie.Name == fx.api.sessions.CookieName() {
			cookieValue = cookie.Value
		}
	}
	if cookieValue == "" {
		t.Fatal("сессионная cookie не выдана")
	}
	session, ok := fx.api.sessions.Authenticate(cookieValue)
	if !ok {
		t.Fatal("сессия не распознана")
	}
	return &consoleSession{client: client, csrf: session.CSRF()}
}

// getConsole выполняет GET экрана консоли и возвращает код и тело.
func (c *consoleSession) getConsole(t *testing.T, path string) (int, string) {
	t.Helper()
	resp, err := c.client.Get(path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("чтение тела: %v", err)
	}
	return resp.StatusCode, string(body)
}

// postConsole выполняет POST формы экрана консоли с CSRF-токеном.
func (c *consoleSession) postConsole(t *testing.T, path string, form url.Values) (int, string, string) {
	t.Helper()
	form.Set("csrf", c.csrf)
	resp, err := c.client.PostForm(path, form)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("чтение тела: %v", err)
	}
	return resp.StatusCode, resp.Header.Get("Location"), string(body)
}

// seedCampaign создаёт кампанию через JSON-границу среза.
func seedCampaign(t *testing.T, fx *testFixture, campaignID string) {
	t.Helper()
	resp, body := doJSON(t, "POST", fx.server.URL+"/campaigns", map[string]any{
		"command_id":   "cmd-" + campaignID,
		"campaign_id":  campaignID,
		"tenant_id":    string(testTenant),
		"actor_id":     string(testOperator),
		"name":         "Кампания " + campaignID,
		"window_start": testNow(),
		"window_end":   testNow().Add(24 * time.Hour),
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("создание кампании: got %d (%s)", resp.StatusCode, body)
	}
}

// seedDraft ставит черновик через JSON-границу среза.
func seedDraft(t *testing.T, fx *testFixture, campaignID, draftID, material, segment, recipient, channel string) {
	t.Helper()
	resp, body := doJSON(t, "POST", fx.server.URL+"/campaigns/"+campaignID+"/drafts", map[string]any{
		"tenant_id":    string(testTenant),
		"actor_id":     string(testOperator),
		"draft_id":     draftID,
		"material_id":  material,
		"segment_id":   segment,
		"recipient_id": recipient,
		"channel":      channel,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("постановка черновика %s: got %d (%s)", draftID, resp.StatusCode, body)
	}
}

func TestConsoleRequiresSession(t *testing.T) {
	fx := newFixture(t)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(fx.server.URL + "/outreach/drafts")
	if err != nil {
		t.Fatalf("GET /outreach/drafts: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("без сессии: got %d, want %d", resp.StatusCode, http.StatusSeeOther)
	}
	if location := resp.Header.Get("Location"); location != "/login" {
		t.Fatalf("редирект: got %q, want %q", location, "/login")
	}
}

func TestConsoleQueueShowsRiskChecksPerDraft(t *testing.T) {
	risk := func(_ context.Context, req domain.RiskFactsRequest) ([]domain.RiskReason, error) {
		if req.DraftID == "draft-risky" {
			return []domain.RiskReason{domain.RiskStopRuleTriggered}, nil
		}
		return nil, nil
	}
	fx := newFixtureWithRisk(t, risk)
	seedCampaign(t, fx, "campaign-1")
	seedDraft(t, fx, "campaign-1", "draft-risky", "m1", "s1", "r1", "email")
	seedDraft(t, fx, "campaign-1", "draft-safe", "m1", "s1", "r2", "email")

	session := loginConsole(t, fx)
	status, body := session.getConsole(t, fx.server.URL+"/outreach/drafts?tenant_id=tenant-1")
	if status != http.StatusOK {
		t.Fatalf("GET очереди: got %d", status)
	}
	if !strings.Contains(body, "draft-risky") || !strings.Contains(body, "draft-safe") {
		t.Fatal("очередь не содержит оба черновика")
	}
	if !strings.Contains(body, "рисковый") || !strings.Contains(body, "сработало правило остановки") {
		t.Fatal("результат автоматических проверок по рисковому черновику не показан")
	}
	if !strings.Contains(body, "class=\"queue-item is-risky\"") {
		t.Fatal("рисковый черновик не отделён визуально")
	}
}

func TestBatchApproveWithoutCompositionIsRejected(t *testing.T) {
	fx := newFixture(t)
	seedCampaign(t, fx, "campaign-1")
	seedDraft(t, fx, "campaign-1", "draft-1", "m1", "s1", "r1", "email")
	seedDraft(t, fx, "campaign-1", "draft-2", "m1", "s1", "r2", "email")

	session := loginConsole(t, fx)
	// Решение без предъявленного состава: пустой digest и нет позиций.
	status, _, _ := session.postConsole(t, fx.server.URL+"/outreach/campaigns/campaign-1/batches/approve", url.Values{
		"tenant_id": {"tenant-1"},
	})
	if status == http.StatusSeeOther {
		t.Fatal("решение без предъявления состава не должно приниматься")
	}
	if status != http.StatusConflict && status != http.StatusBadRequest {
		t.Fatalf("ожидался отказ, получен %d", status)
	}
	// Состояние не изменилось: черновики всё ещё ожидают решения.
	status, body := session.getConsole(t, fx.server.URL+"/outreach/drafts?tenant_id=tenant-1")
	if status != http.StatusOK || !strings.Contains(body, "draft-1") || !strings.Contains(body, "draft-2") {
		t.Fatal("черновики должны остаться в очереди после отвергнутого решения")
	}
}

func TestBatchApproveWithPresentedComposition(t *testing.T) {
	fx := newFixture(t)
	seedCampaign(t, fx, "campaign-1")
	seedDraft(t, fx, "campaign-1", "draft-1", "m1", "s1", "r1", "email")
	seedDraft(t, fx, "campaign-1", "draft-2", "m1", "s1", "r2", "email")

	session := loginConsole(t, fx)
	status, _ := session.getConsole(t, fx.server.URL+"/outreach/campaigns/campaign-1/batches?tenant_id=tenant-1")
	if status != http.StatusOK {
		t.Fatalf("GET состава: got %d", status)
	}

	session2 := session
	// Состав предъявлен на экране: берём digest и позиции из формы.
	_, screen := session2.getConsole(t, fx.server.URL+"/outreach/campaigns/campaign-1/batches?tenant_id=tenant-1")
	digest := firstMatch(screen, `name="digest" value="([^"]+)"`)
	if digest == "" {
		t.Fatal("экран состава не предъявил digest")
	}
	ids := allMatches(screen, `name="draft_id" value="([^"]+)"`)
	versions := allMatches(screen, `name="draft_version" value="([^"]+)"`)
	if len(ids) != 2 || len(versions) != 2 {
		t.Fatalf("состав формы: ids=%d versions=%d", len(ids), len(versions))
	}

	form := url.Values{
		"tenant_id":     {"tenant-1"},
		"material_id":   {"m1"},
		"segment_id":    {"s1"},
		"digest":        {digest},
		"draft_id":      ids,
		"draft_version": versions,
	}
	status, location, body := session.postConsole(t, fx.server.URL+"/outreach/campaigns/campaign-1/batches/approve", form)
	if status != http.StatusSeeOther {
		t.Fatalf("пакетное решение: got %d (%s)", status, body)
	}
	if !strings.Contains(location, "/outreach/campaigns/campaign-1/batches") {
		t.Fatalf("PRG: got %q", location)
	}
	// Черновики ушли из очереди.
	_, queue := session.getConsole(t, fx.server.URL+"/outreach/drafts?tenant_id=tenant-1")
	if strings.Contains(queue, "draft-1") || strings.Contains(queue, "draft-2") {
		t.Fatal("утверждённые черновики должны уйти из очереди")
	}
}

func TestRiskyDraftExcludedFromPackage(t *testing.T) {
	risk := func(_ context.Context, req domain.RiskFactsRequest) ([]domain.RiskReason, error) {
		if req.DraftID == "draft-risky" {
			return []domain.RiskReason{domain.RiskStopRuleTriggered}, nil
		}
		return nil, nil
	}
	fx := newFixtureWithRisk(t, risk)
	seedCampaign(t, fx, "campaign-1")
	seedDraft(t, fx, "campaign-1", "draft-1", "m1", "s1", "r1", "email")
	seedDraft(t, fx, "campaign-1", "draft-2", "m1", "s1", "r2", "email")
	seedDraft(t, fx, "campaign-1", "draft-risky", "m1", "s1", "r3", "email")

	session := loginConsole(t, fx)
	status, screen := session.getConsole(t, fx.server.URL+"/outreach/campaigns/campaign-1/batches?tenant_id=tenant-1")
	if status != http.StatusOK {
		t.Fatalf("GET состава: got %d", status)
	}
	if !strings.Contains(screen, "draft-risky") || !strings.Contains(screen, "правило остановки") {
		t.Fatal("рисковый черновик должен быть показан исключённым с причиной")
	}
	// Рисковый черновик не среди позиций пакета.
	batchSection := screen[:strings.Index(screen, "excluded")]
	if strings.Contains(batchSection, `value="draft-risky"`) {
		t.Fatal("рисковый черновик не должен быть позицией пакета")
	}

	// Попытка включить рисковый черновик в пакет отвергается.
	digest := firstMatch(screen, `name="digest" value="([^"]+)"`)
	form := url.Values{
		"tenant_id":     {"tenant-1"},
		"material_id":   {"m1"},
		"segment_id":    {"s1"},
		"digest":        {digest},
		"draft_id":      {"draft-1", "draft-2", "draft-risky"},
		"draft_version": {"1", "1", "1"},
	}
	status, _, _ = session.postConsole(t, fx.server.URL+"/outreach/campaigns/campaign-1/batches/approve", form)
	if status != http.StatusConflict {
		t.Fatalf("включение рискового черновика в пакет: got %d, want %d", status, http.StatusConflict)
	}
}

func TestIndividualEditApproveRecordsAuthor(t *testing.T) {
	fx := newFixture(t)
	seedCampaign(t, fx, "campaign-1")
	seedDraft(t, fx, "campaign-1", "draft-1", "m1", "s1", "r1", "email")

	session := loginConsole(t, fx)
	form := url.Values{
		"tenant_id":    {"tenant-1"},
		"material_id":  {"m1"},
		"segment_id":   {"s1"},
		"recipient_id": {"r-edited"},
		"channel":      {"call"},
	}
	status, location, body := session.postConsole(t, fx.server.URL+"/outreach/campaigns/campaign-1/drafts/draft-1/edit-approve", form)
	if status != http.StatusSeeOther {
		t.Fatalf("правка и утверждение: got %d (%s)", status, body)
	}
	if !strings.Contains(location, "/outreach/drafts") {
		t.Fatalf("PRG: got %q", location)
	}

	campaign, err := fx.repo.FindByID(t.Context(), domain.CampaignID("campaign-1"))
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	draft, ok := campaign.Draft(domain.DraftID("draft-1"))
	if !ok {
		t.Fatal("черновик не найден")
	}
	if draft.State() != domain.DraftStateApproved {
		t.Fatalf("состояние после правки и утверждения: got %q", draft.State())
	}
	edits := draft.Edits()
	if len(edits) != 1 {
		t.Fatalf("записей правок: got %d, want 1", len(edits))
	}
	if edits[0].Actor != testOperator {
		t.Fatalf("автор правки: got %q, want %q", edits[0].Actor, testOperator)
	}
	if edits[0].From.Recipient != "r1" || edits[0].To.Recipient != "r-edited" {
		t.Fatalf("состав правки: %+v", edits[0])
	}
}

func TestIndividualRejectRequiresReason(t *testing.T) {
	fx := newFixture(t)
	seedCampaign(t, fx, "campaign-1")
	seedDraft(t, fx, "campaign-1", "draft-1", "m1", "s1", "r1", "email")

	session := loginConsole(t, fx)
	status, _, _ := session.postConsole(t, fx.server.URL+"/outreach/campaigns/campaign-1/drafts/draft-1/reject", url.Values{
		"tenant_id": {"tenant-1"},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("отклонение без причины: got %d, want %d", status, http.StatusBadRequest)
	}
	campaign, _ := fx.repo.FindByID(t.Context(), domain.CampaignID("campaign-1"))
	if draft, _ := campaign.Draft(domain.DraftID("draft-1")); draft.State() != domain.DraftStatePendingApproval {
		t.Fatalf("состояние после отказа: got %q", draft.State())
	}
}

func TestIndividualPostponeKeepsDraftPending(t *testing.T) {
	fx := newFixture(t)
	seedCampaign(t, fx, "campaign-1")
	seedDraft(t, fx, "campaign-1", "draft-1", "m1", "s1", "r1", "email")

	session := loginConsole(t, fx)
	until := testNow().Add(72 * time.Hour).Format(whenFormat)
	status, _, body := session.postConsole(t, fx.server.URL+"/outreach/campaigns/campaign-1/drafts/draft-1/postpone", url.Values{
		"tenant_id": {"tenant-1"},
		"until":     {until},
	})
	if status != http.StatusSeeOther {
		t.Fatalf("отложение: got %d (%s)", status, body)
	}
	campaign, _ := fx.repo.FindByID(t.Context(), domain.CampaignID("campaign-1"))
	draft, _ := campaign.Draft(domain.DraftID("draft-1"))
	if draft.State() != domain.DraftStatePendingApproval {
		t.Fatalf("отложенный черновик должен остаться ожидающим: got %q", draft.State())
	}
	if draft.PostponedUntil().IsZero() {
		t.Fatal("срок отложения не зафиксирован")
	}
}

func TestStalledQueueNotifiesOperator(t *testing.T) {
	fx := newFixture(t)
	seedCampaign(t, fx, "campaign-1")
	seedDraft(t, fx, "campaign-1", "draft-1", "m1", "s1", "r1", "email")

	campaign, err := fx.repo.FindByID(t.Context(), domain.CampaignID("campaign-1"))
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if err := campaign.ExpireDraft(domain.DraftID("draft-1"), 1); err != nil {
		t.Fatalf("ExpireDraft: %v", err)
	}
	if err := fx.repo.Save(t.Context(), campaign); err != nil {
		t.Fatalf("Save: %v", err)
	}

	session := loginConsole(t, fx)
	status, body := session.getConsole(t, fx.server.URL+"/outreach/stalled?tenant_id=tenant-1")
	if status != http.StatusOK {
		t.Fatalf("GET зависших: got %d", status)
	}
	if !strings.Contains(body, "draft-1") || !strings.Contains(body, "expired") {
		t.Fatal("зависший черновик не показан")
	}
	if !strings.Contains(body, "неисполненным") {
		t.Fatal("оператор не уведомлён, что действие не исполнено")
	}
}

// TestOutreachSubnavReplacesSidebarGroup проверяет change console-shell-navigation
// (задачи 3.1–3.2): вторичные переходы раздела «Рассылки» вынесены из общего
// сайдбара во внутриэкранную панель. Общий сайдбар несёт только канонические
// разделы; панель отмечает текущий экран активным, а её ссылки сохраняют
// активные tenant_id и actor, чтобы контекст не терялся при переходе.
func TestOutreachSubnavReplacesSidebarGroup(t *testing.T) {
	fx := newFixture(t)
	seedCampaign(t, fx, "campaign-1")
	seedDraft(t, fx, "campaign-1", "draft-1", "m1", "s1", "r1", "email")
	session := loginConsole(t, fx)

	queueHref := `href="/outreach/drafts?actor=operator-1&amp;tenant_id=tenant-1"`
	stalledHref := `href="/outreach/stalled?actor=operator-1&amp;tenant_id=tenant-1"`

	// Очередь черновиков: сайдбар без вторичной группы, панель активна на очереди.
	status, page := session.getConsole(t, fx.server.URL+"/outreach/drafts?tenant_id="+string(testTenant))
	if status != http.StatusOK {
		t.Fatalf("GET очереди = %d, ожидался 200", status)
	}
	if strings.Contains(page, "sidebar-group-label") || strings.Contains(page, "Экраны рассылок") {
		t.Fatalf("общий сайдбар не должен нести вторичную группу:\n%s", page)
	}
	for _, want := range []string{
		`<nav class="tabs">`,
		`class="tab is-active" ` + queueHref + ` aria-current="page">Очередь черновиков</a>`,
		`class="tab" ` + stalledHref + `>Зависшие черновики</a>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("экран очереди не содержит %q:\n%s", want, page)
		}
	}

	// Экран черновика и экран состава пакетов — те же экраны раздела: панель
	// сохраняется и активна на очереди черновиков.
	for _, path := range []string{
		"/outreach/campaigns/campaign-1/drafts/draft-1?tenant_id=" + string(testTenant),
		"/outreach/campaigns/campaign-1/batches?tenant_id=" + string(testTenant),
	} {
		status, page = session.getConsole(t, fx.server.URL+path)
		if status != http.StatusOK {
			t.Fatalf("GET %s = %d, ожидался 200", path, status)
		}
		if !strings.Contains(page, `<nav class="tabs">`) {
			t.Errorf("экран %s не вывел внутриэкранную панель раздела:\n%s", path, page)
		}
		if !strings.Contains(page, `class="tab is-active" `+queueHref+` aria-current="page">Очередь черновиков</a>`) {
			t.Errorf("экран %s не отметил активной очередь черновиков:\n%s", path, page)
		}
	}

	// Срез зависших: панель активна на зависших, ссылка на очередь сохраняет контекст.
	status, page = session.getConsole(t, fx.server.URL+"/outreach/stalled?tenant_id="+string(testTenant))
	if status != http.StatusOK {
		t.Fatalf("GET зависших = %d, ожидался 200", status)
	}
	for _, want := range []string{
		`class="tab" ` + queueHref + `>Очередь черновиков</a>`,
		`class="tab is-active" ` + stalledHref + ` aria-current="page">Зависшие черновики</a>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("экран зависших не содержит %q:\n%s", want, page)
		}
	}
}

// firstMatch возвращает первую подгруппу совпадения.
func firstMatch(text, pattern string) string {
	matches := regexp.MustCompile(pattern).FindStringSubmatch(text)
	if len(matches) < 2 {
		return ""
	}
	return matches[1]
}

// allMatches возвращает первые подгруппы всех совпадений.
func allMatches(text, pattern string) []string {
	matches := regexp.MustCompile(pattern).FindAllStringSubmatch(text, -1)
	results := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) >= 2 {
			results = append(results, match[1])
		}
	}
	return results
}
