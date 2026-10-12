package messagedelivery

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

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

// NewPostgresApprovedActionBridge собирает тот же bridge, но принимает команды
// через durable PostgreSQL inbox. Вызывающая composition обязана применить
// миграции message-delivery до создания bridge; queue и service должны читать
// одну и ту же БД.
func NewPostgresApprovedActionBridge(
	pool *pgxpool.Pool,
	recipients deliverytarget.Resolver,
	materials ports.MaterialResolver,
	senderDomain string,
) (*ApprovedActionBridge, error) {
	source, publisher, err := NewPostgresApprovedActionSource(pool, recipients, materials, senderDomain, 30*time.Second)
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

// Publisher возвращает приёмник утверждённых действий моста. Он используется
// приёмной HTTP-поверхностью контракта (Config.Intake) при разделённой
// топологии: та же сущность, что отдаёт команды роли sender, принимает действие.
//
// На nil-мосте или мосте без собранного приёмника возвращается nil: отсутствие
// проводки не маскируется выдуманным приёмником.
func (b *ApprovedActionBridge) Publisher() approvedaction.Publisher {
	if b == nil {
		return nil
	}
	return b.publisher
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

// CancelCampaign устанавливает fence по точной паре tenant/campaign и не
// пытается прервать SMTP-вызов, уже начавшийся после reservation.
func (b *ApprovedActionBridge) CancelCampaign(ctx context.Context, cancellation approvedaction.CampaignCancellation) error {
	if b == nil || b.publisher == nil {
		return errors.New("message-delivery: cancellation bridge недоступен")
	}
	if err := cancellation.Validate(); err != nil {
		return err
	}
	canceller, ok := b.publisher.(approvedaction.Canceller)
	if !ok {
		return errors.New("message-delivery: publisher не поддерживает отмену кампании")
	}
	return canceller.CancelCampaign(ctx, cancellation)
}

var _ approvedaction.Canceller = (*ApprovedActionBridge)(nil)
