package query

import (
	"context"

	"lead-gen/backend/contexts/analytics/internal/ports"
)

// ExportJobHandler — чтение статуса и результата задания выгрузки отчёта. Тело
// файла отдаётся только по готовому результату: до завершения задания ссылка на
// результат не действует.
type ExportJobHandler struct {
	store ports.ReportExportStore
}

// NewExportJobHandler собирает use case чтения заданий выгрузки.
func NewExportJobHandler(store ports.ReportExportStore) *ExportJobHandler {
	return &ExportJobHandler{store: store}
}

// Status возвращает операционную запись задания. Отсутствующее задание —
// ports.ErrReportExportNotFound.
func (h *ExportJobHandler) Status(ctx context.Context, id string) (ports.ReportExport, error) {
	if h == nil || h.store == nil {
		return ports.ReportExport{}, ports.ErrReportExportNotFound
	}
	return h.store.ExportByID(ctx, id)
}

// Result возвращает готовый файл вместе с записью задания. Недовыполненное
// задание даёт ports.ErrReportExportNoResult, отсутствующее —
// ports.ErrReportExportNotFound.
func (h *ExportJobHandler) Result(ctx context.Context, id string) (ports.ReportExport, ports.ReportExportResult, error) {
	record, err := h.Status(ctx, id)
	if err != nil {
		return ports.ReportExport{}, ports.ReportExportResult{}, err
	}
	result, err := h.store.ExportResult(ctx, id)
	if err != nil {
		return record, ports.ReportExportResult{}, err
	}
	return record, result, nil
}
