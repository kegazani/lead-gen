package outreachorchestration_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	outreachorchestration "lead-gen/backend/contexts/outreach-orchestration"
	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/platform/observability/observ"
)

// approveDraftForDelivery проходит HTTP-команду утверждения точной версии
// черновика и проверяет успех.
func approveDraftForDelivery(t *testing.T, baseURL string) {
	t.Helper()
	response, body := doJSON(t, http.MethodPost,
		baseURL+"/campaigns/campaign-1/drafts/draft-1/approve", map[string]any{
			"tenant_id":        e2eTenant.String(),
			"actor_id":         e2eOperator.String(),
			"decision_id":      "decision-1",
			"expected_version": 1,
			"reason":           "",
		})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("утверждение черновика: статус %d, тело %s", response.StatusCode, body)
	}
}

// assertDeliveredApprovedAction проверяет, что доставлен ровно один корректный
// контракт approved-action, соответствующий утверждённому черновику.
func assertDeliveredApprovedAction(t *testing.T, consumer *recordingSink) {
	t.Helper()
	delivered := consumer.delivered()
	if len(delivered) != 1 {
		t.Fatalf("внешний потребитель получил %d действий, ожидалось 1", len(delivered))
	}
	action := delivered[0]
	if err := action.Validate(); err != nil {
		t.Fatalf("доставленный контракт невалиден: %v", err)
	}
	if action.EventID == "" {
		t.Fatal("relay не проставил event_id конверта")
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
}

// TestApprovedActionConsumerReceivesApprovedActionViaRelay — ключевой сценарий:
// задан внешний потребитель (Config.ApprovedActionConsumer), утверждение черновика
// проходит путь approve → outbox → relay, и действие доходит до внешнего
// потребителя ровно один раз; транспортный AMQP-Sink при этом не участвует.
func TestApprovedActionConsumerReceivesApprovedActionViaRelay(t *testing.T) {
	consumer := &recordingSink{}
	slice, err := outreachorchestration.Build(outreachorchestration.Params{
		Clock:                  fixedClock{now: e2eNow()},
		Grants:                 []outreachorchestration.Grant{{Actor: e2eOperator, Tenant: e2eTenant}},
		ApprovedActionConsumer: consumer,
		Risk:                   noRiskFacts,
	})
	if err != nil {
		t.Fatalf("сборка среза с внешним потребителем: %v", err)
	}
	if slice.Relay == nil {
		t.Fatal("relay не собран, хотя задан внешний потребитель")
	}

	server := httptest.NewServer(slice.Server)
	t.Cleanup(server.Close)
	fixture := &e2eFixture{slice: slice, server: server, sink: consumer}

	createCampaignAndDraft(t, fixture)
	if got := pendingApprovedEvents(t, fixture); got != 0 {
		t.Fatalf("до утверждения action-approved в outbox: %d, ожидалось 0", got)
	}

	approveDraftForDelivery(t, fixture.server.URL)
	if got := pendingApprovedEvents(t, fixture); got != 1 {
		t.Fatalf("после утверждения action-approved в outbox: %d, ожидалось 1", got)
	}

	published, err := slice.Relay.PublishPending(context.Background())
	if err != nil {
		t.Fatalf("публикация approved-action: %v", err)
	}
	if published != 1 {
		t.Fatalf("опубликовано %d контрактов, ожидался 1", published)
	}
	assertDeliveredApprovedAction(t, consumer)

	// Повторный проход не доставляет действие второй раз: идемпотентность relay.
	published, err = slice.Relay.PublishPending(context.Background())
	if err != nil {
		t.Fatalf("повторная публикация: %v", err)
	}
	if published != 0 {
		t.Fatalf("повторно опубликовано %d контрактов, ожидалось 0", published)
	}
	if got := len(consumer.delivered()); got != 1 {
		t.Fatalf("после повторного прохода доставок %d, ожидалась 1", got)
	}
}

// TestApprovedActionSinkReceivesApprovedActionViaRelay — явное назначение релиза
// (Config.ApprovedActionSink, здесь через Params.ApprovedActionSink) подменяет
// транспортный Sink: утверждённое действие доходит до заданной функции.
func TestApprovedActionSinkReceivesApprovedActionViaRelay(t *testing.T) {
	consumer := &recordingSink{}
	slice, err := outreachorchestration.Build(outreachorchestration.Params{
		Clock:              fixedClock{now: e2eNow()},
		Grants:             []outreachorchestration.Grant{{Actor: e2eOperator, Tenant: e2eTenant}},
		ApprovedActionSink: outreachorchestration.SinkFunc(consumer.Publish),
		Risk:               noRiskFacts,
	})
	if err != nil {
		t.Fatalf("сборка среза с внешним sink: %v", err)
	}
	if slice.Relay == nil {
		t.Fatal("relay не собран, хотя задан внешний sink")
	}

	server := httptest.NewServer(slice.Server)
	t.Cleanup(server.Close)
	fixture := &e2eFixture{slice: slice, server: server, sink: consumer}

	createCampaignAndDraft(t, fixture)
	approveDraftForDelivery(t, fixture.server.URL)

	published, err := slice.Relay.PublishPending(context.Background())
	if err != nil {
		t.Fatalf("публикация approved-action: %v", err)
	}
	if published != 1 {
		t.Fatalf("опубликовано %d контрактов, ожидался 1", published)
	}
	assertDeliveredApprovedAction(t, consumer)
}

// TestBuildWithoutDeliveryDestinationLeavesRelayUnassembled — без внешнего sink,
// потребителя и транспортного Publisher поведение прежнее: relay не собирается,
// встроенный publication-сервис владельца остаётся доступен.
func TestBuildWithoutDeliveryDestinationLeavesRelayUnassembled(t *testing.T) {
	slice, err := outreachorchestration.Build(outreachorchestration.Params{
		Risk: noRiskFacts,
	})
	if err != nil {
		t.Fatalf("сборка среза по умолчанию: %v", err)
	}
	if slice.Relay != nil {
		t.Fatal("relay собран без назначения доставки")
	}
	if slice.Publication == nil {
		t.Fatal("встроенный publication-сервис владельца не собран")
	}
}

// TestNewAcceptsApprovedActionSink — точка входа принимает внешний sink из Config
// без ошибок: это контракт проводки поля для cmd/lead-gen.
func TestNewAcceptsApprovedActionSink(t *testing.T) {
	consumer := &recordingSink{}
	_, err := outreachorchestration.New(outreachorchestration.Config{
		Risk:               noRiskFacts,
		ApprovedActionSink: outreachorchestration.SinkFunc(consumer.Publish),
		Logger:             observ.NewLoggerWithWriter(io.Discard),
		Metrics:            observ.NewMetrics(),
		Tracer:             observ.NewTracer(outreachorchestration.ServiceID),
	})
	if err != nil {
		t.Fatalf("сборка сервиса с внешним sink: %v", err)
	}
}
