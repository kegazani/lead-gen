package command

import (
	"context"
	"time"

	"lead-gen/backend/contexts/sales-workflow/internal/domain"
	"lead-gen/backend/contexts/sales-workflow/internal/ports"
)

// AttributeResult — команда подтверждения результата сделки и его атрибуции
// исходящему действию.
type AttributeResult struct {
	DealID   domain.DealID
	TenantID domain.TenantID
	Actor    domain.ActorID

	// ActionRef — исходящее действие, чей результат подтверждается.
	ActionRef domain.ActionRef

	// EvidenceRef — проверяемое основание связи результата с действием.
	EvidenceRef domain.EvidenceRef

	// ExternalRef — идемпотентная внешняя ссылка записи CRM.
	ExternalRef domain.ExternalDealRef

	Reason        string
	At            time.Time
	CorrelationID domain.CorrelationID
	CausationID   domain.CausationID
}

// AttributeResultHandler — use case подтверждения результата.
type AttributeResultHandler struct{ base }

// NewAttributeResultHandler собирает use case.
func NewAttributeResultHandler(
	deals ports.DealRepository,
	syncs ports.CRMSyncRepository,
	uow ports.UnitOfWork,
	clock ports.Clock,
	ids ports.IDGenerator,
) *AttributeResultHandler {
	return &AttributeResultHandler{base{deals: deals, syncs: syncs, uow: uow, clock: clock, ids: ids}}
}

// Handle подтверждает результат сделки в одной транзакции с записью события
// result-attributed. Повтор с той же внешней ссылкой идемпотентен: второго
// события не возникает.
func (h *AttributeResultHandler) Handle(ctx context.Context, cmd AttributeResult) (DealResult, error) {
	deal, err := h.loadDeal(ctx, cmd.DealID, cmd.TenantID)
	if err != nil {
		return DealResult{}, err
	}
	expected := deal.Version()
	in := h.actionInput(cmd.Actor, cmd.At, cmd.Reason, cmd.CorrelationID, cmd.CausationID)
	proof, err := domain.NewAttributionProof(cmd.ActionRef, cmd.EvidenceRef, cmd.ExternalRef, in.At)
	if err != nil {
		return DealResult{}, err
	}
	if _, err := deal.ConfirmAttribution(in, proof); err != nil {
		return DealResult{}, err
	}
	events, err := h.commit(ctx, deal, expected, nil)
	if err != nil {
		return DealResult{}, err
	}
	return DealResult{
		DealID:  deal.ID(),
		Stage:   deal.Stage(),
		Version: deal.Version(),
		Events:  events,
	}, nil
}
