package outreachorchestration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	composition "lead-gen/backend/contexts/outreach-orchestration"
	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports/contracts"
)

const (
	e2eTenant   = domain.TenantID("tenant-1")
	e2eOperator = domain.ActorID("operator-1")
)

// e2eNow — детерминированная точка отсчёта: сборка времени не зависит от
// системных часов.
func e2eNow() time.Time {
	return time.Date(2026, time.March, 4, 12, 0, 0, 0, time.UTC)
}

// fixedClock — часы с фиксированным моментом для порта Clock.
type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

// recordingSink — назначение публикации relay: запоминает доставленные
// контракты, чтобы тест проверил публикацию approved-action.
type recordingSink struct {
	mu      sync.Mutex
	actions []contracts.ApprovedAction
}

// Publish реализует relay.Sink.
func (s *recordingSink) Publish(_ context.Context, action contracts.ApprovedAction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.actions = append(s.actions, action)
	return nil
}

func (s *recordingSink) delivered() []contracts.ApprovedAction {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]contracts.ApprovedAction, len(s.actions))
	copy(out, s.actions)
	return out
}

// e2eFixture — собранный контекст среза: HTTP-сервер, назначение публикации и
// сам срез для наблюдения состояния.
type e2eFixture struct {
	slice  *composition.Slice
	server *httptest.Server
	sink   *recordingSink
}

// newE2EFixture собирает контекст среза на in-memory адаптерах и открывает
// HTTP-сервер адаптера http. Предикат риска задаётся явно: Build не принимает
// nil, а для сценариев без проверок используется осознанная заглушка.
func newE2EFixture(t *testing.T) *e2eFixture {
	t.Helper()
	return newE2EFixtureWithRisk(t, composition.NoUpstreamRiskChecks)
}

// newE2EFixtureWithRisk делает то же, но с инъецированным предикатом признаков
// риска пакетного пути. Предикат передаётся в composition.Build, поэтому его
// получают и сценарий состава, и пакетное решение: исключение состава и отказ в
// решении опираются на один и тот же признак.
func newE2EFixtureWithRisk(t *testing.T, risk domain.CanBeDecidedInPackage) *e2eFixture {
	t.Helper()

	sink := &recordingSink{}
	slice, err := composition.Build(composition.Params{
		Clock:  fixedClock{now: e2eNow()},
		Grants: []composition.Grant{{Actor: e2eOperator, Tenant: e2eTenant}},
		Sink:   sink,
		Risk:   risk,
	})
	if err != nil {
		t.Fatalf("сборка контекста среза: %v", err)
	}

	server := httptest.NewServer(slice.Server)
	t.Cleanup(server.Close)
	return &e2eFixture{slice: slice, server: server, sink: sink}
}

// doJSON выполняет HTTP-запрос с JSON-телом и возвращает ответ и тело.
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

// createCampaignBody — тело команды создания кампании с ключом идемпотентности.
func createCampaignBody() map[string]any {
	return map[string]any{
		"command_id":   "command-1",
		"campaign_id":  "campaign-1",
		"tenant_id":    e2eTenant.String(),
		"actor_id":     e2eOperator.String(),
		"name":         "Кампания сквозного сценария",
		"window_start": e2eNow().Format(time.RFC3339),
		"window_end":   e2eNow().Add(24 * time.Hour).Format(time.RFC3339),
	}
}

// draftActionBody — тело команды постановки черновика.
func draftActionBody() map[string]any {
	return map[string]any{
		"tenant_id":    e2eTenant.String(),
		"actor_id":     e2eOperator.String(),
		"draft_id":     "draft-1",
		"material_id":  "material-1",
		"segment_id":   "segment-1",
		"recipient_id": "recipient-1",
		"channel":      domain.ActionChannelEmailName,
	}
}

// createCampaignAndDraft проходит первые шаги сценария через HTTP и проверяет
// их результат.
func createCampaignAndDraft(t *testing.T, fixture *e2eFixture) {
	t.Helper()

	response, body := doJSON(t, http.MethodPost, fixture.server.URL+"/campaigns", createCampaignBody())
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("создание кампании: статус %d, тело %s", response.StatusCode, body)
	}
	created := decodeBody[struct {
		CampaignID string `json:"campaign_id"`
		Created    bool   `json:"created"`
	}](t, body)
	if created.CampaignID != "campaign-1" || !created.Created {
		t.Fatalf("ответ создания кампании: %+v", created)
	}

	response, body = doJSON(t, http.MethodPost,
		fixture.server.URL+"/campaigns/campaign-1/drafts", draftActionBody())
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("постановка черновика: статус %d, тело %s", response.StatusCode, body)
	}
	draft := decodeBody[struct {
		DraftID string `json:"draft_id"`
		Version int    `json:"version"`
		State   string `json:"state"`
	}](t, body)
	if draft.DraftID != "draft-1" || draft.Version != 1 || draft.State != string(domain.DraftStatePendingApproval) {
		t.Fatalf("ответ постановки черновика: %+v", draft)
	}
}

// pendingApprovedEvents считает неподтверждённые события action-approved в
// журнале outbox.
func pendingApprovedEvents(t *testing.T, fixture *e2eFixture) int {
	t.Helper()
	pending, err := fixture.slice.Journal.Pending(context.Background())
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

// TestDraftApprovalFlowPublishesApprovedAction — сквозной сценарий задачи 8.1:
// создание кампании → постановка черновика → утверждение → публикация
// approved-action через relay. Отдельно проверяется, что прямой вызов
// исполнения без решения не исполняет действие.
func TestDraftApprovalFlowPublishesApprovedAction(t *testing.T) {
	fixture := newE2EFixture(t)
	createCampaignAndDraft(t, fixture)
	ctx := context.Background()

	// Черновик без решения не порождает approved-action.
	if got := pendingApprovedEvents(t, fixture); got != 0 {
		t.Fatalf("после постановки черновика action-approved в outbox: %d, ожидалось 0", got)
	}
	published, err := fixture.slice.Relay.PublishPending(ctx)
	if err != nil {
		t.Fatalf("публикация до решения: %v", err)
	}
	if published != 0 {
		t.Fatalf("до решения опубликовано %d контрактов, ожидалось 0", published)
	}
	if delivered := fixture.sink.delivered(); len(delivered) != 0 {
		t.Fatalf("до решения получатель получил %d контрактов, ожидалось 0", len(delivered))
	}

	// Прямой вызов исполнения без решения отвергается агрегатом (инвариант I1).
	campaign, err := fixture.slice.Repository.FindByID(ctx, "campaign-1")
	if err != nil {
		t.Fatalf("загрузка кампании: %v", err)
	}
	if err := campaign.ExecuteDraft("draft-1", 1); !errors.Is(err, domain.ErrApprovalRequired) {
		t.Fatalf("исполнение без решения: получено %v, ожидалась ErrApprovalRequired", err)
	}

	// Расширенная проверка на свежем агрегате: отказ не меняет состояние черновика.
	loaded, err := fixture.slice.Repository.FindByID(ctx, "campaign-1")
	if err != nil {
		t.Fatalf("повторная загрузка кампании: %v", err)
	}
	stillPending, ok := loaded.Draft("draft-1")
	if !ok || stillPending.State() != domain.DraftStatePendingApproval {
		t.Fatalf("черновик после отказа в исполнении: %+v", stillPending)
	}

	// Утверждение точной версии — единственный путь к публикации.
	response, body := doJSON(t, http.MethodPost,
		fixture.server.URL+"/campaigns/campaign-1/drafts/draft-1/approve", map[string]any{
			"tenant_id":        e2eTenant.String(),
			"actor_id":         e2eOperator.String(),
			"decision_id":      "decision-1",
			"expected_version": 1,
			"reason":           "",
		})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("утверждение: статус %d, тело %s", response.StatusCode, body)
	}
	decision := decodeBody[struct {
		DecisionID string `json:"decision_id"`
		Outcome    string `json:"outcome"`
		Recorded   bool   `json:"recorded"`
	}](t, body)
	if decision.Outcome != string(domain.DecisionApproved) || !decision.Recorded {
		t.Fatalf("ответ утверждения: %+v", decision)
	}
	if got := pendingApprovedEvents(t, fixture); got != 1 {
		t.Fatalf("после утверждения action-approved в outbox: %d, ожидалось 1", got)
	}

	// Публикация approved-action через relay.
	published, err = fixture.slice.Relay.PublishPending(ctx)
	if err != nil {
		t.Fatalf("публикация approved-action: %v", err)
	}
	if published != 1 {
		t.Fatalf("опубликовано %d контрактов, ожидался 1", published)
	}
	delivered := fixture.sink.delivered()
	if len(delivered) != 1 {
		t.Fatalf("получатель получил %d контрактов, ожидался 1", len(delivered))
	}
	action := delivered[0]
	if err := action.Validate(); err != nil {
		t.Fatalf("опубликованный контракт невалиден: %v", err)
	}
	if action.EventID == "" {
		t.Fatal("relay не проставил event_id конверта")
	}
	if action.Name != contracts.ContractApprovedAction {
		t.Fatalf("имя контракта %q, ожидалось %q", action.Name, contracts.ContractApprovedAction)
	}
	if action.CampaignID != "campaign-1" || action.DraftID != "draft-1" || action.DraftVersion != 1 {
		t.Fatalf("ссылки контракта: %+v", action)
	}
	if action.DecisionID != "decision-1" || action.Channel != domain.ActionChannelEmailName {
		t.Fatalf("решение и канал контракта: %+v", action)
	}
	if action.TenantID != e2eTenant.String() {
		t.Fatalf("тенант контракта %q, ожидался %q", action.TenantID, e2eTenant)
	}

	// Повторный проход не публикует контракт второй раз.
	published, err = fixture.slice.Relay.PublishPending(ctx)
	if err != nil {
		t.Fatalf("повторная публикация: %v", err)
	}
	if published != 0 {
		t.Fatalf("повторно опубликовано %d контрактов, ожидалось 0", published)
	}
	if got := len(fixture.sink.delivered()); got != 1 {
		t.Fatalf("после повторного прохода доставок %d, ожидалась 1", got)
	}
}

// TestDirectExecutionWithoutDecisionDoesNotPublish отдельно фиксирует инвариант
// «ноль исходящих действий без утверждения»: прямой вызов исполнения завершается
// отказом, состояние черновика не меняется, approved-action не публикуется.
func TestDirectExecutionWithoutDecisionDoesNotPublish(t *testing.T) {
	fixture := newE2EFixture(t)
	createCampaignAndDraft(t, fixture)
	ctx := context.Background()

	campaign, err := fixture.slice.Repository.FindByID(ctx, "campaign-1")
	if err != nil {
		t.Fatalf("загрузка кампании: %v", err)
	}
	if err := campaign.ExecuteDraft("draft-1", 1); !errors.Is(err, domain.ErrApprovalRequired) {
		t.Fatalf("исполнение без решения: получено %v, ожидалась ErrApprovalRequired", err)
	}
	if campaign.State() != domain.CampaignStateActive {
		t.Fatalf("состояние кампании после отказа: %q, ожидалось %q", campaign.State(), domain.CampaignStateActive)
	}

	if got := pendingApprovedEvents(t, fixture); got != 0 {
		t.Fatalf("после отказа в исполнении action-approved в outbox: %d, ожидалось 0", got)
	}
	published, err := fixture.slice.Relay.PublishPending(ctx)
	if err != nil {
		t.Fatalf("публикация после отказа: %v", err)
	}
	if published != 0 {
		t.Fatalf("после отказа опубликовано %d контрактов, ожидалось 0", published)
	}
	if delivered := fixture.sink.delivered(); len(delivered) != 0 {
		t.Fatalf("после отказа получатель получил %d контрактов, ожидалось 0", len(delivered))
	}

	loaded, err := fixture.slice.Repository.FindByID(ctx, "campaign-1")
	if err != nil {
		t.Fatalf("повторная загрузка кампании: %v", err)
	}
	draft, ok := loaded.Draft("draft-1")
	if !ok || draft.State() != domain.DraftStatePendingApproval || draft.Version() != 1 {
		t.Fatalf("состояние черновика после отказа: %+v", draft)
	}
}

// TestReadsThroughComposition проверяет, что чтение через собранный срез (очередь
// и журнал) работает с переданным composition decider: корректный запрос получает
// данные, а запрос без тенанта/актора или без права — ошибку доступа. Тест ловит
// регрессию, при которой composition не пробрасывает доступ в читающие use cases.
func TestReadsThroughComposition(t *testing.T) {
	fixture := newE2EFixture(t)
	createCampaignAndDraft(t, fixture)

	// Очередь до решения видит ожидающий черновик: право e2eOperator/e2eTenant
	// проходит проверку доступа, проброшенную composition.
	queueURL := fixture.server.URL + "/campaigns?campaign_id=campaign-1&tenant_id=tenant-1&actor_id=operator-1"
	response, body := doJSON(t, http.MethodGet, queueURL, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("очередь: статус %d, тело %s", response.StatusCode, body)
	}
	queue := decodeBody[struct {
		Items []struct {
			CampaignID string `json:"campaign_id"`
			DraftID    string `json:"draft_id"`
		} `json:"items"`
	}](t, body)
	if len(queue.Items) != 1 || queue.Items[0].CampaignID != "campaign-1" || queue.Items[0].DraftID != "draft-1" {
		t.Fatalf("позиции очереди: %+v", queue.Items)
	}

	// Утверждение точной версии — единственный путь к записи в журнал решений.
	response, body = doJSON(t, http.MethodPost,
		fixture.server.URL+"/campaigns/campaign-1/drafts/draft-1/approve", map[string]any{
			"tenant_id":        e2eTenant.String(),
			"actor_id":         e2eOperator.String(),
			"decision_id":      "decision-1",
			"expected_version": 1,
			"reason":           "",
		})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("утверждение: статус %d, тело %s", response.StatusCode, body)
	}

	journalURL := fixture.server.URL + "/campaigns/campaign-1/drafts/draft-1/decisions?tenant_id=tenant-1&actor_id=operator-1"
	response, body = doJSON(t, http.MethodGet, journalURL, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("журнал: статус %d, тело %s", response.StatusCode, body)
	}
	journal := decodeBody[struct {
		Entries []struct {
			DecisionID string `json:"decision_id"`
			ActorID    string `json:"actor_id"`
			Outcome    string `json:"outcome"`
		} `json:"entries"`
	}](t, body)
	if len(journal.Entries) != 1 ||
		journal.Entries[0].DecisionID != "decision-1" ||
		journal.Entries[0].ActorID != e2eOperator.String() ||
		journal.Entries[0].Outcome != string(domain.DecisionApproved) {
		t.Fatalf("записи журнала: %+v", journal.Entries)
	}

	// После утверждения очередь пуста: PendingApproval исключает решённые черновики.
	response, body = doJSON(t, http.MethodGet, queueURL, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("очередь после решения: статус %d, тело %s", response.StatusCode, body)
	}
	if after := decodeBody[struct {
		Items []json.RawMessage `json:"items"`
	}](t, body); len(after.Items) != 0 {
		t.Fatalf("очередь после решения не пуста: %+v", after.Items)
	}

	// Без тенанта или актора чтение отвергается как ошибка запроса.
	for _, url := range []string{
		"/campaigns/campaign-1/drafts/draft-1/decisions?actor_id=operator-1",
		"/campaigns/campaign-1/drafts/draft-1/decisions?tenant_id=tenant-1",
	} {
		response, body = doJSON(t, http.MethodGet, fixture.server.URL+url, nil)
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: статус %d, ожидался 400, тело %s", url, response.StatusCode, body)
		}
	}

	// Актор без выданного composition права не читает данные тенанта.
	for _, url := range []string{
		"/campaigns?campaign_id=campaign-1&tenant_id=tenant-1&actor_id=intruder",
		"/campaigns/campaign-1/drafts/draft-1/decisions?tenant_id=tenant-1&actor_id=intruder",
	} {
		response, body = doJSON(t, http.MethodGet, fixture.server.URL+url, nil)
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("%s: статус %d, ожидался 403, тело %s", url, response.StatusCode, body)
		}
	}
}
