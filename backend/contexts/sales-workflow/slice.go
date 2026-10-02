package salesworkflow

import (
	"time"

	"lead-gen/backend/contexts/sales-workflow/internal/adapters/contract"
	crmmemory "lead-gen/backend/contexts/sales-workflow/internal/adapters/crm/memory"
	httpapi "lead-gen/backend/contexts/sales-workflow/internal/adapters/http"
	"lead-gen/backend/contexts/sales-workflow/internal/adapters/idgen"
	ingestapi "lead-gen/backend/contexts/sales-workflow/internal/adapters/ingest"
	outboxinmemory "lead-gen/backend/contexts/sales-workflow/internal/adapters/outbox/inmemory"
	persistence "lead-gen/backend/contexts/sales-workflow/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/sales-workflow/internal/app/command"
	"lead-gen/backend/contexts/sales-workflow/internal/app/query"
	"lead-gen/backend/contexts/sales-workflow/internal/ports"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
)

// SystemClock — часы процесса, источник времени по умолчанию. Не адаптер внешней
// системы: используется, когда вызывающий не передал свой Clock.
type SystemClock struct{}

// Now реализует ports.Clock.
func (SystemClock) Now() time.Time { return time.Now() }

// Slice — собранный контекст среза: порты, адаптеры и use cases.
type Slice struct {
	// CRM — порт внешней CRM, использованный при сборке.
	CRM ports.DealRepository

	// Syncs — репозиторий синхронизации (локальное состояние).
	Syncs *persistence.Repository

	// Journal — журнал outbox событий.
	Journal platformoutbox.Journal

	// UnitOfWork — транзакция локального состояния (синхронизация + outbox).
	UnitOfWork *outboxinmemory.UnitOfWork

	// Relay не равен nil только если при сборке задан Publisher.
	Relay *platformoutbox.Relay

	// APIServer — HTTP-обработчики роли api. Реализует http.Handler.
	APIServer *httpapi.Server

	// IngestServer — HTTP-обработчики роли ingest (приём событий CRM). Реализует
	// http.Handler.
	IngestServer *ingestapi.Server

	CreateDeal      *command.CreateDealHandler
	ChangeDealStage *command.ChangeDealStageHandler
	AttributeResult *command.AttributeResultHandler
	IngestCRMEvent  *command.IngestCRMEventHandler
	GetDeal         *query.GetDealHandler
	CRMSyncStatus   *query.CRMSyncStatusHandler

	// DealStatePort — синхронный опубликованный контракт deal-state (для
	// потребителя deals-for-requests, например request-fulfillment).
	DealStatePort *contract.DealState

	// CRMEventHandler — приём опубликованного контракта crm-event для co-location
	// и тестов.
	CRMEventHandler *contract.CRMEvent
}

// Build собирает контекст среза из адаптеров и переданных зависимостей.
// Бизнес-правил здесь нет — соединение адаптеров, портов и use cases только своего
// контекста.
func Build(cfg Config) (*Slice, error) {
	crm := cfg.CRM
	if crm == nil {
		crm = crmmemory.New()
	}
	syncs := cfg.Syncs
	if syncs == nil {
		syncs = persistence.New()
	}
	journal := cfg.Journal
	if journal == nil {
		journal = platformoutbox.NewMemoryJournal()
	}
	clock := cfg.Clock
	if clock == nil {
		clock = SystemClock{}
	}
	ids := cfg.IDs
	if ids == nil {
		ids = idgen.NewRandom()
	}

	unitOfWork := outboxinmemory.NewUnitOfWorkWith(syncs, journal)

	createDeal := command.NewCreateDealHandler(crm, syncs, unitOfWork, clock, ids)
	changeStage := command.NewChangeDealStageHandler(crm, syncs, unitOfWork, clock, ids)
	attributeResult := command.NewAttributeResultHandler(crm, syncs, unitOfWork, clock, ids)
	ingestEvent := command.NewIngestCRMEventHandler(crm, syncs, unitOfWork, clock, ids)

	getDeal := query.NewGetDealHandler(crm)
	syncStatus := query.NewCRMSyncStatusHandler(syncs)

	apiServer := httpapi.NewServer(httpapi.Dependencies{
		CreateDeal:      createDeal,
		ChangeDealStage: changeStage,
		AttributeResult: attributeResult,
		GetDeal:         getDeal,
		CRMSyncStatus:   syncStatus,
	})
	ingestServer := ingestapi.NewServer(ingestapi.Dependencies{
		Ingest: ingestEvent,
	})

	var relay *platformoutbox.Relay
	if cfg.Publisher != nil {
		relay = platformoutbox.NewRelay(journal, cfg.Publisher, cfg.Interval)
	}

	return &Slice{
		CRM:        crm,
		Syncs:      syncs,
		Journal:    journal,
		UnitOfWork: unitOfWork,
		Relay:      relay,

		APIServer:    apiServer,
		IngestServer: ingestServer,

		CreateDeal:      createDeal,
		ChangeDealStage: changeStage,
		AttributeResult: attributeResult,
		IngestCRMEvent:  ingestEvent,
		GetDeal:         getDeal,
		CRMSyncStatus:   syncStatus,

		DealStatePort:   contract.NewDealState(getDeal),
		CRMEventHandler: contract.NewCRMEvent(ingestEvent),
	}, nil
}
