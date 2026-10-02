package query

import (
	"context"
	"time"

	"lead-gen/backend/contexts/sales-workflow/internal/domain"
	"lead-gen/backend/contexts/sales-workflow/internal/ports"
)

// CRMSyncStatus — запрос состояния синхронизации сделки.
type CRMSyncStatus struct {
	DealID   domain.DealID
	TenantID domain.TenantID
}

// CRMStatusView — представление состояния синхронизации.
type CRMStatusView struct {
	DealID           domain.DealID          `json:"deal_id"`
	TenantID         domain.TenantID        `json:"tenant_id"`
	Status           domain.SyncStatus      `json:"status"`
	ExternalRef      domain.ExternalDealRef `json:"external_ref"`
	ExternalRevision int64                  `json:"external_revision"`
	UpdatedAt        time.Time              `json:"updated_at"`
}

// CRMSyncStatusHandler — use case чтения синхронизации.
type CRMSyncStatusHandler struct {
	syncs ports.CRMSyncRepository
}

// NewCRMSyncStatusHandler собирает use case.
func NewCRMSyncStatusHandler(syncs ports.CRMSyncRepository) *CRMSyncStatusHandler {
	return &CRMSyncStatusHandler{syncs: syncs}
}

// Handle загружает запись синхронизации и собирает представление. Отсутствие
// записи — исход ErrCRMSyncNotFound порта, а не пустой успех.
func (h *CRMSyncStatusHandler) Handle(ctx context.Context, q CRMSyncStatus) (CRMStatusView, error) {
	sync, err := h.syncs.FindByDeal(ctx, q.DealID)
	if err != nil {
		return CRMStatusView{}, err
	}
	if sync.TenantID() != q.TenantID {
		return CRMStatusView{}, ErrTenantMismatch
	}
	return CRMStatusView{
		DealID:           sync.DealID(),
		TenantID:         sync.TenantID(),
		Status:           sync.Status(),
		ExternalRef:      sync.Version().Ref(),
		ExternalRevision: sync.Version().Revision(),
		UpdatedAt:        sync.UpdatedAt(),
	}, nil
}
