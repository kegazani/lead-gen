package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"lead-gen/backend/contexts/lead-management/internal/app"
	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
)

// semanticRecallBody — тело запроса семантического recall: текст оффера и предел
// числа кандидатов. Доменного вердикта в запросе нет: близость — это кандидатство.
type semanticRecallBody struct {
	Text  string `json:"text"`
	Limit int    `json:"limit,omitempty"`
}

// recallCandidateBody — кандидат recall: идентификатор компании и оценка близости.
// Оценка — косинусная близость владельца хранилища, а не вердикт релевантности.
type recallCandidateBody struct {
	CompanyID string  `json:"company_id"`
	Score     float64 `json:"score"`
}

// semanticRecallResponse — ответ recall (200 OK). Пустой список — допустимый исход:
// recall может не найти кандидатов.
type semanticRecallResponse struct {
	Candidates []recallCandidateBody `json:"candidates"`
}

// handleSemanticRecall обслуживает POST /semantic-recall — доступ оператора к
// семантическому recall по тексту оффера. Возвращённые кандидаты упорядочены по
// убыванию близости и не несут вердикта релевантности: вердикт остаётся за оценкой
// (lead-relevance). Незамонтированный сценарий или несмонтированное хранилище
// честно отвечают недоступностью; кандидаты в этом случае не выдумываются.
func (s *Server) handleSemanticRecall(w http.ResponseWriter, r *http.Request) {
	if s.semanticRecall == nil {
		writeErrorBody(w, http.StatusServiceUnavailable, "семантический поиск: сценарий не подключён")
		return
	}
	var body semanticRecallBody
	if err := decodeLeadSearchBody(w, r, &body); err != nil {
		writeErrorBody(w, http.StatusBadRequest, err.Error())
		return
	}
	candidates, err := s.semanticRecall.Run(r.Context(), app.SemanticRecallInput{
		Text:  body.Text,
		Limit: body.Limit,
	})
	if err != nil {
		writeSemanticRecallError(w, err)
		return
	}
	items := make([]recallCandidateBody, 0, len(candidates))
	for _, candidate := range candidates {
		items = append(items, recallCandidateBody{CompanyID: candidate.CompanyID, Score: candidate.Score})
	}
	writeJSONBody(w, http.StatusOK, semanticRecallResponse{Candidates: items})
}

// writeSemanticRecallError переводит исход recall в код ответа: некорректный
// запрос (пустой текст) — 400, несмонтированное векторное хранилище — 503,
// прочие сбои — 500.
func writeSemanticRecallError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, app.ErrInvalidRecallRequest):
		writeErrorBody(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, app.ErrVectorUnavailable):
		writeErrorBody(w, http.StatusServiceUnavailable, err.Error())
	default:
		writeErrorBody(w, http.StatusInternalServerError, "внутренняя ошибка")
	}
}

// embedCompanyResponse — результат эмбеддинга компании: идентификатор, размерность
// вектора и watermark исходных данных. Сам вектор не отдаётся: он не нужен экрану
// оператора, а его передача раздувала бы ответ.
type embedCompanyResponse struct {
	CompanyID     string `json:"company_id"`
	Dimensions    int    `json:"dimensions"`
	SourceVersion string `json:"source_version"`
}

// handleEmbedCompany обслуживает POST /cards/{id}/embed — доступ оператора к
// формированию (или пересчёту) эмбеддинга компании по контракту vector. Параметр
// ?force=1 заставляет пересчитать вектор, даже если watermark не менялся.
// Незамонтированный сценарий или хранилище честно отвечают недоступностью.
func (s *Server) handleEmbedCompany(w http.ResponseWriter, r *http.Request) {
	if s.embedCompany == nil {
		writeErrorBody(w, http.StatusServiceUnavailable, "эмбеддинг компании: сценарий не подключён")
		return
	}
	companyID := r.PathValue("id")
	embedding, err := s.embedCompany.Run(r.Context(), app.EmbedCompanyInput{
		CompanyID: domain.CompanyID(companyID),
		Force:     isForce(r),
	})
	if err != nil {
		writeEmbedCompanyError(w, err)
		return
	}
	writeJSONBody(w, http.StatusOK, embedCompanyResponse{
		CompanyID:     companyID,
		Dimensions:    len(embedding.Vector()),
		SourceVersion: embedding.SourceVersion(),
	})
}

// writeEmbedCompanyError переводит исход эмбеддинга в код ответа: отсутствующая
// карточка — 404, несмонтированное хранилище — 503, прочие сбои — 500.
func writeEmbedCompanyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ports.ErrProfileNotFound):
		writeErrorBody(w, http.StatusNotFound, "компания не найдена")
	case errors.Is(err, app.ErrVectorUnavailable):
		writeErrorBody(w, http.StatusServiceUnavailable, err.Error())
	default:
		writeErrorBody(w, http.StatusInternalServerError, "внутренняя ошибка")
	}
}

// isForce сообщает, запрошен ли принудительный пересчёт (?force=1).
func isForce(r *http.Request) bool {
	value, err := strconv.ParseBool(r.URL.Query().Get("force"))
	return err == nil && value
}
