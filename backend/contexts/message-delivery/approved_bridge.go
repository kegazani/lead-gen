package messagedelivery

import (
	"context"

	"lead-gen/backend/contexts/message-delivery/internal/ports"
	"lead-gen/contracts/lead-management/deliverytarget"
	approvedaction "lead-gen/contracts/outreach-orchestration/approvedaction"
)

// ApprovedActionBridge — интеграционно-дружественная обёртка над мостом
// утверждённого действия. Она скрывает внутренние типы контекста от точки
// сборки: cmd/lead-gen не именует ports.CommandSource и работает только с
// *ApprovedActionBridge, его Bind и Sink.
//
// Источник команд и приёмник утверждённого действия — одна и та же внутренняя
// сущность моста. Иначе утверждённое действие ушло бы в один экземпляр, а роль
// sender прочитала бы команду из другого, и путь «утверждено → отправлено»
// разорвался бы.
type ApprovedActionBridge struct {
	source    ports.CommandSource
	publisher approvedaction.Publisher
}

// NewApprovedActionBridge собирает мост поверх опубликованных контрактов.
// Получателя разрешает владелец контакта (delivery-target); materials —
// необязательный резолвер материала письма (см. NewApprovedActionSource).
func NewApprovedActionBridge(
	recipients deliverytarget.Resolver,
	materials ports.MaterialResolver,
	senderDomain string,
) (*ApprovedActionBridge, error) {
	source, publisher, err := NewApprovedActionSource(recipients, materials, senderDomain)
	if err != nil {
		return nil, err
	}
	return &ApprovedActionBridge{source: source, publisher: publisher}, nil
}

// Bind ставит источник команд моста в конфигурацию сервиса (cfg.Source), чтобы
// роль sender читала собранные команды. Вызов на nil-мосте или nil-конфигурации
// — no-op.
func (b *ApprovedActionBridge) Bind(cfg *Config) {
	if b == nil || cfg == nil {
		return
	}
	cfg.Source = b.source
}

// Sink возвращает функцию публикации утверждённого действия. Её тип совпадает с
// outreachorchestration.SinkFunc: func(context.Context, approvedaction.ApprovedAction) error.
// Точка сборки передаёт результат напрямую в конфигурацию outreach
// (ApprovedActionSink), не именуя внутренние типы контекста.
//
// На nil-мосте возвращается nil: SinkFunc(nil) ничего не доставляет и не
// маскирует отсутствие проводки выдуманным получателем.
func (b *ApprovedActionBridge) Sink() func(ctx context.Context, action approvedaction.ApprovedAction) error {
	if b == nil || b.publisher == nil {
		return nil
	}
	publisher := b.publisher
	return func(ctx context.Context, action approvedaction.ApprovedAction) error {
		return publisher.Publish(ctx, action)
	}
}
