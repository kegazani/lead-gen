package outreachorchestration_test

import (
	"context"
	"strings"
	"testing"
	"time"

	outreachorchestration "lead-gen/backend/contexts/outreach-orchestration"
	"lead-gen/contracts/outreach-orchestration/approvedaction"
)

// approvedActionConsumer — потребитель синхронного контракта approved-action. Он
// держит только интерфейс контракта и не импортирует пакеты владельца: так
// выглядит другая сторона связи после разделения сервисов. Реализацию владельца
// он получает из сборки, а не из внутренних пакетов контекста.
type approvedActionConsumer struct {
	publisher approvedaction.Publisher
}

// deliver доставляет утверждённое действие владельцу через интерфейс контракта.
func (c approvedActionConsumer) deliver(ctx context.Context, action approvedaction.ApprovedAction) error {
	return c.publisher.Publish(ctx, action)
}

// TestApprovedActionContractBindsOwnerImplementation — контрактный тест связи:
// потребитель, держащий интерфейс контракта, получает реализацию владельца.
//
// Потребитель не видит ни одного внутреннего пакета контекста. Успешная доставка
// и запись в журнал приёма владельца доказывают, что за интерфейсом стоит именно
// реализация владельца, а не её копия у потребителя.
func TestApprovedActionContractBindsOwnerImplementation(t *testing.T) {
	slice, err := outreachorchestration.Build(outreachorchestration.Params{
		Risk: outreachorchestration.NoUpstreamRiskChecks,
	})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	if slice.ApprovedActionPublisher == nil {
		t.Fatal("синхронный контракт approved-action не связан в сборке")
	}

	consumer := approvedActionConsumer{publisher: slice.ApprovedActionPublisher}
	action := contractApprovedActionFixture()

	if err := consumer.deliver(context.Background(), action); err != nil {
		t.Fatalf("доставка approved-action: %v", err)
	}

	accepted := slice.Publication.Accepted()
	if len(accepted) != 1 {
		t.Fatalf("владелец принял %d действий, ожидалось 1", len(accepted))
	}
	if accepted[0] != action {
		t.Fatalf("владелец принял %+v, ожидалось %+v", accepted[0], action)
	}
}

// TestApprovedActionContractIsIdempotentAcrossTransport — повторная доставка того
// же действия не создаёт второго эффекта и не зависит от транспорта: за
// интерфейсом стоит адаптер, соблюдающий ключ идемпотентности контракта.
func TestApprovedActionContractIsIdempotentAcrossTransport(t *testing.T) {
	slice, err := outreachorchestration.Build(outreachorchestration.Params{
		Risk: outreachorchestration.NoUpstreamRiskChecks,
	})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	consumer := approvedActionConsumer{publisher: slice.ApprovedActionPublisher}

	action := contractApprovedActionFixture()
	if err := consumer.deliver(context.Background(), action); err != nil {
		t.Fatalf("первая доставка: %v", err)
	}
	repeat := action
	repeat.EventID = "evt-repeat"
	if err := consumer.deliver(context.Background(), repeat); err != nil {
		t.Fatalf("повторная доставка: %v", err)
	}
	if accepted := slice.Publication.Accepted(); len(accepted) != 1 {
		t.Fatalf("повторная доставка применена %d раз, ожидался 1", len(accepted))
	}
}

// TestParseTransportUnknownMechanism — неизвестный механизм группы процессов
// отвергается на сборке: расхождение топологии и сборки не должно проходить
// молча.
func TestParseTransportUnknownMechanism(t *testing.T) {
	if _, err := outreachorchestration.ParseTransport("amqp"); err == nil {
		t.Fatal("механизм amqp не отвергнут для синхронного контракта")
	}
	if got, err := outreachorchestration.ParseTransport(""); err != nil || got != outreachorchestration.TransportInProcess {
		t.Fatalf("пустой механизм: got (%q, %v), ожидался in-process", got, err)
	}
}

// TestLinkNetworkTransportIsExtensionPoint — сетевой транспорт остаётся точкой
// расширения: связывание честно сообщает, что адаптера ещё нет, а не молча
// подставляет in-process.
func TestLinkNetworkTransportIsExtensionPoint(t *testing.T) {
	if _, err := outreachorchestration.LinkApprovedActionPublisher(outreachorchestration.TransportNetwork, nil); err == nil {
		t.Fatal("сетевой транспорт связан, хотя адаптер не реализован")
	} else if !strings.Contains(err.Error(), "точка расширения") {
		t.Fatalf("ошибка не называет сетевой транспорт точкой расширения: %v", err)
	}
}

// contractApprovedActionFixture собирает валидный approved-action публичного
// контракта.
func contractApprovedActionFixture() approvedaction.ApprovedAction {
	instant := time.Date(2026, time.September, 20, 12, 0, 0, 0, time.UTC)
	action := approvedaction.NewApprovedAction(approvedaction.Envelope{
		EventID:       "evt-1",
		OccurredAt:    instant,
		TenantID:      "tenant-1",
		CorrelationID: "camp-1",
		CausationID:   "dec-1",
	})
	action.ApprovedActionID = "draft-1@1"
	action.CampaignID = "camp-1"
	action.DraftID = "draft-1"
	action.DraftVersion = 1
	action.DecisionID = "dec-1"
	action.Channel = approvedaction.ChannelEmailName
	action.RecipientRef = "draft-1"
	action.DecidedAt = instant
	return action
}
