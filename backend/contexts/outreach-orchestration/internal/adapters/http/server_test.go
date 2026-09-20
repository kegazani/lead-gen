package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/adapters/access"
	"lead-gen/backend/contexts/outreach-orchestration/internal/adapters/idempotency"
	outboxinmemory "lead-gen/backend/contexts/outreach-orchestration/internal/adapters/outbox/inmemory"
	persistence "lead-gen/backend/contexts/outreach-orchestration/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/outreach-orchestration/internal/app/command"
	"lead-gen/backend/contexts/outreach-orchestration/internal/app/query"
	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

const (
	testTenant   = domain.TenantID("tenant-1")
	testOperator = domain.ActorID("operator-1")
)

// testNow — детерминированная точка отсчёта, чтобы ответы не зависели от
// системных часов.
func testNow() time.Time {
	return time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
}

// fixedClock — часы с фиксированным моментом.
type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

// testFixture — собранный срез: HTTP-сервер и наблюдаемые зависимости.
type testFixture struct {
	server  *httptest.Server
	repo    *persistence.Repository
	journal *outboxinmemory.Journal
	keys    *idempotency.Store
	access  *access.Decider
}

// newFixture собирает срез на настоящих адаптерах: in-memory репозитории,
// journal outbox с unit-of-work, списке разрешений среза, фиксированных часах и
// хранилище ключей идемпотентности. Тест идёт через реальный HTTP-адаптер.
func newFixture(t *testing.T) *testFixture {
	t.Helper()
	return newFixtureWithRisk(t, nil)
}

// testBatchLimits — лимиты пакета для интеграционных сценариев HTTP. Минимум
// снижен до двух однородных черновиков, чтобы проверять путь решения, не создавая
// 50 записей; сам порог продукта закреплён тестами app/batchlimits.
func testBatchLimits() domain.BatchLimits {
	return domain.BatchLimits{MinSize: 2, ShardSize: 10, MaxSize: 100}
}

// newFixtureWithRisk собирает тот же срез, но с заданным внешним предикатом
// риска: nil означает «рисков нет», как и в домене.
func newFixtureWithRisk(t *testing.T, risk domain.CanBeDecidedInPackage) *testFixture {
	t.Helper()

	repo := persistence.New()
	uow := outboxinmemory.NewUnitOfWorkWith(repo, nil)
	decider := access.NewDecider()
	if err := decider.Grant(testOperator, testTenant); err != nil {
		t.Fatalf("выдача права доступа: %v", err)
	}
	clock := fixedClock{now: testNow()}
	keys := idempotency.NewStore()

	server := NewServer(Dependencies{
		CreateCampaign: command.NewCreateCampaignHandler(decider, uow),
		DraftAction:    command.NewDraftActionHandler(decider, clock, uow),
		ApproveDraft:   command.NewApproveDraftHandler(decider, clock, uow),
		RejectDraft:    command.NewRejectDraftHandler(decider, clock, uow),
		ReviseDraft:    command.NewReviseDraftHandler(decider, uow),
		ApprovalQueue:  query.NewApprovalQueueHandler(repo, decider),
		DraftDecision:  query.NewDraftDecisionHandler(repo, decider),
		BatchQueue:     query.NewBatchQueueHandler(repo, risk, decider),
		BuildBatch:     command.NewBuildBatchHandler(repo, decider, risk),
		ApproveBatch:   command.NewApproveBatchHandler(decider, clock, uow, risk),
		BatchLimits:    testBatchLimits(),
		CommandKeys:    keys,
	})

	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	return &testFixture{server: httpServer, repo: repo, journal: uow.Journal(), keys: keys, access: decider}
}

// doJSON выполняет HTTP-запрос с JSON-телом и возвращает ответ и его тело.
func doJSON(t *testing.T, method, url string, body any) (*http.Response, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("сериализация тела запроса: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("создание запроса: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("чтение ответа: %v", err)
	}
	return response, data
}

// decodeBody разбирает тело ответа в типизированную форму.
func decodeBody[T any](t *testing.T, data []byte) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("разбор ответа %q: %v", data, err)
	}
	return out
}

// countApproved считает записи outbox с событием action-approved.
func countApproved(t *testing.T, fixture *testFixture) int {
	t.Helper()
	pending, err := fixture.journal.Pending(context.Background())
	if err != nil {
		t.Fatalf("чтение журнала outbox: %v", err)
	}
	count := 0
	for _, message := range pending {
		if _, ok := message.Event.(domain.ActionApproved); ok {
			count++
		}
	}
	return count
}

// TestDraftApprovalFlowOverHTTP проводит черновик от постановки до решения
// через HTTP и проверяет несущий инвариант: без решения оператора действие не
// исполняется. Тот же сценарий покрывает идемпотентность границы (задача 6.2).
func TestDraftApprovalFlowOverHTTP(t *testing.T) {
	fixture := newFixture(t)
	ctx := context.Background()

	createBody := func(campaignID string) map[string]any {
		return map[string]any{
			"command_id":   "command-1",
			"campaign_id":  campaignID,
			"tenant_id":    testTenant.String(),
			"actor_id":     testOperator.String(),
			"name":         "Кампания среза",
			"window_start": testNow().Format(time.RFC3339),
			"window_end":   testNow().Add(24 * time.Hour).Format(time.RFC3339),
		}
	}

	// Создание кампании через границу campaign-command.
	response, body := doJSON(t, http.MethodPost, fixture.server.URL+"/campaigns", createBody("campaign-1"))
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("создание кампании: статус %d, тело %s", response.StatusCode, body)
	}
	created := decodeBody[campaignResponse](t, body)
	if created.CampaignID != "campaign-1" || !created.Created || created.Replayed {
		t.Fatalf("ответ создания кампании: %+v", created)
	}

	// Повтор команды с тем же ключом и другим campaign_id не создаёт вторую
	// кампанию: ключ идемпотентности — (tenant_id, command_id).
	response, body = doJSON(t, http.MethodPost, fixture.server.URL+"/campaigns", createBody("campaign-2"))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("повтор команды: статус %d, тело %s", response.StatusCode, body)
	}
	replayed := decodeBody[campaignResponse](t, body)
	if replayed.CampaignID != "campaign-1" || !replayed.Replayed {
		t.Fatalf("ответ повтора команды: %+v", replayed)
	}
	if _, err := fixture.repo.FindByID(ctx, "campaign-2"); !errors.Is(err, ports.ErrCampaignNotFound) {
		t.Fatalf("повтор команды создал вторую кампанию: %v", err)
	}
	if fixture.keys.Len() != 1 {
		t.Fatalf("ключей идемпотентности: %d, ожидался 1", fixture.keys.Len())
	}

	draftBody := map[string]any{
		"tenant_id":    testTenant.String(),
		"actor_id":     testOperator.String(),
		"draft_id":     "draft-1",
		"material_id":  "material-1",
		"segment_id":   "segment-1",
		"recipient_id": "recipient-1",
		"channel":      "email",
	}

	// Постановка черновика: состояние «ожидает утверждения», действие не исполнено.
	response, body = doJSON(t, http.MethodPost,
		fixture.server.URL+"/campaigns/campaign-1/drafts", draftBody)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("постановка черновика: статус %d, тело %s", response.StatusCode, body)
	}
	draft := decodeBody[draftResponse](t, body)
	if draft.DraftID != "draft-1" || draft.Version != 1 || draft.State != string(domain.DraftStatePendingApproval) {
		t.Fatalf("ответ постановки черновика: %+v", draft)
	}
	if got := countApproved(t, fixture); got != 0 {
		t.Fatalf("после постановки черновика событий action-approved: %d, ожидалось 0", got)
	}

	// Прямой вызов исполнения без решения отвергается агрегатом (инвариант I1).
	campaign, err := fixture.repo.FindByID(ctx, "campaign-1")
	if err != nil {
		t.Fatalf("загрузка кампании: %v", err)
	}
	if err := campaign.ExecuteDraft("draft-1", 1); !errors.Is(err, domain.ErrApprovalRequired) {
		t.Fatalf("исполнение без решения: получено %v, ожидалась ErrApprovalRequired", err)
	}

	// Решение по устаревшей версии отвергается без изменения состояния.
	staleBody := map[string]any{
		"tenant_id":        testTenant.String(),
		"actor_id":         testOperator.String(),
		"decision_id":      "decision-stale",
		"expected_version": 2,
		"reason":           "",
	}
	response, body = doJSON(t, http.MethodPost,
		fixture.server.URL+"/campaigns/campaign-1/drafts/draft-1/approve", staleBody)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("решение по устаревшей версии: статус %d, тело %s", response.StatusCode, body)
	}

	// Утверждение точной версии через HTTP.
	approveBody := map[string]any{
		"tenant_id":        testTenant.String(),
		"actor_id":         testOperator.String(),
		"decision_id":      "decision-1",
		"expected_version": 1,
		"reason":           "",
	}
	response, body = doJSON(t, http.MethodPost,
		fixture.server.URL+"/campaigns/campaign-1/drafts/draft-1/approve", approveBody)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("утверждение: статус %d, тело %s", response.StatusCode, body)
	}
	decision := decodeBody[decisionResponse](t, body)
	if decision.Outcome != string(domain.DecisionApproved) || !decision.Recorded {
		t.Fatalf("ответ утверждения: %+v", decision)
	}
	if got := countApproved(t, fixture); got != 1 {
		t.Fatalf("после утверждения событий action-approved: %d, ожидалось 1", got)
	}

	campaign, err = fixture.repo.FindByID(ctx, "campaign-1")
	if err != nil {
		t.Fatalf("повторная загрузка кампании: %v", err)
	}
	approved, ok := campaign.Draft("draft-1")
	if !ok || approved.State() != domain.DraftStateApproved {
		t.Fatalf("состояние черновика после утверждения: %+v", approved)
	}
	if err := campaign.ExecuteDraft("draft-1", 1); err != nil {
		t.Fatalf("исполнение утверждённого черновика: %v", err)
	}

	// Повтор того же решения идемпотентен: второй записи и события нет.
	response, body = doJSON(t, http.MethodPost,
		fixture.server.URL+"/campaigns/campaign-1/drafts/draft-1/approve", approveBody)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("повтор решения: статус %d, тело %s", response.StatusCode, body)
	}
	repeated := decodeBody[decisionResponse](t, body)
	if repeated.Recorded || repeated.DecisionID != decision.DecisionID {
		t.Fatalf("ответ повтора решения: %+v", repeated)
	}
	if got := countApproved(t, fixture); got != 1 {
		t.Fatalf("после повтора решения событий action-approved: %d, ожидалось 1", got)
	}

	// Очередь после утверждения пуста, журнал содержит собственное решение.
	response, body = doJSON(t, http.MethodGet,
		fixture.server.URL+"/campaigns?campaign_id=campaign-1&tenant_id=tenant-1&actor_id=operator-1", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("очередь утверждения: статус %d, тело %s", response.StatusCode, body)
	}
	if queue := decodeBody[approvalQueueResponse](t, body); len(queue.Items) != 0 {
		t.Fatalf("очередь после утверждения: %+v", queue.Items)
	}

	response, body = doJSON(t, http.MethodGet,
		fixture.server.URL+"/campaigns/campaign-1/drafts/draft-1/decisions?tenant_id=tenant-1&actor_id=operator-1", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("журнал решений: статус %d, тело %s", response.StatusCode, body)
	}
	journal := decodeBody[decisionJournalResponse](t, body)
	if len(journal.Entries) != 1 {
		t.Fatalf("журнал после утверждения: %+v", journal.Entries)
	}
	if entry := journal.Entries[0]; entry.ActorID != "operator-1" ||
		entry.Outcome != string(domain.DecisionApproved) || entry.DraftVersion != 1 {
		t.Fatalf("запись журнала: %+v", entry)
	}

	// Второй черновик: отклонение без причины отвергается доменом, а с причиной
	// фиксируется и не порождает action-approved.
	draftBody["draft_id"] = "draft-2"
	response, body = doJSON(t, http.MethodPost,
		fixture.server.URL+"/campaigns/campaign-1/drafts", draftBody)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("постановка второго черновика: статус %d, тело %s", response.StatusCode, body)
	}

	rejectBody := map[string]any{
		"tenant_id":        testTenant.String(),
		"actor_id":         testOperator.String(),
		"decision_id":      "decision-2",
		"expected_version": 1,
		"reason":           "",
	}
	response, body = doJSON(t, http.MethodPost,
		fixture.server.URL+"/campaigns/campaign-1/drafts/draft-2/reject", rejectBody)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("отклонение без причины: статус %d, тело %s", response.StatusCode, body)
	}

	rejectBody["reason"] = "не тот сегмент"
	response, body = doJSON(t, http.MethodPost,
		fixture.server.URL+"/campaigns/campaign-1/drafts/draft-2/reject", rejectBody)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("отклонение: статус %d, тело %s", response.StatusCode, body)
	}
	if rejected := decodeBody[decisionResponse](t, body); rejected.Outcome != string(domain.DecisionRejected) {
		t.Fatalf("ответ отклонения: %+v", rejected)
	}
	if got := countApproved(t, fixture); got != 1 {
		t.Fatalf("после отклонения событий action-approved: %d, ожидалось 1", got)
	}

	// Доработка повышает версию: прежнее решение к ней не относится.
	reviseBody := map[string]any{
		"tenant_id":        testTenant.String(),
		"actor_id":         testOperator.String(),
		"expected_version": 1,
		"material_id":      "material-2",
		"segment_id":       "segment-1",
		"recipient_id":     "recipient-1",
		"channel":          "call",
	}
	response, body = doJSON(t, http.MethodPost,
		fixture.server.URL+"/campaigns/campaign-1/drafts/draft-2/revise", reviseBody)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("доработка: статус %d, тело %s", response.StatusCode, body)
	}
	revised := decodeBody[draftResponse](t, body)
	if revised.Version != 2 || !revised.Revised || revised.State != string(domain.DraftStatePendingApproval) {
		t.Fatalf("ответ доработки: %+v", revised)
	}

	staleApprove := map[string]any{
		"tenant_id":        testTenant.String(),
		"actor_id":         testOperator.String(),
		"decision_id":      "decision-3",
		"expected_version": 1,
		"reason":           "",
	}
	response, body = doJSON(t, http.MethodPost,
		fixture.server.URL+"/campaigns/campaign-1/drafts/draft-2/approve", staleApprove)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("решение по прежней версии после доработки: статус %d, тело %s", response.StatusCode, body)
	}
}

// TestReadsRequireTenantAndActor проверяет, что читающие пути без тенанта или
// актора отвергаются как ошибка запроса, а не обслуживаются «без фильтра».
func TestReadsRequireTenantAndActor(t *testing.T) {
	fixture := newFixture(t)

	cases := []struct {
		name string
		url  string
	}{
		{"очередь без тенанта", "/campaigns?campaign_id=campaign-1&actor_id=operator-1"},
		{"очередь без актора", "/campaigns?campaign_id=campaign-1&tenant_id=tenant-1"},
		{"журнал без тенанта", "/campaigns/campaign-1/drafts/draft-1/decisions?actor_id=operator-1"},
		{"журнал без актора", "/campaigns/campaign-1/drafts/draft-1/decisions?tenant_id=tenant-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response, body := doJSON(t, http.MethodGet, fixture.server.URL+tc.url, nil)
			if response.StatusCode != http.StatusBadRequest {
				t.Fatalf("статус %d, ожидался 400, тело %s", response.StatusCode, body)
			}
		})
	}
}

// TestReadsDenyActorWithoutRight проверяет, что актор без выданного права не
// читает ни очередь, ни журнал решений.
func TestReadsDenyActorWithoutRight(t *testing.T) {
	fixture := newFixture(t)
	urls := []string{
		"/campaigns?campaign_id=campaign-1&tenant_id=tenant-1&actor_id=intruder",
		"/campaigns/campaign-1/drafts/draft-1/decisions?tenant_id=tenant-1&actor_id=intruder",
	}
	for _, url := range urls {
		response, body := doJSON(t, http.MethodGet, fixture.server.URL+url, nil)
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("%s: статус %d, ожидался 403, тело %s", url, response.StatusCode, body)
		}
	}
}

// TestReadsDoNotCrossTenants проверяет, что данные одного тенанта недоступны
// другому даже при выданном в чужом тенанте праве.
func TestReadsDoNotCrossTenants(t *testing.T) {
	fixture := newFixture(t)

	createBody := map[string]any{
		"command_id":   "command-1",
		"campaign_id":  "campaign-1",
		"tenant_id":    testTenant.String(),
		"actor_id":     testOperator.String(),
		"name":         "Кампания среза",
		"window_start": testNow().Format(time.RFC3339),
		"window_end":   testNow().Add(24 * time.Hour).Format(time.RFC3339),
	}
	if response, body := doJSON(t, http.MethodPost, fixture.server.URL+"/campaigns", createBody); response.StatusCode != http.StatusCreated {
		t.Fatalf("создание кампании: статус %d, тело %s", response.StatusCode, body)
	}

	draftBody := map[string]any{
		"tenant_id":    testTenant.String(),
		"actor_id":     testOperator.String(),
		"draft_id":     "draft-1",
		"material_id":  "material-1",
		"segment_id":   "segment-1",
		"recipient_id": "recipient-1",
		"channel":      "email",
	}
	if response, body := doJSON(t, http.MethodPost,
		fixture.server.URL+"/campaigns/campaign-1/drafts", draftBody); response.StatusCode != http.StatusCreated {
		t.Fatalf("постановка черновика: статус %d, тело %s", response.StatusCode, body)
	}

	// Оператору выдан доступ в чужом тенанте: право есть, но данных там нет.
	if err := fixture.access.Grant(testOperator, domain.TenantID("tenant-2")); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	response, body := doJSON(t, http.MethodGet,
		fixture.server.URL+"/campaigns?campaign_id=campaign-1&tenant_id=tenant-2&actor_id=operator-1", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("очередь чужого тенанта: статус %d, тело %s", response.StatusCode, body)
	}
	if queue := decodeBody[approvalQueueResponse](t, body); len(queue.Items) != 0 {
		t.Fatalf("утечка данных чужого тенанта в очередь: %+v", queue.Items)
	}

	response, body = doJSON(t, http.MethodGet,
		fixture.server.URL+"/campaigns/campaign-1/drafts/draft-1/decisions?tenant_id=tenant-2&actor_id=operator-1", nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("журнал чужого тенанта: статус %d, тело %s", response.StatusCode, body)
	}
}

// createResult — результат запроса создания кампании из отдельной горутины.
type createResult struct {
	status int
	body   []byte
	err    error
}

// requestCreateCampaign выполняет запрос создания кампании без вызовов t:
// вызывается из горутин, где t.Fatal неприменим.
func requestCreateCampaign(url string, body map[string]any) createResult {
	encoded, err := json.Marshal(body)
	if err != nil {
		return createResult{err: err}
	}
	response, err := http.Post(url, "application/json", bytes.NewReader(encoded))
	if err != nil {
		return createResult{err: err}
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	return createResult{status: response.StatusCode, body: data, err: err}
}

// TestConcurrentCreateCampaignRepeatCreatesOneCampaign фиксирует отсутствие
// TOCTOU: конкурентные повторы одного ключа с разными campaign_id создают ровно
// одну кампанию, а все ответы ссылаются на неё.
func TestConcurrentCreateCampaignRepeatCreatesOneCampaign(t *testing.T) {
	fixture := newFixture(t)
	const attempts = 8

	results := make(chan createResult, attempts)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results <- requestCreateCampaign(fixture.server.URL+"/campaigns", map[string]any{
				"command_id":   "command-race",
				"campaign_id":  fmt.Sprintf("campaign-%d", i),
				"tenant_id":    testTenant.String(),
				"actor_id":     testOperator.String(),
				"name":         "Кампания гонки",
				"window_start": testNow().Format(time.RFC3339),
				"window_end":   testNow().Add(24 * time.Hour).Format(time.RFC3339),
			})
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)

	created := 0
	var authoritative string
	for result := range results {
		if result.err != nil {
			t.Fatalf("запрос создания: %v", result.err)
		}
		if result.status != http.StatusCreated && result.status != http.StatusOK {
			t.Fatalf("статус %d, тело %s", result.status, result.body)
		}
		response := decodeBody[campaignResponse](t, result.body)
		if response.CampaignID == "" {
			t.Fatalf("ответ без campaign_id: %s", result.body)
		}
		if authoritative == "" {
			authoritative = response.CampaignID
		} else if response.CampaignID != authoritative {
			t.Fatalf("конкурентные ответы ссылаются на разные кампании: %q и %q", authoritative, response.CampaignID)
		}
		if !response.Replayed {
			created++
		}
	}

	if created != 1 {
		t.Fatalf("создающих ответов %d, ожидался 1", created)
	}
	if fixture.keys.Len() != 1 {
		t.Fatalf("ключей идемпотентности %d, ожидался 1", fixture.keys.Len())
	}
	pending, err := fixture.repo.FindByID(context.Background(), domain.CampaignID(authoritative))
	if err != nil {
		t.Fatalf("авторитетная кампания не найдена: %v", err)
	}
	if pending.ID().String() != authoritative {
		t.Fatalf("найдена не та кампания: %s", pending.ID())
	}
}

// abandonedKeyStore — хранилище ключей, у которого владелец отменил
// резервирование: Reserve возвращает ErrReservationAbandoned, как его получает
// ожидающий повтор после незавершённого выполнения. Остальные методы не
// вызываются, потому что эффект не запускается.
type abandonedKeyStore struct{}

func (abandonedKeyStore) Reserve(context.Context, domain.TenantID, domain.CommandID, domain.CampaignID) (domain.CampaignID, bool, error) {
	return "", false, idempotency.ErrReservationAbandoned
}

func (abandonedKeyStore) Complete(domain.TenantID, domain.CommandID, domain.CampaignID) {}

func (abandonedKeyStore) Release(domain.TenantID, domain.CommandID) {}

// TestCreateCampaignAbandonedReservationIsConflict закрепляет маппинг
// ErrReservationAbandoned: незавершённое резервирование ключа идемпотентности
// отвечает 409 Conflict, а не 500, и повтор команды остаётся возможным.
func TestCreateCampaignAbandonedReservationIsConflict(t *testing.T) {
	server := httptest.NewServer(NewServer(Dependencies{CommandKeys: abandonedKeyStore{}}))
	t.Cleanup(server.Close)

	response, body := doJSON(t, http.MethodPost, server.URL+"/campaigns", map[string]any{
		"command_id":   "command-1",
		"campaign_id":  "campaign-1",
		"tenant_id":    testTenant.String(),
		"actor_id":     testOperator.String(),
		"name":         "Кампания",
		"window_start": testNow().Format(time.RFC3339),
		"window_end":   testNow().Add(time.Hour).Format(time.RFC3339),
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("статус %d, ожидался 409, тело %s", response.StatusCode, body)
	}
}
