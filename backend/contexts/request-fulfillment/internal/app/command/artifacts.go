package command

import (
	"context"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

// RecordAudienceSnapshot — команда фиксации ссылки на снимок аудитории.
type RecordAudienceSnapshot struct {
	RequestID          domain.RequestID
	TenantID           domain.TenantID
	AudienceSnapshotID domain.AudienceSnapshotID
}

// RecordCampaign — команда фиксации ссылки на кампанию.
type RecordCampaign struct {
	RequestID  domain.RequestID
	TenantID   domain.TenantID
	CampaignID domain.CampaignID
}

// RecordDeal — команда фиксации ссылки на сделку.
type RecordDeal struct {
	RequestID domain.RequestID
	TenantID  domain.TenantID
	DealID    domain.DealID
}

// artifactHandler — общий каркас сценарного обработчика фиксации ссылки.
type artifactHandler struct {
	uow ports.UnitOfWork
}

// record выполняет фиксацию ссылки, не порождая событий: смена ссылки не меняет
// статус заявки, а факт изменения фиксируется версией агрегата.
func (h artifactHandler) record(
	ctx context.Context,
	requestID domain.RequestID,
	tenantID domain.TenantID,
	record func(request *domain.Request) error,
) (TransitionResult, error) {
	return applyChange(ctx, h.uow, commandKey{RequestID: requestID, TenantID: tenantID},
		func(request *domain.Request) ([]domain.DomainEvent, error) {
			return nil, record(request)
		})
}

// RecordAudienceSnapshotHandler — use case фиксации аудитории.
type RecordAudienceSnapshotHandler struct{ artifactHandler }

// NewRecordAudienceSnapshotHandler собирает use case.
func NewRecordAudienceSnapshotHandler(uow ports.UnitOfWork) *RecordAudienceSnapshotHandler {
	return &RecordAudienceSnapshotHandler{artifactHandler{uow: uow}}
}

// Handle фиксирует идентификатор снимка аудитории (не сам объект аудитории).
func (h *RecordAudienceSnapshotHandler) Handle(ctx context.Context, cmd RecordAudienceSnapshot) (TransitionResult, error) {
	return h.record(ctx, cmd.RequestID, cmd.TenantID, func(r *domain.Request) error {
		return r.RecordAudienceSnapshot(cmd.AudienceSnapshotID)
	})
}

// RecordCampaignHandler — use case фиксации кампании.
type RecordCampaignHandler struct{ artifactHandler }

// NewRecordCampaignHandler собирает use case.
func NewRecordCampaignHandler(uow ports.UnitOfWork) *RecordCampaignHandler {
	return &RecordCampaignHandler{artifactHandler{uow: uow}}
}

// Handle фиксирует идентификатор кампании.
func (h *RecordCampaignHandler) Handle(ctx context.Context, cmd RecordCampaign) (TransitionResult, error) {
	return h.record(ctx, cmd.RequestID, cmd.TenantID, func(r *domain.Request) error {
		return r.RecordCampaign(cmd.CampaignID)
	})
}

// RecordDealHandler — use case фиксации сделки.
type RecordDealHandler struct{ artifactHandler }

// NewRecordDealHandler собирает use case.
func NewRecordDealHandler(uow ports.UnitOfWork) *RecordDealHandler {
	return &RecordDealHandler{artifactHandler{uow: uow}}
}

// Handle фиксирует идентификатор сделки.
func (h *RecordDealHandler) Handle(ctx context.Context, cmd RecordDeal) (TransitionResult, error) {
	return h.record(ctx, cmd.RequestID, cmd.TenantID, func(r *domain.Request) error {
		return r.RecordDeal(cmd.DealID)
	})
}
