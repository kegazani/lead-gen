package httpapi

import (
	"net/http"

	"lead-gen/backend/contexts/content-production/internal/app/command"
	"lead-gen/backend/contexts/content-production/internal/app/query"
	"lead-gen/backend/contexts/content-production/internal/domain"
)

// createPackageBody — тело команды создания контент-пакета.
type createPackageBody struct {
	AssetID   string `json:"asset_id"`
	TenantID  string `json:"tenant_id"`
	VersionID string `json:"version_id"`
	Language  string `json:"language"`
	Region    string `json:"region,omitempty"`
	Body      string `json:"body"`
	Actor     string `json:"actor"`

	CorrelationID string `json:"correlation_id,omitempty"`
	CausationID   string `json:"causation_id,omitempty"`
}

// publishVersionBody — тело команды публикации версии.
type publishVersionBody struct {
	TenantID  string `json:"tenant_id"`
	VersionID string `json:"version_id"`
	Body      string `json:"body"`
	Actor     string `json:"actor"`

	CorrelationID string `json:"correlation_id,omitempty"`
	CausationID   string `json:"causation_id,omitempty"`
}

// requestRenderBody — тело команды постановки задания рендеринга.
type requestRenderBody struct {
	RenderJobID string `json:"render_job_id"`
	VersionID   string `json:"version_id"`
	TenantID    string `json:"tenant_id"`
	TemplateID  string `json:"template_id"`
	Format      string `json:"format"`
}

// produceBody — тело команды производства контента по контракту content-request.
type produceBody struct {
	RequestID  string `json:"request_id"`
	AssetID    string `json:"asset_id"`
	TenantID   string `json:"tenant_id"`
	Language   string `json:"language"`
	Region     string `json:"region,omitempty"`
	Format     string `json:"format"`
	TemplateID string `json:"template_id"`
	Body       string `json:"body,omitempty"`
	Actor      string `json:"actor"`

	CorrelationID string `json:"correlation_id,omitempty"`
	CausationID   string `json:"causation_id,omitempty"`
}

// packageResponse — единая форма ответа о версии пакета.
type packageResponse struct {
	AssetID   domain.ContentAssetID   `json:"asset_id"`
	VersionID domain.ContentVersionID `json:"version_id"`
	Revision  int                     `json:"revision"`
	Version   int                     `json:"version"`
}

// requestRenderResponse — ответ постановки задания рендеринга.
type requestRenderResponse struct {
	RenderJobID domain.RenderJobID `json:"render_job_id"`
	State       domain.RenderState `json:"state"`
	Version     int                `json:"version"`
}

// produceResponse — ответ производства контента.
type produceResponse struct {
	AssetID     domain.ContentAssetID   `json:"asset_id"`
	VersionID   domain.ContentVersionID `json:"version_id"`
	RenderJobID domain.RenderJobID      `json:"render_job_id"`
	State       domain.RenderState      `json:"state"`
}

// handleCreatePackage обслуживает POST /content/packages.
func (s *Server) handleCreatePackage(w http.ResponseWriter, r *http.Request) {
	var body createPackageBody
	if err := decodeJSON(w, r, &body); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := s.deps.CreateContentPackage.Handle(r.Context(), command.CreateContentPackage{
		AssetID:       domain.ContentAssetID(body.AssetID),
		TenantID:      domain.TenantID(body.TenantID),
		VersionID:     domain.ContentVersionID(body.VersionID),
		Language:      body.Language,
		Region:        body.Region,
		Body:          body.Body,
		Actor:         domain.ActorID(body.Actor),
		CorrelationID: domain.CorrelationID(body.CorrelationID),
		CausationID:   domain.CausationID(body.CausationID),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, packageResponse{
		AssetID:   result.AssetID,
		VersionID: result.VersionID,
		Revision:  result.Revision,
		Version:   result.Version,
	})
}

// handlePublishVersion обслуживает POST /content/packages/{assetID}/versions.
func (s *Server) handlePublishVersion(w http.ResponseWriter, r *http.Request) {
	var body publishVersionBody
	if err := decodeJSON(w, r, &body); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := s.deps.PublishContentVersion.Handle(r.Context(), command.PublishContentVersion{
		AssetID:       domain.ContentAssetID(r.PathValue("assetID")),
		TenantID:      domain.TenantID(body.TenantID),
		VersionID:     domain.ContentVersionID(body.VersionID),
		Body:          body.Body,
		Actor:         domain.ActorID(body.Actor),
		CorrelationID: domain.CorrelationID(body.CorrelationID),
		CausationID:   domain.CausationID(body.CausationID),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, packageResponse{
		AssetID:   result.AssetID,
		VersionID: result.VersionID,
		Revision:  result.Revision,
		Version:   result.Version,
	})
}

// handleGetContent обслуживает GET /content/packages/{assetID}.
func (s *Server) handleGetContent(w http.ResponseWriter, r *http.Request) {
	view, err := s.deps.GetContent.Handle(r.Context(), query.GetContent{
		AssetID:  domain.ContentAssetID(r.PathValue("assetID")),
		TenantID: domain.TenantID(r.URL.Query().Get("tenant_id")),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// handleRequestRender обслуживает POST /content/packages/{assetID}/render-jobs.
func (s *Server) handleRequestRender(w http.ResponseWriter, r *http.Request) {
	var body requestRenderBody
	if err := decodeJSON(w, r, &body); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	format, err := domain.ParseRenderFormat(body.Format)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	result, err := s.deps.RequestRender.Handle(r.Context(), command.RequestRender{
		RenderJobID: domain.RenderJobID(body.RenderJobID),
		AssetID:     domain.ContentAssetID(r.PathValue("assetID")),
		VersionID:   domain.ContentVersionID(body.VersionID),
		TenantID:    domain.TenantID(body.TenantID),
		TemplateID:  domain.TemplateID(body.TemplateID),
		Format:      format,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, requestRenderResponse{
		RenderJobID: result.RenderJobID,
		State:       result.State,
		Version:     result.Version,
	})
}

// handleProduce обслуживает POST /content/produce.
func (s *Server) handleProduce(w http.ResponseWriter, r *http.Request) {
	var body produceBody
	if err := decodeJSON(w, r, &body); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	format, err := domain.ParseRenderFormat(body.Format)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	result, err := s.deps.ProduceContent.Handle(r.Context(), command.ProduceContent{
		RequestID:     body.RequestID,
		AssetID:       domain.ContentAssetID(body.AssetID),
		TenantID:      domain.TenantID(body.TenantID),
		Language:      body.Language,
		Region:        body.Region,
		Format:        format,
		TemplateID:    domain.TemplateID(body.TemplateID),
		Body:          body.Body,
		Actor:         domain.ActorID(body.Actor),
		CorrelationID: domain.CorrelationID(body.CorrelationID),
		CausationID:   domain.CausationID(body.CausationID),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, produceResponse{
		AssetID:     result.AssetID,
		VersionID:   result.VersionID,
		RenderJobID: result.RenderJobID,
		State:       result.State,
	})
}

// handleGetRenderJob обслуживает GET /content/render-jobs/{renderJobID}.
func (s *Server) handleGetRenderJob(w http.ResponseWriter, r *http.Request) {
	view, err := s.deps.GetRenderJob.Handle(r.Context(), query.GetRenderJob{
		RenderJobID: domain.RenderJobID(r.PathValue("renderJobID")),
		TenantID:    domain.TenantID(r.URL.Query().Get("tenant_id")),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}
