package contract

import (
	"context"
	"errors"

	"lead-gen/backend/contexts/sales-workflow/internal/app/command"
	"lead-gen/backend/contexts/sales-workflow/internal/domain"
	"lead-gen/backend/contexts/sales-workflow/internal/ports"
	"lead-gen/contracts/sales-workflow/crmevent"
)

// dealID/tenantID — короткие приведения строк контракта к доменным типам.
func dealID(raw string) domain.DealID     { return domain.DealID(raw) }
func tenantID(raw string) domain.TenantID { return domain.TenantID(raw) }

// CRMEvent — адаптер приёма контракта crm-event поверх прикладного сценария
// приёма события CRM (для co-location и тестов).
type CRMEvent struct {
	ingest *command.IngestCRMEventHandler
}

// CRMEvent реализует порт контракта.
var _ crmevent.Handler = (*CRMEvent)(nil)

// NewCRMEvent собирает адаптер из сценария приёма события CRM.
func NewCRMEvent(ingest *command.IngestCRMEventHandler) *CRMEvent {
	return &CRMEvent{ingest: ingest}
}

// Ingest реализует crmevent.Handler. Доменные нарушения формы события дают
// ErrRejected, недоступность внешней CRM и прочие сбои — ErrUnavailable.
func (a *CRMEvent) Ingest(ctx context.Context, event crmevent.CRMEvent) (crmevent.IngestResult, error) {
	if err := event.Validate(); err != nil {
		return crmevent.IngestResult{}, crmevent.ErrRejected
	}
	result, err := a.ingest.Handle(ctx, command.IngestCRMEvent{
		DealID:           dealID(event.DealID),
		TenantID:         tenantID(event.TenantID),
		Actor:            domain.ActorID(event.Source),
		ExternalRef:      domain.ExternalDealRef(event.ExternalRef),
		ExternalRevision: event.ExternalRevision,
		Stage:            domain.DealStage(event.Stage),
		ResultConfirmed:  event.ResultConfirmed,
		ActionRef:        domain.ActionRef(event.ActionRef),
		EvidenceRef:      domain.EvidenceRef(event.EvidenceRef),
		At:               event.OccurredAt,
		CorrelationID:    domain.CorrelationID(event.CorrelationID),
		CausationID:      domain.CausationID(event.CausationID),
	})
	if err != nil {
		return crmevent.IngestResult{}, contractError(err)
	}
	return crmevent.IngestResult{
		Applied:    result.Applied,
		SyncStatus: string(result.SyncStatus),
		Stage:      string(result.Stage),
		Version:    result.Version,
	}, nil
}

// contractError переводит ошибку сценария в исход контракта.
func contractError(err error) error {
	switch {
	case errors.Is(err, domain.ErrEmptyID),
		errors.Is(err, domain.ErrMissingActor),
		errors.Is(err, domain.ErrMissingTime),
		errors.Is(err, domain.ErrMissingEventID),
		errors.Is(err, domain.ErrUnknownDealStage),
		errors.Is(err, domain.ErrInvalidStageTransition),
		errors.Is(err, domain.ErrTerminalDeal),
		errors.Is(err, domain.ErrInvalidExternalVersion),
		errors.Is(err, domain.ErrInsufficientProof),
		errors.Is(err, domain.ErrAlreadyAttributed),
		errors.Is(err, domain.ErrAttributionNotAllowed),
		errors.Is(err, ports.ErrDealNotFound),
		errors.Is(err, command.ErrTenantMismatch):
		return crmevent.ErrRejected
	default:
		return crmevent.ErrUnavailable
	}
}
