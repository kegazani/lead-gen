package command

import (
	"context"
	"fmt"

	"lead-gen/backend/contexts/content-production/internal/domain"
	"lead-gen/backend/contexts/content-production/internal/ports"
)

// RequestRender — команда постановки задания рендеринга точной версии контента по
// шаблону в заданный формат.
type RequestRender struct {
	RenderJobID domain.RenderJobID
	AssetID     domain.ContentAssetID
	VersionID   domain.ContentVersionID
	TenantID    domain.TenantID
	TemplateID  domain.TemplateID
	Format      domain.RenderFormat
}

// RequestRenderResult — результат постановки задания.
type RequestRenderResult struct {
	RenderJobID domain.RenderJobID
	State       domain.RenderState
	Version     int
}

// RequestRenderHandler — use case постановки задания рендеринга.
type RequestRenderHandler struct {
	uow ports.UnitOfWork
}

// NewRequestRenderHandler собирает use case из порта транзакции.
func NewRequestRenderHandler(uow ports.UnitOfWork) *RequestRenderHandler {
	return &RequestRenderHandler{uow: uow}
}

// Handle создаёт задание рендеринга в состоянии requested. Версия контента обязана
// существовать в пакете: рендер ссылается на точную версию и шаблон.
func (h *RequestRenderHandler) Handle(ctx context.Context, cmd RequestRender) (RequestRenderResult, error) {
	var result RequestRenderResult
	err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		pkg, err := tx.Packages().FindByID(ctx, cmd.AssetID)
		if err != nil {
			return err
		}
		if pkg.TenantID() != cmd.TenantID {
			return fmt.Errorf("%w: %s", ErrTenantMismatch, cmd.AssetID)
		}
		if _, ok := pkg.FindVersion(cmd.VersionID); !ok {
			return fmt.Errorf("%w: %s", ErrVersionRequired, cmd.VersionID)
		}
		job, err := domain.NewRenderJob(domain.NewRenderJobParams{
			ID:         cmd.RenderJobID,
			TenantID:   cmd.TenantID,
			AssetID:    cmd.AssetID,
			VersionID:  cmd.VersionID,
			TemplateID: cmd.TemplateID,
			Format:     cmd.Format,
		})
		if err != nil {
			return err
		}
		if err := tx.RenderJobs().Save(ctx, job, 0); err != nil {
			return err
		}
		result = RequestRenderResult{
			RenderJobID: job.ID(),
			State:       job.State(),
			Version:     job.Version(),
		}
		return nil
	})
	if err != nil {
		return RequestRenderResult{}, err
	}
	return result, nil
}
