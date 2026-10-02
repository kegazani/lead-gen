package command

import (
	"context"
	"time"

	"lead-gen/backend/contexts/sales-workflow/internal/domain"
	"lead-gen/backend/contexts/sales-workflow/internal/ports"
)

// CreateDeal — команда создания сделки.
type CreateDeal struct {
	DealID   domain.DealID
	TenantID domain.TenantID

	// CompanyRef — ссылка на карточку компании (владение lead-management).
	CompanyRef domain.CompanyRef

	// ExternalRef — идентификатор записи сделки во внешней CRM.
	ExternalRef domain.ExternalDealRef

	// ExternalRevision — ревизия записи во внешней CRM (>= 1).
	ExternalRevision int64

	Actor         domain.ActorID
	At            time.Time
	CorrelationID domain.CorrelationID
	CausationID   domain.CausationID
}

// DealResult — результат команды изменения сделки.
type DealResult struct {
	DealID  domain.DealID
	Stage   domain.DealStage
	Version int
	Events  []domain.DomainEvent
}

// CreateDealHandler — use case создания сделки.
type CreateDealHandler struct{ base }

// NewCreateDealHandler собирает use case.
func NewCreateDealHandler(
	deals ports.DealRepository,
	syncs ports.CRMSyncRepository,
	uow ports.UnitOfWork,
	clock ports.Clock,
	ids ports.IDGenerator,
) *CreateDealHandler {
	return &CreateDealHandler{base{deals: deals, syncs: syncs, uow: uow, clock: clock, ids: ids}}
}

// Handle создаёт сделку во внешней CRM и запись её синхронизации, фиксируя
// событие deal-state-changed. Нулевая внешняя версия не принимается: создание
// сделки уже подразумевает запись в CRM.
func (h *CreateDealHandler) Handle(ctx context.Context, cmd CreateDeal) (DealResult, error) {
	in := h.actionInput(cmd.Actor, cmd.At, "", cmd.CorrelationID, cmd.CausationID)
	external, err := domain.NewExternalVersion(cmd.ExternalRef, cmd.ExternalRevision)
	if err != nil {
		return DealResult{}, err
	}
	deal, err := domain.NewDeal(domain.NewDealParams{
		ID:            cmd.DealID,
		TenantID:      cmd.TenantID,
		CompanyRef:    cmd.CompanyRef,
		External:      external,
		Actor:         in.Actor,
		At:            in.At,
		EventID:       in.EventID,
		CorrelationID: in.CorrelationID,
		CausationID:   in.CausationID,
	})
	if err != nil {
		return DealResult{}, err
	}
	sync, err := domain.NewCRMSync(domain.NewSyncParams{
		ID:       syncIDFor(cmd.DealID),
		TenantID: cmd.TenantID,
		DealID:   cmd.DealID,
		External: external,
		At:       in.At,
	})
	if err != nil {
		return DealResult{}, err
	}

	events, err := h.commit(ctx, deal, 0, sync)
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
