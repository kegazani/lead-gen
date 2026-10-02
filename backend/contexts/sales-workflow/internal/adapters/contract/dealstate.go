// Package contract реализует порты опубликованных контрактов контекста
// sales-workflow поверх его прикладных сценариев.
//
// Адаптеры переводят доменные и прикладные исходы в исходы контрактов, не
// раскрывая внутренние типы: потребитель (например, request-fulfillment,
// получающий deal-state по customer-supplier) держит только опубликованный
// контракт.
package contract

import (
	"context"
	"errors"

	"lead-gen/backend/contexts/sales-workflow/internal/app/query"
	"lead-gen/backend/contexts/sales-workflow/internal/ports"
	"lead-gen/contracts/sales-workflow/dealstate"
)

// DealState — адаптер синхронного контракта deal-state поверх сценария чтения
// сделки.
type DealState struct {
	get *query.GetDealHandler
}

// DealState реализует порт контракта.
var _ dealstate.Port = (*DealState)(nil)

// NewDealState собирает адаптер из сценария чтения сделки.
func NewDealState(get *query.GetDealHandler) *DealState {
	return &DealState{get: get}
}

// Get реализует dealstate.Port. «Не найдено» и чужой тенант неотличимы и дают
// ErrNotFound; недоступность внешней CRM и прочие сбои — ErrUnavailable.
func (a *DealState) Get(ctx context.Context, q dealstate.Query) (dealstate.DealState, error) {
	view, err := a.get.Handle(ctx, query.GetDeal{
		DealID:   dealID(q.DealID),
		TenantID: tenantID(q.TenantID),
	})
	if err != nil {
		switch {
		case errors.Is(err, ports.ErrDealNotFound), errors.Is(err, query.ErrTenantMismatch):
			return dealstate.DealState{}, dealstate.ErrNotFound
		default:
			return dealstate.DealState{}, dealstate.ErrUnavailable
		}
	}
	return dealstate.DealState{
		DealID:           string(view.DealID),
		CompanyRef:       string(view.CompanyRef),
		Stage:            string(view.Stage),
		ExternalRef:      string(view.ExternalRef),
		ExternalRevision: view.ExternalRevision,
		ResultConfirmed:  view.ResultConfirmed,
		Version:          view.Version,
	}, nil
}
