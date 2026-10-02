package command

import (
	"context"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

// RecordCall — команда фиксации ссылки на звонок-уточнение по идентификатору.
type RecordCall struct {
	RequestID domain.RequestID
	TenantID  domain.TenantID
	CallID    domain.CallID
}

// RecordCallHandler — use case фиксации ссылки на звонок.
type RecordCallHandler struct{ artifactHandler }

// NewRecordCallHandler собирает use case.
func NewRecordCallHandler(uow ports.UnitOfWork) *RecordCallHandler {
	return &RecordCallHandler{artifactHandler{uow: uow}}
}

// Handle фиксирует идентификатор звонка (не сам звонок): владение звонком
// остаётся у outreach-orchestration.
func (h *RecordCallHandler) Handle(ctx context.Context, cmd RecordCall) (TransitionResult, error) {
	return h.record(ctx, cmd.RequestID, cmd.TenantID, func(r *domain.Request) error {
		return r.AttachCall(cmd.CallID)
	})
}
