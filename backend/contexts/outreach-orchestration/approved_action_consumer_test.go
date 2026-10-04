package outreachorchestration_test

import (
	"context"
	"io"
	"sync"
	"testing"

	outreachorchestration "lead-gen/backend/contexts/outreach-orchestration"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/contracts/outreach-orchestration/approvedaction"
)

// recordingApprovedActionConsumer — внешний потребитель синхронного контракта
// approved-action (например, мост в message-delivery). Он держит только интерфейс
// контракта и запоминает принятые действия: так тест видит, что связь доведена до
// внешней реализации, а не замкнута на встроенный сервис владельца.
type recordingApprovedActionConsumer struct {
	mu      sync.Mutex
	actions []approvedaction.ApprovedAction
}

// Publish реализует approvedaction.Publisher.
func (c *recordingApprovedActionConsumer) Publish(_ context.Context, action approvedaction.ApprovedAction) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.actions = append(c.actions, action)
	return nil
}

// received возвращает копию принятых действий в порядке приёма.
func (c *recordingApprovedActionConsumer) received() []approvedaction.ApprovedAction {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]approvedaction.ApprovedAction, len(c.actions))
	copy(out, c.actions)
	return out
}

// TestInjectedConsumerReceivesApprovedAction — заданный внешний потребитель
// становится реализацией за Slice.ApprovedActionPublisher: доставленное действие
// доходит до внешней реализации, а встроенный in-memory сервис владельца не
// собирается.
func TestInjectedConsumerReceivesApprovedAction(t *testing.T) {
	consumer := &recordingApprovedActionConsumer{}
	slice, err := outreachorchestration.Build(outreachorchestration.Params{
		Risk:                   outreachorchestration.NoUpstreamRiskChecks,
		ApprovedActionConsumer: consumer,
	})
	if err != nil {
		t.Fatalf("сборка среза с внешним потребителем: %v", err)
	}
	if slice.ApprovedActionPublisher == nil {
		t.Fatal("синхронный контракт approved-action не связан в сборке")
	}
	if slice.Publication != nil {
		t.Fatal("встроенный publication-сервис собран, хотя задан внешний потребитель")
	}

	action := contractApprovedActionFixture()
	if err := slice.ApprovedActionPublisher.Publish(context.Background(), action); err != nil {
		t.Fatalf("доставка внешнему потребителю: %v", err)
	}

	received := consumer.received()
	if len(received) != 1 {
		t.Fatalf("внешний потребитель получил %d действий, ожидалось 1", len(received))
	}
	if received[0] != action {
		t.Fatalf("внешний потребитель получил %+v, ожидалось %+v", received[0], action)
	}
}

// TestDefaultWithoutConsumerUsesOwnerPublication — без внешнего потребителя
// сохраняется прежнее поведение: контракт связан со встроенным
// publication-сервисом владельца, и доставленное действие попадает в его журнал
// приёма.
func TestDefaultWithoutConsumerUsesOwnerPublication(t *testing.T) {
	slice, err := outreachorchestration.Build(outreachorchestration.Params{
		Risk: outreachorchestration.NoUpstreamRiskChecks,
	})
	if err != nil {
		t.Fatalf("сборка среза без внешнего потребителя: %v", err)
	}
	if slice.Publication == nil {
		t.Fatal("встроенный publication-сервис владельца не собран без внешнего потребителя")
	}
	if slice.ApprovedActionPublisher == nil {
		t.Fatal("синхронный контракт approved-action не связан в сборке")
	}

	action := contractApprovedActionFixture()
	if err := slice.ApprovedActionPublisher.Publish(context.Background(), action); err != nil {
		t.Fatalf("доставка владельцу: %v", err)
	}
	if accepted := slice.Publication.Accepted(); len(accepted) != 1 {
		t.Fatalf("владелец принял %d действий, ожидалось 1", len(accepted))
	}
}

// TestInjectedConsumerRejectsInvalidAction — невалидное действие не доставляется
// внешнему потребителю: ошибка видна вызывающему, а приёмная сторона не вызывается.
func TestInjectedConsumerRejectsInvalidAction(t *testing.T) {
	consumer := &recordingApprovedActionConsumer{}
	slice, err := outreachorchestration.Build(outreachorchestration.Params{
		Risk:                   outreachorchestration.NoUpstreamRiskChecks,
		ApprovedActionConsumer: consumer,
	})
	if err != nil {
		t.Fatalf("сборка среза с внешним потребителем: %v", err)
	}

	if err := slice.ApprovedActionPublisher.Publish(context.Background(), approvedaction.ApprovedAction{}); err == nil {
		t.Fatal("невалидное действие доставлено внешнему потребителю без ошибки")
	}
	if received := consumer.received(); len(received) != 0 {
		t.Fatalf("внешний потребитель получил %d невалидных действий, ожидалось 0", len(received))
	}
}

// TestNewAcceptsApprovedActionConsumer — точка входа передаёт внешнего потребителя
// из Config в сборку: сервис собирается с ним без ошибок. Это контракт для
// cmd/lead-gen, который заполняет Config.ApprovedActionConsumer.
func TestNewAcceptsApprovedActionConsumer(t *testing.T) {
	consumer := &recordingApprovedActionConsumer{}
	_, err := outreachorchestration.New(outreachorchestration.Config{
		Risk:                   outreachorchestration.NoUpstreamRiskChecks,
		ApprovedActionConsumer: consumer,
		Logger:                 observ.NewLoggerWithWriter(io.Discard),
		Metrics:                observ.NewMetrics(),
		Tracer:                 observ.NewTracer(outreachorchestration.ServiceID),
	})
	if err != nil {
		t.Fatalf("сборка сервиса с внешним потребителем: %v", err)
	}
}
