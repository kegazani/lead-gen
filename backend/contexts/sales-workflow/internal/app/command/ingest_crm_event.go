package command

import (
	"context"
	"errors"
	"time"

	"lead-gen/backend/contexts/sales-workflow/internal/domain"
	"lead-gen/backend/contexts/sales-workflow/internal/ports"
)

// IngestCRMEvent — команда приёма события внешней CRM о сделке.
type IngestCRMEvent struct {
	DealID   domain.DealID
	TenantID domain.TenantID
	Actor    domain.ActorID

	// ExternalRef — идентификатор записи сделки во внешней CRM.
	ExternalRef domain.ExternalDealRef

	// ExternalRevision — ревизия записи во внешней CRM (>= 1).
	ExternalRevision int64

	// Stage — наблюдаемый внешний этап сделки.
	Stage domain.DealStage

	// ResultConfirmed — событие подтверждает результат сделки. Тогда обязательны
	// ActionRef и EvidenceRef.
	ResultConfirmed bool
	ActionRef       domain.ActionRef
	EvidenceRef     domain.EvidenceRef

	At            time.Time
	CorrelationID domain.CorrelationID
	CausationID   domain.CausationID
}

// IngestCRMEventResult — результат приёма события CRM.
type IngestCRMEventResult struct {
	// Applied — была ли внешняя версия применена к сделке. false означает, что
	// событие устарело или конфликтует и локальное состояние не тронуто.
	Applied bool

	SyncStatus domain.SyncStatus
	Stage      domain.DealStage
	Version    int
	Events     []domain.DomainEvent
}

// IngestCRMEventHandler — use case приёма события CRM.
type IngestCRMEventHandler struct{ base }

// NewIngestCRMEventHandler собирает use case.
func NewIngestCRMEventHandler(
	deals ports.DealRepository,
	syncs ports.CRMSyncRepository,
	uow ports.UnitOfWork,
	clock ports.Clock,
	ids ports.IDGenerator,
) *IngestCRMEventHandler {
	return &IngestCRMEventHandler{base{deals: deals, syncs: syncs, uow: uow, clock: clock, ids: ids}}
}

// Handle принимает событие внешней CRM.
//
// Сначала политика conflict-resolution решает судьбу внешней версии: конфликт
// фиксируется событием crm-sync-conflict и НЕ перезаписывает локальное состояние
// сделки; устаревшая версия не вносит изменений. Применимая версия обновляет этап
// сделки, а если событие подтверждает результат — политика confirmed-attribution
// атрибутирует его (событие result-attributed).
func (h *IngestCRMEventHandler) Handle(ctx context.Context, cmd IngestCRMEvent) (IngestCRMEventResult, error) {
	actor := cmd.Actor
	if actor == "" {
		actor = CRMActor
	}
	at := cmd.At
	if at.IsZero() && h.clock != nil {
		at = h.clock.Now()
	}
	external, err := domain.NewExternalVersion(cmd.ExternalRef, cmd.ExternalRevision)
	if err != nil {
		return IngestCRMEventResult{}, err
	}

	sync, err := h.syncs.FindByDeal(ctx, cmd.DealID)
	switch {
	case errors.Is(err, ports.ErrCRMSyncNotFound):
		sync, err = domain.NewCRMSync(domain.NewSyncParams{
			ID:       syncIDFor(cmd.DealID),
			TenantID: cmd.TenantID,
			DealID:   cmd.DealID,
			At:       at,
		})
		if err != nil {
			return IngestCRMEventResult{}, err
		}
	case err != nil:
		return IngestCRMEventResult{}, err
	}
	if sync.TenantID() != cmd.TenantID {
		return IngestCRMEventResult{}, ErrTenantMismatch
	}

	result, err := sync.RecordExternalVersion(
		domain.SyncInput{
			At:            at,
			EventID:       h.ids.NewEventID(),
			CorrelationID: cmd.CorrelationID,
			CausationID:   cmd.CausationID,
		},
		external,
	)
	if err != nil {
		return IngestCRMEventResult{}, err
	}

	if !result.Applied {
		events, err := h.commit(ctx, nil, 0, sync)
		if err != nil {
			return IngestCRMEventResult{}, err
		}
		return IngestCRMEventResult{Applied: false, SyncStatus: result.Status, Events: events}, nil
	}

	deal, err := h.loadDeal(ctx, cmd.DealID, cmd.TenantID)
	if err != nil {
		return IngestCRMEventResult{}, err
	}
	expected := deal.Version()
	if _, err := deal.ApplyExternal(
		h.actionInput(actor, at, "", cmd.CorrelationID, cmd.CausationID),
		external,
		cmd.Stage,
	); err != nil {
		return IngestCRMEventResult{}, err
	}
	if cmd.ResultConfirmed {
		attrIn := h.actionInput(actor, at, "", cmd.CorrelationID, cmd.CausationID)
		proof, err := domain.NewAttributionProof(cmd.ActionRef, cmd.EvidenceRef, cmd.ExternalRef, attrIn.At)
		if err != nil {
			return IngestCRMEventResult{}, err
		}
		if _, err := deal.ConfirmAttribution(attrIn, proof); err != nil {
			return IngestCRMEventResult{}, err
		}
	}

	events, err := h.commit(ctx, deal, expected, sync)
	if err != nil {
		return IngestCRMEventResult{}, err
	}
	return IngestCRMEventResult{
		Applied:    true,
		SyncStatus: result.Status,
		Stage:      deal.Stage(),
		Version:    deal.Version(),
		Events:     events,
	}, nil
}
