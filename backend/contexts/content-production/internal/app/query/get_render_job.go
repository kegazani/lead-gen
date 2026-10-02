package query

import (
	"context"
	"fmt"

	"lead-gen/backend/contexts/content-production/internal/domain"
	"lead-gen/backend/contexts/content-production/internal/ports"
)

// GetRenderJob — запрос состояния задания рендеринга.
type GetRenderJob struct {
	RenderJobID domain.RenderJobID
	TenantID    domain.TenantID
}

// RenderJobView — представление задания рендеринга для чтения.
type RenderJobView struct {
	RenderJobID domain.RenderJobID
	TenantID    domain.TenantID
	AssetID     domain.ContentAssetID
	VersionID   domain.ContentVersionID
	TemplateID  domain.TemplateID
	Format      domain.RenderFormat
	State       domain.RenderState
	ArtifactRef string
	Reason      string
	Version     int
}

// GetRenderJobHandler — use case чтения задания рендеринга.
type GetRenderJobHandler struct {
	renderJobs ports.RenderJobRepository
}

// NewGetRenderJobHandler собирает use case чтения задания.
func NewGetRenderJobHandler(renderJobs ports.RenderJobRepository) *GetRenderJobHandler {
	return &GetRenderJobHandler{renderJobs: renderJobs}
}

// Handle загружает задание и строит представление. Задание другого тенанта
// неотличимо от отсутствующего.
func (h *GetRenderJobHandler) Handle(ctx context.Context, q GetRenderJob) (RenderJobView, error) {
	job, err := h.renderJobs.FindByID(ctx, q.RenderJobID)
	if err != nil {
		return RenderJobView{}, err
	}
	if job.TenantID() != q.TenantID {
		return RenderJobView{}, fmt.Errorf("%w: %s", ports.ErrRenderJobNotFound, q.RenderJobID)
	}
	return buildRenderJob(job), nil
}

// buildRenderJob переводит задание в представление чтения.
func buildRenderJob(job *domain.RenderJob) RenderJobView {
	view := RenderJobView{
		RenderJobID: job.ID(),
		TenantID:    job.TenantID(),
		AssetID:     job.AssetID(),
		VersionID:   job.VersionID(),
		TemplateID:  job.TemplateID(),
		Format:      job.Format(),
		State:       job.State(),
		Reason:      job.Reason(),
		Version:     job.Version(),
	}
	if artifact, ok := job.Artifact(); ok {
		view.ArtifactRef = artifact.Ref().String()
	}
	return view
}
