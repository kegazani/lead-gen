package messagedelivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"lead-gen/backend/contexts/message-delivery/internal/adapters/commandsource"
	"lead-gen/backend/contexts/message-delivery/internal/adapters/mailer"
	outboxinmemory "lead-gen/backend/contexts/message-delivery/internal/adapters/outbox/inmemory"
	"lead-gen/backend/contexts/message-delivery/internal/adapters/outbox/relay"
	persistence "lead-gen/backend/contexts/message-delivery/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/message-delivery/internal/adapters/suppression"
	"lead-gen/backend/contexts/message-delivery/internal/app/command"
	"lead-gen/backend/contexts/message-delivery/internal/domain"
	"lead-gen/backend/contexts/message-delivery/internal/ports"
	platformcontracts "lead-gen/backend/platform/contracts"
	outbox "lead-gen/backend/platform/messaging/outbox"
	"lead-gen/backend/platform/transport/amqp"
	"lead-gen/contracts/message-delivery/deliverycommand"
	"lead-gen/contracts/message-delivery/deliveryevent"
)

// Сборка сервиса message-delivery для его ролей (sender, worker, schedule).
//
// Здесь только соединение адаптеров, портов и use cases: ни одного бизнес-правила
// и ни одного решения о допустимости. Инварианты обеспечивают агрегаты message и
// sender-reputation, порядок шагов доставки — прикладной слой, а сборка лишь
// выбирает реализации и передаёт зависимости. Каждый сервис собирает только свой
// контекст, поэтому пакет вправе импортировать adapters своего контекста.
//
// В срезе подключаются in-memory адаптеры: реальный SMTP-адаптер (integration-
// platform, contract provider-port) и интеграция стоп-листа (data-governance,
// contract suppression-policy) не подключены.

// SystemClock — часы процесса, источник времени по умолчанию. Не адаптер
// внешней системы: используется, когда вызывающий не передал свой Clock.
type SystemClock struct{}

// Now реализует ports.Clock.
func (SystemClock) Now() time.Time { return time.Now() }

// Params — параметры сборки среза. Репозитории и журнал можно передать снаружи,
// чтобы разделить состояние нескольких потребителей; nil означает создание
// новых.
type Params struct {
	// Clock — источник времени. nil → SystemClock.
	Clock ports.Clock

	// Messages — репозиторий сообщений. nil → новый in-memory репозиторий.
	Messages *persistence.Messages

	// Reputations — репозиторий репутаций. nil → новый in-memory репозиторий.
	Reputations *persistence.Reputations

	// Journal — журнал outbox. nil → новый журнал.
	Journal *outboxinmemory.Journal

	// Sink — назначение публикации delivery-event. nil означает, что relay не
	// собирается: процесс не публикует контракт (например, сборка без брокера).
	Sink relay.Sink

	// Mailer — порт отправки почты. nil → in-memory адаптер. Честный
	// недоступный адаптер (mailer.Unavailable) подключает вызывающий явно.
	Mailer ports.Mailer

	// Suppressions — стоп-лист получателей. nil → пустой in-memory стоп-лист.
	Suppressions ports.SuppressionList

	// Source — источник команд доставки для роли sender. nil → очередь в памяти.
	Source ports.CommandSource
}

// Slice — собранный контекст среза: порты, use case и публикация. Поля
// экспортированы, чтобы процесс мог запустить нужную часть.
type Slice struct {
	Messages    *persistence.Messages
	Reputations *persistence.Reputations
	Journal     *outboxinmemory.Journal
	UnitOfWork  *outboxinmemory.UnitOfWork

	// Relay не равен nil только если при сборке задан Sink.
	Relay *relay.Relay

	Mailer       ports.Mailer
	Suppressions ports.SuppressionList
	Source       ports.CommandSource

	// DeliverMessage — use case доставки, вход единственной роли отправки sender.
	DeliverMessage *command.DeliverMessageHandler
}

// Build собирает контекст среза из in-memory адаптеров и переданных
// зависимостей.
func Build(params Params) (*Slice, error) {
	clock := params.Clock
	if clock == nil {
		clock = SystemClock{}
	}

	messages := params.Messages
	if messages == nil {
		messages = persistence.NewMessages()
	}
	reputations := params.Reputations
	if reputations == nil {
		reputations = persistence.NewReputations()
	}
	journal := params.Journal
	if journal == nil {
		journal = outboxinmemory.NewJournal()
	}
	unitOfWork := outboxinmemory.NewUnitOfWorkWith(messages, reputations, journal)

	mail := params.Mailer
	if mail == nil {
		mail = mailer.New()
	}
	suppressions := params.Suppressions
	if suppressions == nil {
		suppressions = suppression.New()
	}
	source := params.Source
	if source == nil {
		source = commandsource.New(0)
	}

	var publication *relay.Relay
	if params.Sink != nil {
		publication = relay.New(journal, params.Sink)
	}

	deliver := command.NewDeliverMessageHandler(suppressions, mail, clock, unitOfWork)

	return &Slice{
		Messages:       messages,
		Reputations:    reputations,
		Journal:        journal,
		UnitOfWork:     unitOfWork,
		Relay:          publication,
		Mailer:         mail,
		Suppressions:   suppressions,
		Source:         source,
		DeliverMessage: deliver,
	}, nil
}

// NewDeliveryCommandPort собирает опубликованный контракт delivery-command поверх
// собранного среза: команду принимает сценарий доставки.
//
// Это публичная фабрика владельца: точка сборки не имеет доступа к внутренним
// пакетам контекста, поэтому прямой адаптер контракта объявлен здесь. При
// co-location он вызывается в процессе; при разделении сервисов ту же форму даёт
// сетевой клиент.
func NewDeliveryCommandPort(slice *Slice) deliverycommand.Port {
	if slice == nil {
		return deliverycommand.InProc(deliverycommand.InProcHandler{})
	}
	return deliverycommand.InProc(deliverycommand.InProcHandler{
		DeliverFunc: func(ctx context.Context, cmd deliverycommand.Command) (deliverycommand.Result, error) {
			domainCmd, err := slice.mapCommand(cmd)
			if err != nil {
				return deliverycommand.Result{}, fmt.Errorf("%w: %v", deliverycommand.ErrRejected, err)
			}
			result, err := slice.DeliverMessage.Handle(ctx, domainCmd)
			if err != nil {
				return deliverycommand.Result{}, portError(err)
			}
			return deliverycommand.Result{
				MessageID:     result.MessageID.String(),
				Status:        deliverycommand.Status(result.Status),
				AttemptNumber: result.AttemptNumber,
				Recorded:      !result.Replayed,
			}, nil
		},
	})
}

// mapCommand переводит команду контракта в доменную: проверяет форму и разбирает
// адрес и отправляющий домен в value objects.
func (s *Slice) mapCommand(cmd deliverycommand.Command) (domain.DeliveryCommand, error) {
	if err := cmd.Validate(); err != nil {
		return domain.DeliveryCommand{}, err
	}
	address, err := domain.ParseDeliveryAddress(cmd.DeliveryAddress)
	if err != nil {
		return domain.DeliveryCommand{}, err
	}
	sender, err := domain.ParseSenderDomain(cmd.SenderDomain)
	if err != nil {
		return domain.DeliveryCommand{}, err
	}
	return domain.DeliveryCommand{
		CommandID:        domain.CommandID(cmd.CommandID),
		TenantID:         domain.TenantID(cmd.TenantID),
		ApprovedActionID: domain.ApprovedActionID(cmd.ApprovedActionID),
		RecipientRef:     domain.RecipientRef(cmd.RecipientRef),
		Address:          address,
		SenderDomain:     sender,
		CorrelationID:    domain.CorrelationID(cmd.CorrelationID),
		CausationID:      cmd.CausationID,
	}, nil
}

// portError переводит ошибку сценария в исход опубликованного контракта:
// доменный отказ — ErrRejected, прочее — ErrUnavailable. Ошибка не выдаётся за
// успех.
func portError(err error) error {
	switch {
	case errors.Is(err, domain.ErrRecipientSuppressed),
		errors.Is(err, domain.ErrSenderSuspended),
		errors.Is(err, domain.ErrApprovedActionRequired),
		errors.Is(err, domain.ErrEmptyDeliveryAddress),
		errors.Is(err, domain.ErrInvalidDeliveryAddress),
		errors.Is(err, domain.ErrEmptySenderDomain),
		errors.Is(err, domain.ErrInvalidSenderDomain),
		errors.Is(err, domain.ErrEmptyID),
		errors.Is(err, domain.ErrIdempotencyKeyRequired):
		return fmt.Errorf("%w: %v", deliverycommand.ErrRejected, err)
	default:
		return fmt.Errorf("%w: %v", deliverycommand.ErrUnavailable, err)
	}
}

// ExchangeMessageDelivery — topic exchange контекста-владельца. Имя совпадает с
// идентификатором сервиса.
const ExchangeMessageDelivery = ServiceID

// AMQPSink связывает публикацию delivery-event с транспортом.
//
// Он реализует relay.Sink среза: получив контракт, сериализует его, вычисляет
// ключ маршрутизации <contract>.<major> и ставит запись в transactional outbox
// платформы. Доставку в брокер выполняет outbox.Relay через amqp.Publisher:
// публикация подтверждается брокером (publisher confirm), а при сбое запись
// остаётся в журнале и повторяется. Повтор не создаёт второго эффекта, потому
// что получатель идемпотентен по event_id.
//
// Так «изменение агрегата сохранено, но событие не ушло» невозможно: доменный
// relay вызывает Sink только после commit, а транспортный outbox не теряет
// запись до подтверждения брокером.
type AMQPSink struct {
	journal *outbox.MemoryJournal
	relay   *outbox.Relay
}

// NewAMQPSink собирает адаптер публикации над издателем брокера. Некорректный
// интервал заменяется значением по умолчанию внутри outbox.NewRelay.
func NewAMQPSink(publisher amqp.Publisher, interval time.Duration) *AMQPSink {
	journal := outbox.NewMemoryJournal()
	return &AMQPSink{
		journal: journal,
		relay:   outbox.NewRelay(journal, publisher, interval),
	}
}

// Publish реализует relay.Sink. Контракт проверяется и записывается в outbox;
// ошибка означает, что запись не принята и доменный relay не подтвердит событие.
func (s *AMQPSink) Publish(ctx context.Context, event deliveryevent.DeliveryEvent) error {
	if err := event.Validate(); err != nil {
		return fmt.Errorf("sink delivery-event: %w", err)
	}
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("sink delivery-event: сериализация контракта: %w", err)
	}
	routingKey, err := platformcontracts.RoutingKey(event.Name, event.SchemaVersion)
	if err != nil {
		return fmt.Errorf("sink delivery-event: ключ маршрутизации: %w", err)
	}
	if err := s.journal.Append(ctx, outbox.Message{
		ID:         event.EventID,
		Exchange:   ExchangeMessageDelivery,
		RoutingKey: routingKey,
		Body:       body,
		Headers:    envelopeHeaders(event),
	}); err != nil {
		return fmt.Errorf("sink delivery-event: постановка в outbox: %w", err)
	}
	return nil
}

// Run публикует записи транспортного outbox в брокер до отмены контекста. Ошибка
// отдельного прохода не теряет запись: она будет повторена.
func (s *AMQPSink) Run(ctx context.Context) error {
	return s.relay.Run(ctx)
}

// envelopeHeaders переносит метаданные конверта в заголовки сообщения. Имена
// совпадают с полями конверта (platform/contracts.Envelope) и ключом event_id
// брокера, поэтому потребитель дедуплицирует и коррелирует без разбора тела.
func envelopeHeaders(event deliveryevent.DeliveryEvent) map[string]string {
	headers := map[string]string{
		"event_id":       event.EventID,
		"name":           event.Name,
		"schema_version": event.SchemaVersion,
		"tenant_id":      event.TenantID,
		"correlation_id": event.CorrelationID,
	}
	if event.CausationID != "" {
		headers["causation_id"] = event.CausationID
	}
	return headers
}

// Утверждение, что адаптер удовлетворяет порт публикации среза.
var _ relay.Sink = (*AMQPSink)(nil)
