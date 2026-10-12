package httpapi

import (
	"errors"
	"net/http"
	"time"

	"lead-gen/backend/contexts/lead-management/internal/app"
	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/contracts/lead-management/leadrelevance"
)

// recommendationApplyBody — тело команды применения рекомендации (контракт
// recommendation-apply). Имена полей совпадают с wire-форматом сетевого адаптера
// recommendationapply/httpport.
type recommendationApplyBody struct {
	TenantID  string                   `json:"tenant_id"`
	ChangeID  string                   `json:"change_id"`
	ScopeKind string                   `json:"scope_kind"`
	ScopeKey  string                   `json:"scope_key"`
	Reason    string                   `json:"reason"`
	Basis     string                   `json:"basis"`
	Change    recommendationChangeBody `json:"change"`
}

// recommendationChangeBody — структурное изменение контура поиска.
type recommendationChangeBody struct {
	Kind     string   `json:"kind"`
	Criteria string   `json:"criteria"`
	Targets  []string `json:"targets"`
	Limit    int      `json:"limit"`
}

// recommendationApplyResponse — ответ команды применения (200 OK).
type recommendationApplyResponse struct {
	JobID string `json:"job_id"`
}

// handleRecommendationApply обслуживает POST /recommendation-apply — транспорт
// опубликованного контракта recommendation-apply. Разбирает запрос, вызывает
// сценарий и отдаёт идентификатор созданного задания; доменных решений здесь нет.
// Незамонтированный сценарий честно отвечает 503.
func (s *Server) handleRecommendationApply(w http.ResponseWriter, r *http.Request) {
	if s.applyRecommendation == nil {
		writeErrorBody(w, http.StatusServiceUnavailable, "применение рекомендации: контракт recommendation-apply не подключён")
		return
	}
	var body recommendationApplyBody
	if err := decodeLeadSearchBody(w, r, &body); err != nil {
		writeErrorBody(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := s.applyRecommendation.Run(r.Context(), app.ApplyRecommendationInput{
		TenantID:  body.TenantID,
		ChangeID:  body.ChangeID,
		ScopeKind: body.ScopeKind,
		ScopeKey:  body.ScopeKey,
		Reason:    body.Reason,
		Basis:     body.Basis,
		Criteria:  body.Change.Criteria,
		Targets:   body.Change.Targets,
		Limit:     body.Change.Limit,
	})
	if err != nil {
		if errors.Is(err, app.ErrInvalidRecommendationChange) {
			writeErrorBody(w, http.StatusBadRequest, err.Error())
			return
		}
		writeErrorBody(w, http.StatusInternalServerError, "внутренняя ошибка")
		return
	}
	writeJSONBody(w, http.StatusOK, recommendationApplyResponse{JobID: result.JobID})
}

// leadRelevanceEvidenceBody — свидетельство оценки в ответе (контракт
// lead-relevance). Имена полей совпадают с wire-форматом leadrelevance/httpport.
type leadRelevanceEvidenceBody struct {
	Criterion  string `json:"criterion"`
	Fact       string `json:"fact"`
	SourceURL  string `json:"source_url"`
	Reasoning  string `json:"reasoning"`
	SignalKind string `json:"signal_kind"`
	Missing    string `json:"missing,omitempty"`
	Mismatch   string `json:"mismatch,omitempty"`
}

// leadRelevanceResponse — ответ чтения оценки (200 OK).
type leadRelevanceResponse struct {
	Contract      string                      `json:"contract"`
	SchemaVersion string                      `json:"schema_version"`
	JobID         string                      `json:"job_id"`
	CompanyID     string                      `json:"company_id"`
	Verdict       string                      `json:"verdict"`
	SignalKind    string                      `json:"signal_kind"`
	Evidence      []leadRelevanceEvidenceBody `json:"evidence"`
	AssessedAt    time.Time                   `json:"assessed_at"`
}

// handleLeadRelevance обслуживает GET /lead-relevance — транспорт
// опубликованного read-контракта lead-relevance. Разбирает пару (задание,
// компания), вызывает сценарий и отдаёт вердикт со свидетельствами; доменных
// решений здесь нет. Незамонтированный сценарий честно отвечает 503; отсутствие
// оценки — 404, а не выдуманный вердикт.
func (s *Server) handleLeadRelevance(w http.ResponseWriter, r *http.Request) {
	if s.leadRelevance == nil {
		writeErrorBody(w, http.StatusServiceUnavailable, "чтение оценки: контракт lead-relevance не подключён")
		return
	}
	result, err := s.leadRelevance.Run(r.Context(), app.ReadLeadRelevanceInput{
		JobID:     r.URL.Query().Get("job_id"),
		CompanyID: r.URL.Query().Get("company_id"),
	})
	if err != nil {
		writeLeadRelevanceError(w, err)
		return
	}
	evidence := make([]leadRelevanceEvidenceBody, 0, len(result.Evidence))
	for _, e := range result.Evidence {
		evidence = append(evidence, leadRelevanceEvidenceBody{
			Criterion:  e.Criterion,
			Fact:       e.Fact,
			SourceURL:  e.SourceURL,
			Reasoning:  e.Reasoning,
			SignalKind: e.SignalKind,
			Missing:    e.Missing,
			Mismatch:   e.Mismatch,
		})
	}
	writeJSONBody(w, http.StatusOK, leadRelevanceResponse{
		Contract:      leadrelevance.ContractName,
		SchemaVersion: leadrelevance.SchemaVersion,
		JobID:         result.JobID,
		CompanyID:     result.CompanyID,
		Verdict:       result.Verdict,
		SignalKind:    result.SignalKind,
		Evidence:      evidence,
		AssessedAt:    result.AssessedAt,
	})
}

// writeLeadRelevanceError переводит исход чтения оценки в код ответа:
// некорректный запрос — 400, отсутствие оценки — 404, прочие сбои — 500
// (недоступность владельца), а не выдуманный вердикт.
func writeLeadRelevanceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, app.ErrInvalidLeadRelevanceRequest):
		writeErrorBody(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ports.ErrAssessmentNotFound):
		writeErrorBody(w, http.StatusNotFound, "оценка не найдена")
	default:
		writeErrorBody(w, http.StatusInternalServerError, "внутренняя ошибка")
	}
}
