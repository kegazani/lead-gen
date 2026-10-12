package messagedelivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/message-delivery/internal/adapters/commandsource"
	approvedactionadapter "lead-gen/backend/contexts/message-delivery/internal/adapters/commandsource/approvedaction"
	approvedactionpostgres "lead-gen/backend/contexts/message-delivery/internal/adapters/commandsource/postgres"
	"lead-gen/backend/contexts/message-delivery/internal/adapters/mailer"
	"lead-gen/backend/contexts/message-delivery/internal/adapters/mailer/provider"
	outboxinmemory "lead-gen/backend/contexts/message-delivery/internal/adapters/outbox/inmemory"
	"lead-gen/backend/contexts/message-delivery/internal/adapters/outbox/relay"
	persistence "lead-gen/backend/contexts/message-delivery/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/message-delivery/internal/adapters/reference"
	"lead-gen/backend/contexts/message-delivery/internal/adapters/suppression"
	suppressionpolicyadapter "lead-gen/backend/contexts/message-delivery/internal/adapters/suppression/policy"
	"lead-gen/backend/contexts/message-delivery/internal/app/command"
	"lead-gen/backend/contexts/message-delivery/internal/domain"
	"lead-gen/backend/contexts/message-delivery/internal/ports"
	platformcontracts "lead-gen/backend/platform/contracts"
	outbox "lead-gen/backend/platform/messaging/outbox"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/backend/platform/transport/amqp"
	"lead-gen/contracts/data-governance/suppressionpolicy"
	"lead-gen/contracts/integration-platform/mail"
	deliverytarget "lead-gen/contracts/lead-management/deliverytarget"
	"lead-gen/contracts/message-delivery/deliverycommand"
	"lead-gen/contracts/message-delivery/deliveryevent"
	approvedaction "lead-gen/contracts/outreach-orchestration/approvedaction"
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

// Params — параметры сборки среза. Порты можно передать снаружи — тогда
// состояние разделяется несколькими потребителями (ролями). Нулевые значения
// означают in-memory адаптеры среза.
type Params struct {
	// Clock — источник времени. nil → SystemClock.
	Clock ports.Clock

	// UnitOfWork — транзакция контекста. Не nil → внешняя (например,
	// PostgreSQL); nil → in-memory unit-of-work срезa.
	UnitOfWork ports.UnitOfWork

	// RelaySource — журнал outbox для публикации. Обязателен, если задан
	// UnitOfWork: публикация читает то же состояние, что пишет транзакция.
	RelaySource relay.Source

	// Retryables — выборка сообщений для повторных окон. Не nil — используется
	// ролью schedule; nil отключает повторные окна.
	Retryables ports.RetryableMessages

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

	// MaxRetryAttempts — предельное число попыток доставки. <= 0 отключает
	// повторные окна.
	MaxRetryAttempts int
}

// Slice — собранный контекст среза: порты, use cases и публикация. Поля
// экспортированы, чтобы процесс мог запустить нужную часть.
type Slice struct {
	// Journal — in-memory журнал outbox. nil, если состояние вынесено во внешнюю
	// транзакцию (PostgreSQL).
	Journal *outboxinmemory.Journal

	UnitOfWork ports.UnitOfWork

	// Relay не равен nil только если при сборке задан Sink.
	Relay *relay.Relay

	Mailer       ports.Mailer
	Suppressions ports.SuppressionList
	Source       ports.CommandSource

	// DeliverMessage — use case доставки, вход единственной роли отправки sender.
	DeliverMessage *command.DeliverMessageHandler

	// RetryPending — use case повторных окон роли schedule. nil, если повторные
	// окна отключены или не задана выборка.
	RetryPending *command.RetryPendingHandler
}

// Build собирает контекст среза из in-memory адаптеров и переданных
// зависимостей. Если задан UnitOfWork, состояние берётся из него, а in-memory
// репозитории не создаются.
func Build(params Params) (*Slice, error) {
	clock := params.Clock
	if clock == nil {
		clock = SystemClock{}
	}

	unitOfWork := params.UnitOfWork
	relaySource := params.RelaySource
	retryables := params.Retryables
	var journal *outboxinmemory.Journal
	if unitOfWork == nil {
		messages := persistence.NewMessages()
		reputations := persistence.NewReputations()
		journal = outboxinmemory.NewJournal()
		unitOfWork = outboxinmemory.NewUnitOfWorkWith(messages, reputations, journal)
		relaySource = journal
		retryables = messages
	}

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
	if binder, ok := unitOfWork.(ports.CampaignFenceBinder); ok {
		if reader, ok := source.(ports.CampaignFenceReader); ok {
			binder.BindCampaignFence(reader)
		}
	}

	var publication *relay.Relay
	if params.Sink != nil && relaySource != nil {
		publication = relay.New(relaySource, params.Sink)
	}

	deliver := command.NewDeliverMessageHandler(suppressions, mail, clock, unitOfWork)

	var retry *command.RetryPendingHandler
	if retryables != nil {
		retry = command.NewRetryPendingHandler(retryables, mail, clock, unitOfWork, params.MaxRetryAttempts)
	}

	return &Slice{
		Journal:        journal,
		UnitOfWork:     unitOfWork,
		Relay:          publication,
		Mailer:         mail,
		Suppressions:   suppressions,
		Source:         source,
		DeliverMessage: deliver,
		RetryPending:   retry,
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
		CampaignID:       cmd.CampaignID,
		RecipientRef:     domain.RecipientRef(cmd.RecipientRef),
		Address:          address,
		SenderDomain:     sender,
		Subject:          cmd.Subject,
		Body:             cmd.Body,
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

// NewProviderMailer собирает реальный почтовый адаптер поверх контракта mail
// владельца integration-platform. Точка сборки передаёт сюда SMTP-адаптер и
// адрес отправителя; контекст message-delivery не импортирует чужие внутренние
// пакеты.
func NewProviderMailer(port mail.Port, from string) (ports.Mailer, error) {
	return provider.New(provider.Config{Port: port, From: from})
}

// NewSuppressionList собирает стоп-лист поверх контракта suppression-policy
// владельца data-governance. Недоступность источника остаётся ошибкой, а не
// пустым списком.
func NewSuppressionList(port suppressionpolicy.Port) (ports.SuppressionList, error) {
	return suppressionpolicyadapter.New(port)
}

// NewApprovedActionSource собирает мост утверждённого действия: источник команд
// роли sender (Params.Source / Config.Source) и приёмник утверждённых действий
// (approved-action.Publisher). Это одна и та же сущность: `Publish`/`Enqueue`
// принимает утверждённое действие, `Next` отдаёт собранную команду роли sender.
//
// Публичная фабрика владельца: точка сборки (cmd/lead-gen) не имеет доступа к
// внутренним пакетам контекста, поэтому адаптер контракта delivery-target объявлен
// внутри контекста. Получателя разрешает владелец контакта (lead-management,
// связь leads-for-delivery); ссылка переводится в адрес в момент доставки, а
// approved-action остаётся ссылочным и без PII.
//
// materials — резолвер материала письма поверх контракта content-version
// владельца content-production (см. NewContentMaterialResolver). Если задан
// MaterialVersionRef, а резолвер не подключён, приём честно сообщает о
// недоступности, а содержимое не выдумывается (AGENTS.md §3).
func NewApprovedActionSource(
	recipients deliverytarget.Resolver,
	materials ports.MaterialResolver,
	senderDomain string,
) (ports.CommandSource, approvedaction.Publisher, error) {
	recipientResolver, err := reference.NewRecipientResolver(recipients)
	if err != nil {
		return nil, nil, fmt.Errorf("message-delivery: резолвер получателя: %w", err)
	}
	bridge, err := approvedactionadapter.New(approvedactionadapter.Config{
		Recipients:   recipientResolver,
		Materials:    materials,
		SenderDomain: senderDomain,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("message-delivery: мост approved-action: %w", err)
	}
	return bridge, bridge, nil
}

// NewPostgresApprovedActionSource собирает approved-action bridge с долговечной
// PostgreSQL inbox. Возвращённые source/publisher предназначены для последующей
// явной передачи в Config.Source и Config.Intake; composition root не подключает
// их автоматически. Pool должен использовать уже применённую миграцию 0002.
func NewPostgresApprovedActionSource(
	pool *pgxpool.Pool,
	recipients deliverytarget.Resolver,
	materials ports.MaterialResolver,
	senderDomain string,
	lease time.Duration,
) (ports.ClaimedCommandSource, approvedaction.Publisher, error) {
	queue, err := approvedactionpostgres.NewApprovedQueue(pool, lease)
	if err != nil {
		return nil, nil, fmt.Errorf("message-delivery: PostgreSQL inbox: %w", err)
	}
	recipientResolver, err := reference.NewRecipientResolver(recipients)
	if err != nil {
		return nil, nil, fmt.Errorf("message-delivery: резолвер получателя: %w", err)
	}
	bridge, err := approvedactionadapter.New(approvedactionadapter.Config{
		Recipients: recipientResolver, Materials: materials, SenderDomain: senderDomain,
		DurableQueue: queue,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("message-delivery: durable bridge approved-action: %w", err)
	}
	return bridge, bridge, nil
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

// WithLogger задаёт логгер жизненного цикла публикации транспортного outbox и
// возвращает адаптер.
func (s *AMQPSink) WithLogger(logger observ.Logger) *AMQPSink {
	s.relay = s.relay.WithLogger(logger)
	return s
}

// WithMetrics задаёт приёмник метрик для счётчика отказов публикации.
func (s *AMQPSink) WithMetrics(metrics observ.Metrics) *AMQPSink {
	s.relay = s.relay.WithMetrics(metrics)
	return s
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
