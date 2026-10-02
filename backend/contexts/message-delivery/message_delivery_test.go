package messagedelivery

import (
	"context"
	"testing"
	"time"

	"lead-gen/backend/contexts/message-delivery/internal/adapters/commandsource"
	"lead-gen/backend/contexts/message-delivery/internal/adapters/outbox/relay"
	"lead-gen/backend/contexts/message-delivery/internal/domain"
	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/backend/platform/transport/amqp"
	"lead-gen/contracts/message-delivery/deliverycommand"
	"lead-gen/contracts/message-delivery/deliveryevent"
)

// captureSink — приёмник публикации для проверок: запоминает опубликованные
// контракты delivery-event.
type captureSink struct {
	events []deliveryevent.DeliveryEvent
}

func (s *captureSink) Publish(_ context.Context, event deliveryevent.DeliveryEvent) error {
	s.events = append(s.events, event)
	return nil
}

var _ relay.Sink = (*captureSink)(nil)

func validPortCommand() deliverycommand.Command {
	return deliverycommand.Command{
		CommandID:        "cmd-1",
		TenantID:         "tenant-1",
		ApprovedActionID: "act-1",
		RecipientRef:     "ref-1",
		DeliveryAddress:  "lead@example.com",
		SenderDomain:     "mail.example.com",
		Channel:          deliverycommand.ChannelEmailName,
		CorrelationID:    "act-1",
	}
}

func queuedDomainCommand(t *testing.T, id string) domain.DeliveryCommand {
	t.Helper()
	address, err := domain.ParseDeliveryAddress("lead@example.com")
	if err != nil {
		t.Fatal(err)
	}
	sender, err := domain.ParseSenderDomain("mail.example.com")
	if err != nil {
		t.Fatal(err)
	}
	return domain.DeliveryCommand{
		CommandID:        domain.CommandID(id),
		TenantID:         domain.TenantID("tenant-1"),
		ApprovedActionID: domain.ApprovedActionID("act-" + id),
		RecipientRef:     domain.RecipientRef("ref-1"),
		Address:          address,
		SenderDomain:     sender,
		CorrelationID:    domain.CorrelationID("act-" + id),
	}
}

// Вертикальный срез: команда контракта доходит до сценария доставки, а исход
// публикуется контрактом delivery-event после commit.
func TestSlice_DeliverCommandAndPublishEvent(t *testing.T) {
	sink := &captureSink{}
	slice, err := Build(Params{Sink: sink})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	port := NewDeliveryCommandPort(slice)
	result, err := port.Deliver(context.Background(), validPortCommand())
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if result.Status != deliverycommand.StatusDelivered || result.AttemptNumber != 1 || !result.Recorded {
		t.Fatalf("результат команды: %+v", result)
	}

	// Повтор команды идемпотентен: второго сообщения и второго эффекта нет.
	replay, err := port.Deliver(context.Background(), validPortCommand())
	if err != nil {
		t.Fatalf("повтор Deliver: %v", err)
	}
	if replay.Recorded {
		t.Fatal("повтор команды не должен создавать новое сообщение")
	}
	if replay.MessageID != result.MessageID {
		t.Fatalf("повтор вернул другое сообщение: %s vs %s", replay.MessageID, result.MessageID)
	}

	published, err := slice.Relay.PublishPending(context.Background())
	if err != nil {
		t.Fatalf("PublishPending: %v", err)
	}
	if published != 1 {
		t.Fatalf("ожидалась одна публикация, got %d", published)
	}
	if len(sink.events) != 1 {
		t.Fatalf("ожидался один контракт, got %d", len(sink.events))
	}
	event := sink.events[0]
	if err := event.Validate(); err != nil {
		t.Fatalf("опубликованный контракт невалиден: %v", err)
	}
	if event.MessageID != "cmd-1" || event.Outcome != deliveryevent.OutcomeDelivered {
		t.Fatalf("контракт: %+v", event)
	}
}

func TestService_RoleRunner(t *testing.T) {
	service, err := New(Config{
		Logger:  observ.NewLogger(),
		Metrics: observ.NewMetrics(),
		Tracer:  observ.NewTracer(ServiceID),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, role := range []composition.Role{composition.RoleSender, composition.RoleWorker, composition.RoleSchedule} {
		if _, ok := service.RoleRunner(role); !ok {
			t.Errorf("роль %q должна быть реализована", role)
		}
	}
	for _, role := range []composition.Role{composition.RoleAPI, composition.RoleIngest, composition.RoleChains} {
		if _, ok := service.RoleRunner(role); ok {
			t.Errorf("роль %q не должна быть реализована", role)
		}
	}
}

func TestService_RequiresObservability(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("сборка без наблюдаемости должна отвергаться")
	}
}

// Роль sender обрабатывает команду из источника и фиксирует событие в outbox.
func TestService_SenderProcessesQueuedCommand(t *testing.T) {
	source := commandsource.New(2)
	if err := source.Enqueue(queuedDomainCommand(t, "cmd-9")); err != nil {
		t.Fatal(err)
	}
	source.Close()

	service, err := New(Config{
		Publisher: amqp.NewInMemoryBroker(3),
		Source:    source,
		Logger:    observ.NewLogger(),
		Metrics:   observ.NewMetrics(),
		Tracer:    observ.NewTracer(ServiceID),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	runner, ok := service.RoleRunner(composition.RoleSender)
	if !ok {
		t.Fatal("роль sender не зарегистрирована")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := runner.Run(ctx); err != nil {
		t.Fatalf("sender: %v", err)
	}
	if got := service.Journal().PendingCount(); got != 1 {
		t.Fatalf("ожидалось одно событие в outbox, got %d", got)
	}
}
