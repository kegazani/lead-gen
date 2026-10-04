package httpapi

import (
	"net/http"
	"strings"

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

// restoreVersionBody — тело команды возврата к предыдущей версии.
type restoreVersionBody struct {
	TenantID     string `json:"tenant_id"`
	NewVersionID string `json:"new_version_id"`
	Actor        string `json:"actor"`

	CorrelationID string `json:"correlation_id,omitempty"`
	CausationID   string `json:"causation_id,omitempty"`
}

// restoreVersionResponse — ответ возврата к версии.
type restoreVersionResponse struct {
	AssetID      domain.ContentAssetID   `json:"asset_id"`
	VersionID    domain.ContentVersionID `json:"version_id"`
	RestoredFrom domain.ContentVersionID `json:"restored_from"`
	Revision     int                     `json:"revision"`
	Version      int                     `json:"version"`
}

// handleRestoreVersion обслуживает
// POST /content/packages/{assetID}/versions/{versionID}/restore.
func (s *Server) handleRestoreVersion(w http.ResponseWriter, r *http.Request) {
	var body restoreVersionBody
	if err := decodeJSON(w, r, &body); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := s.deps.RestoreContentVersion.Handle(r.Context(), command.RestoreContentVersion{
		AssetID:         domain.ContentAssetID(r.PathValue("assetID")),
		TenantID:        domain.TenantID(body.TenantID),
		SourceVersionID: domain.ContentVersionID(r.PathValue("versionID")),
		NewVersionID:    domain.ContentVersionID(body.NewVersionID),
		Actor:           domain.ActorID(body.Actor),
		CorrelationID:   domain.CorrelationID(body.CorrelationID),
		CausationID:     domain.CausationID(body.CausationID),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, restoreVersionResponse{
		AssetID:      result.AssetID,
		VersionID:    result.VersionID,
		RestoredFrom: result.RestoredFrom,
		Revision:     result.Revision,
		Version:      result.Version,
	})
}

// landingPageResponse — ответ сборки страницы лендинга.
type landingPageResponse struct {
	Ref              domain.ArtifactRef `json:"ref"`
	Title            string             `json:"title"`
	Description      string             `json:"description,omitempty"`
	Language         string             `json:"language"`
	HTML             string             `json:"html"`
	StyleSheet       string             `json:"stylesheet"`
	ExcludedContacts []string           `json:"excluded_contacts,omitempty"`
}

// handleGetLandingPage обслуживает GET /content/packages/{assetID}/landing-page.
// Результат — собранная страница; при format=archive отдаётся архив размещения.
func (s *Server) handleGetLandingPage(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()

	brand, err := brandFromQuery(values)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	company, err := companyFromQuery(values)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	form, err := formFromQuery(values)
	if err != nil {
		writeDomainError(w, err)
		return
	}

	view, err := s.deps.GetLandingPage.Handle(r.Context(), query.GetLandingPage{
		AssetID:     domain.ContentAssetID(r.PathValue("assetID")),
		TenantID:    domain.TenantID(values.Get("tenant_id")),
		Description: values.Get("description"),
		Brand:       brand,
		Company:     company,
		Form:        form,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}

	if values.Get("format") == "archive" {
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="landing.zip"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(view.Archive)
		return
	}

	writeJSON(w, http.StatusOK, landingPageResponse{
		Ref:              view.Ref,
		Title:            view.Title,
		Description:      view.Description,
		Language:         view.Language,
		HTML:             view.HTML,
		StyleSheet:       view.StyleSheet,
		ExcludedContacts: view.ExcludedContacts,
	})
}

// defaultFormFields — минимальный состав полей формы по умолчанию: имя и почта.
var defaultFormFields = []domain.LeadFormField{domain.LeadFormFieldName, domain.LeadFormFieldEmail}

// formFromQuery собирает форму заявки из параметров form_action и fields.
// Без form_action форма не строится (блок заявки опускается).
func formFromQuery(values map[string][]string) (domain.LeadForm, error) {
	action := firstValue(values, "form_action")
	if strings.TrimSpace(action) == "" {
		return domain.LeadForm{}, nil
	}
	fields := defaultFormFields
	if raw := firstValue(values, "fields"); strings.TrimSpace(raw) != "" {
		parsed, err := parseFormFields(raw)
		if err != nil {
			return domain.LeadForm{}, err
		}
		fields = parsed
	}
	return domain.NewLeadForm(action, fields)
}

// parseFormFields разбирает список полей формы через запятую.
func parseFormFields(raw string) ([]domain.LeadFormField, error) {
	parts := strings.Split(raw, ",")
	fields := make([]domain.LeadFormField, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		fields = append(fields, domain.LeadFormField(trimmed))
	}
	return fields, nil
}

// brandFromQuery собирает бренд-комплект из параметров primary, accent, font, logo.
func brandFromQuery(values map[string][]string) (domain.BrandKit, error) {
	return domain.NewBrandKit(
		firstValue(values, "primary"),
		firstValue(values, "accent"),
		firstValue(values, "font"),
		firstValue(values, "logo"),
	)
}

// companyFromQuery собирает профиль компании из параметров company_* и контактов
// company_phone/company_email. Без наименования профиль нулевой: блок опускается.
func companyFromQuery(values map[string][]string) (domain.CompanyProfile, error) {
	name := firstValue(values, "company_name")
	if strings.TrimSpace(name) == "" {
		return domain.CompanyProfile{}, nil
	}
	var contacts []domain.CompanyContact
	if phone := firstValue(values, "company_phone"); strings.TrimSpace(phone) != "" {
		contact, err := domain.NewCompanyContact(domain.CompanyContactPhone, phone, false)
		if err != nil {
			return domain.CompanyProfile{}, err
		}
		contacts = append(contacts, contact)
	}
	if email := firstValue(values, "company_email"); strings.TrimSpace(email) != "" {
		contact, err := domain.NewCompanyContact(domain.CompanyContactEmail, email, false)
		if err != nil {
			return domain.CompanyProfile{}, err
		}
		contacts = append(contacts, contact)
	}
	return domain.NewCompanyProfile(name, firstValue(values, "company_address"), contacts)
}

// landingStructureBody — тело запроса построения структуры лендинга.
type landingStructureBody struct {
	SiteURL             string   `json:"site_url,omitempty"`
	BusinessDescription string   `json:"business_description"`
	ProductInfo         string   `json:"product_info,omitempty"`
	Documents           []string `json:"documents,omitempty"`
}

// handleProposeLandingStructure обслуживает POST /content/landing-structure:
// показывает перечень блоков по материалам клиента до генерации текстов.
func (s *Server) handleProposeLandingStructure(w http.ResponseWriter, r *http.Request) {
	var body landingStructureBody
	if err := decodeJSON(w, r, &body); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	view, err := s.deps.ProposeLandingStructure.Handle(r.Context(), query.ProposeLandingStructure{
		SiteURL:             body.SiteURL,
		BusinessDescription: body.BusinessDescription,
		ProductInfo:         body.ProductInfo,
		Documents:           body.Documents,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// firstValue возвращает первое значение параметра запроса.
func firstValue(values map[string][]string, key string) string {
	if list := values[key]; len(list) > 0 {
		return list[0]
	}
	return ""
}
