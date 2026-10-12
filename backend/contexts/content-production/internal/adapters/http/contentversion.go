package httpapi

import (
	"net/http"

	"lead-gen/backend/contexts/content-production/internal/app/query"
	"lead-gen/backend/contexts/content-production/internal/domain"
)

// HTTP-обработчик транспорта опубликованного контракта content-version.
// Маршрут — ровно POST /content-version (без префикса /content/): это wire-формат
// контракта, совпадающий с сетевым адаптером contentversion/httpport. Адаптер
// только разбирает запрос, вызывает сценарий чтения и переводит доменную ошибку
// в код ответа; содержимое версии не выдумывается.

// contentVersionBody — тело команды чтения версии контента (контракт
// content-version). Имена полей совпадают с wire-форматом сетевого адаптера
// contentversion/httpport. VersionID может быть пустым — тогда отдаётся последняя
// версия пакета.
type contentVersionBody struct {
	AssetID   string `json:"asset_id"`
	VersionID string `json:"version_id"`
	TenantID  string `json:"tenant_id"`
}

// contentVersionResponse — ответ чтения версии контента (200 OK). Имена полей
// совпадают с wire-форматом сетевого адаптера contentversion/httpport.
type contentVersionResponse struct {
	AssetID   string `json:"asset_id"`
	VersionID string `json:"version_id"`
	Revision  int    `json:"revision"`
	Language  string `json:"language"`
	Region    string `json:"region"`
	Subject   string `json:"subject"`
	Body      string `json:"body"`
}

// handleContentVersion обслуживает POST /content-version — транспорт контракта
// content-version. Незамонтированный сценарий чтения честно отвечает 503: контракт
// не подключён, а не «версии нет».
func (s *Server) handleContentVersion(w http.ResponseWriter, r *http.Request) {
	if s.deps.GetContent == nil {
		writeErrorMessage(w, http.StatusServiceUnavailable, "чтение контента: контракт content-version не подключён")
		return
	}
	var body contentVersionBody
	if err := decodeJSON(w, r, &body); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	view, err := s.deps.GetContent.Handle(r.Context(), query.GetContent{
		AssetID:  domain.ContentAssetID(body.AssetID),
		TenantID: domain.TenantID(body.TenantID),
	})
	if err != nil {
		// ErrContentPackageNotFound → 404 (в том числе пакет чужого тенанта:
		// «нет» и «не твоя» неотличимы); прочие сбои — 5xx честно.
		writeDomainError(w, err)
		return
	}
	chosen, ok := selectContentVersion(view, body.VersionID)
	if !ok {
		writeErrorMessage(w, http.StatusNotFound, "версия контента не найдена")
		return
	}
	subject := chosen.Subject
	if subject == "" {
		subject = contentVersionSubjectOf(view, chosen.VersionID)
	}
	writeJSON(w, http.StatusOK, contentVersionResponse{
		AssetID:   view.AssetID.String(),
		VersionID: chosen.VersionID.String(),
		Revision:  chosen.Revision,
		Language:  view.Language,
		Region:    view.Region,
		Subject:   subject,
		Body:      chosen.Body,
	})
}

// selectContentVersion выбирает версию по запросу: конкретную при непустом
// VersionID, иначе последнюю версию пакета. Повторяет логику сборки контракта
// content-version (contract.go) на стороне HTTP-транспорта, чтобы wire-ответ был
// идентичен прямому вызову; разнести общий хелпер нельзя без правки вне scope.
func selectContentVersion(view query.ContentView, versionID string) (query.VersionView, bool) {
	if versionID != "" {
		for _, version := range view.Versions {
			if version.VersionID.String() == versionID {
				return version, true
			}
		}
		return query.VersionView{}, false
	}
	for _, version := range view.Versions {
		if version.VersionID == view.LatestVersionID {
			return version, true
		}
	}
	if len(view.Versions) > 0 {
		return view.Versions[len(view.Versions)-1], true
	}
	return query.VersionView{}, false
}

// contentVersionSubjectOf возвращает тему материала письма — заголовок материала,
// прикреплённого к выбранной версии контента. Материала с заголовком у версии нет —
// пустая строка: тема не выдумывается (AGENTS.md §3). Повторяет логику
// contract.go на стороне HTTP-транспорта.
func contentVersionSubjectOf(view query.ContentView, versionID domain.ContentVersionID) string {
	if view.Document != nil && view.Document.VersionID == versionID {
		return view.Document.Title
	}
	if view.Landing != nil && view.Landing.VersionID == versionID {
		return view.Landing.Title
	}
	return ""
}
