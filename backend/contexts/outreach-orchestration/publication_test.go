package outreachorchestration_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	outreachorchestration "lead-gen/backend/contexts/outreach-orchestration"
	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports/contracts"
	"lead-gen/backend/platform/transport/amqp"
)

// TestAMQPSinkPublishesApprovedAction подтверждает задачу 3.3: approved-action
// доходит до брокера после commit через transactional outbox (relay), в topic
// exchange контекста-владельца, с ключом маршрутизации <contract>.<major>.
//
// Проверка идёт на фиктивном брокере без внешней сети. Очередь потребителя
// называется <downstream>.<contract> и получает сообщение только при верном
// ключе маршрутизации; отдельная очередь с именем exchange подтверждает, что
// публикация ушла в exchange контекста, а не «куда-нибудь».
func TestAMQPSinkPublishesApprovedAction(t *testing.T) {
	broker := amqp.NewInMemoryBroker(3)
	sink := outreachorchestration.NewAMQPSink(broker, time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	byContract := make(chan amqp.Delivery, 1)
	byExchange := make(chan amqp.Delivery, 1)
	go func() {
		_ = broker.Consume(ctx, "message-delivery.approved-action",
			func(_ context.Context, d amqp.Delivery) error {
				byContract <- d
				return nil
			})
	}()
	go func() {
		_ = broker.Consume(ctx, outreachorchestration.ExchangeOutreachOrchestration,
			func(_ context.Context, d amqp.Delivery) error {
				byExchange <- d
				return nil
			})
	}()

	go func() { _ = sink.Run(ctx) }()

	action := approvedActionFixture()
	if err := sink.Publish(ctx, action); err != nil {
		t.Fatalf("постановка approved-action: %v", err)
	}

	for name, deliveries := range map[string]chan amqp.Delivery{
		"routing key <contract>.<major>": byContract,
		"exchange контекста-владельца":   byExchange,
	} {
		select {
		case delivered := <-deliveries:
			assertDelivered(t, delivered, action)
		case <-time.After(3 * time.Second):
			t.Fatalf("%s: сообщение не доставлено", name)
		}
	}
}

// TestAMQPSinkRejectsInvalidContract фиксирует, что невалидный контракт не
// попадает в outbox: ошибку нужно видеть на стороне издателя, а не терять в
// чужой системе.
func TestAMQPSinkRejectsInvalidContract(t *testing.T) {
	sink := outreachorchestration.NewAMQPSink(amqp.NewInMemoryBroker(1), time.Millisecond)
	if err := sink.Publish(context.Background(), contracts.ApprovedAction{}); err == nil {
		t.Fatal("публикация пустого контракта не отвергнута")
	}
}

// approvedActionFixture собирает валидный approved-action с event_id, который
// доменный relay проставляет перед публикацией.
func approvedActionFixture() contracts.ApprovedAction {
	now := time.Date(2026, time.March, 4, 12, 0, 0, 0, time.UTC)
	action := contracts.NewApprovedAction(contracts.Envelope{
		EventID:       "evt-1",
		OccurredAt:    now,
		TenantID:      "tenant-1",
		CorrelationID: "campaign-1",
		CausationID:   "decision-1",
	})
	action.ApprovedActionID = "draft-1@1"
	action.CampaignID = "campaign-1"
	action.DraftID = "draft-1"
	action.DraftVersion = 1
	action.DecisionID = "decision-1"
	action.Channel = domain.ActionChannelEmailName
	action.RecipientRef = "draft-1"
	action.DecidedAt = now
	return action
}

// assertDelivered сверяет тело и метаданные доставленного сообщения с
// опубликованным контрактом.
func assertDelivered(t *testing.T, delivered amqp.Delivery, action contracts.ApprovedAction) {
	t.Helper()
	if delivered.EventID != action.EventID {
		t.Fatalf("event_id доставки %q, ожидался %q", delivered.EventID, action.EventID)
	}
	var decoded contracts.ApprovedAction
	if err := json.Unmarshal(delivered.Body, &decoded); err != nil {
		t.Fatalf("разбор тела сообщения: %v", err)
	}
	if decoded != action {
		t.Fatalf("тело сообщения %+v, ожидалось %+v", decoded, action)
	}
	if delivered.Headers["name"] != contracts.ContractApprovedAction {
		t.Fatalf("имя контракта в заголовках: %q", delivered.Headers["name"])
	}
}
