package httpapi

import (
	"net/http"
	"strings"
	"time"

	"lead-gen/backend/contexts/content-production/internal/app/command"
	"lead-gen/backend/contexts/content-production/internal/domain"
)

// HTTP-обработчики генерации текстов лендинга, сбора сведений с сайта и проверки
// на заимствование (capability landing-page-generator). Адаптер только разбирает
// запрос и переводит доменную ошибку в код ответа.

// landingTextsBody — тело запроса генерации текстов блоков.
type landingTextsBody struct {
	SiteURL             string   `json:"site_url,omitempty"`
	BusinessDescription string   `json:"business_description"`
	ProductInfo         string   `json:"product_info,omitempty"`
	Documents           []string `json:"documents,omitempty"`
	SiteText            string   `json:"site_text,omitempty"`

	Tone     string `json:"tone,omitempty"`
	Language string `json:"language,omitempty"`

	Approved   bool   `json:"approved"`
	ApprovedBy string `json:"approved_by,omitempty"`
	ApprovedAt string `json:"approved_at,omitempty"`
}

// handleGenerateLandingTexts обслуживает POST /content/landing-texts: порождает
// тексты блоков через capability linguistics. Структура должна быть утверждена.
func (s *Server) handleGenerateLandingTexts(w http.ResponseWriter, r *http.Request) {
	var body landingTextsBody
	if err := decodeJSON(w, r, &body); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	var approvedAt time.Time
	if raw := strings.TrimSpace(body.ApprovedAt); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeErrorMessage(w, http.StatusBadRequest, "approved_at: ожидается время в формате RFC3339")
			return
		}
		approvedAt = parsed
	}

	result, err := s.deps.GenerateLandingTexts.Handle(r.Context(), command.GenerateLandingTexts{
		SiteURL:             body.SiteURL,
		BusinessDescription: body.BusinessDescription,
		ProductInfo:         body.ProductInfo,
		Documents:           body.Documents,
		SiteText:            body.SiteText,
		Tone:                body.Tone,
		Language:            body.Language,
		Approved:            body.Approved,
		ApprovedBy:          domain.ActorID(body.ApprovedBy),
		ApprovedAt:          approvedAt,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// collectSiteBody — тело запроса сбора сведений с сайта.
type collectSiteBody struct {
	StartURL       string `json:"start_url"`
	MaxPages       int    `json:"max_pages,omitempty"`
	MaxDepth       int    `json:"max_depth,omitempty"`
	MaxTimeSeconds int    `json:"max_time_seconds,omitempty"`
}

// handleCollectSite обслуживает POST /content/landing-collect: собирает сведения с
// сайта клиента в заданных границах и очищает их от сведений о физических лицах.
func (s *Server) handleCollectSite(w http.ResponseWriter, r *http.Request) {
	var body collectSiteBody
	if err := decodeJSON(w, r, &body); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := s.deps.CollectSite.Handle(r.Context(), command.CollectSite{
		StartURL:       body.StartURL,
		MaxPages:       body.MaxPages,
		MaxDepth:       body.MaxDepth,
		MaxTimeSeconds: body.MaxTimeSeconds,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// landingDocumentSourceBody — один предоставленный документ в теле запроса.
type landingDocumentSourceBody struct {
	Source string `json:"source"`
	Format string `json:"format,omitempty"`
}

// parseDocumentsBody — тело запроса разбора предоставленных документов.
type parseDocumentsBody struct {
	Sources []landingDocumentSourceBody `json:"sources"`
}

// handleParseDocuments обслуживает POST /content/landing-documents: принимает
// предоставленные документы (коммерческое предложение) и разбирает их движком
// сбора данных. Извлечённые тексты возвращаются в documents и передаются
// генерации наполнения как материалы.
func (s *Server) handleParseDocuments(w http.ResponseWriter, r *http.Request) {
	var body parseDocumentsBody
	if err := decodeJSON(w, r, &body); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	sources := make([]command.DocumentSource, 0, len(body.Sources))
	for _, source := range body.Sources {
		sources = append(sources, command.DocumentSource{Source: source.Source, Format: source.Format})
	}
	result, err := s.deps.ParseDocuments.Handle(r.Context(), command.ParseDocuments{Sources: sources})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// landingCheckBody — тело запроса проверки текста на заимствование.
type landingCheckBody struct {
	Text      string   `json:"text"`
	Sources   []string `json:"sources,omitempty"`
	Protected []string `json:"protected,omitempty"`
	Language  string   `json:"language,omitempty"`
	Tone      string   `json:"tone,omitempty"`
}

// handleCheckLandingTexts обслуживает POST /content/landing-check: находит
// дословные совпадения и переформулирует их через capability linguistics.
func (s *Server) handleCheckLandingTexts(w http.ResponseWriter, r *http.Request) {
	var body landingCheckBody
	if err := decodeJSON(w, r, &body); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := s.deps.CheckLandingTexts.Handle(r.Context(), command.CheckLandingTexts{
		Text:      body.Text,
		Sources:   body.Sources,
		Protected: body.Protected,
		Language:  body.Language,
		Tone:      body.Tone,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
