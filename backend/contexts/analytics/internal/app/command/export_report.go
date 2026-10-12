package command

import (
	"context"
	"errors"
	"fmt"
	"time"

	"lead-gen/backend/contexts/analytics/internal/app/query"
	"lead-gen/backend/contexts/analytics/internal/ports"
	"lead-gen/backend/platform/jobs"
	"lead-gen/backend/platform/observability/observ"
)

// ExportReport — use case длительной выгрузки отчёта. Постановка (Enqueue) только
// помещает запрос в очередь и сразу возвращает идентификатор; сборка файла
// выполняется фоновым обработчиком (Handle) под управлением jobs.Runner роли
// worker. Обработчик реально формирует файл и сохраняет его в хранилище, откуда
// его отдаёт ссылка на результат.
type ExportReport struct {
	exports ports.ReportExportStore
	source  query.ReportSource
	clock   ports.Clock

	logger  observ.Logger
	metrics observ.Metrics
	tracer  observ.Tracer
	service string
}

// NewExportReport собирает use case выгрузки отчёта.
func NewExportReport(
	exports ports.ReportExportStore,
	source query.ReportSource,
	clock ports.Clock,
	logger observ.Logger,
	metrics observ.Metrics,
	tracer observ.Tracer,
	service string,
) *ExportReport {
	return &ExportReport{
		exports: exports,
		source:  source,
		clock:   clock,
		logger:  logger,
		metrics: metrics,
		tracer:  tracer,
		service: service,
	}
}

// Enqueue ставит задание выгрузки и возвращает его операционную запись. Запрос с
// неизвестным форматом отклоняется до постановки: незачем занимать очередь
// заданием, которое заведомо не соберётся.
func (h *ExportReport) Enqueue(ctx context.Context, req ports.ReportExportRequest) (ports.ReportExport, error) {
	if err := req.Validate(); err != nil {
		return ports.ReportExport{}, err
	}
	return h.exports.CreateReportExport(ctx, req)
}

// Handle разбирает задание, собирает файл выгрузки и сохраняет его. Любой отказ
// возвращается нормализованной ошибкой jobs.CodedError: оператор видит
// бизнес-формулировку, а техническая деталь уходит только в лог.
func (h *ExportReport) Handle(ctx context.Context, job jobs.Job) error {
	fields := observ.Fields{TaskID: job.ID}
	ctx = observ.WithFields(ctx, fields)

	var finishTracer func(error)
	if h.tracer != nil {
		ctx, finishTracer = h.tracer.Start(ctx, "job.export")
	}

	start := time.Now()
	err := h.handle(ctx, job)
	duration := time.Since(start)

	if finishTracer != nil {
		finishTracer(err)
	}

	switch {
	case errors.Is(err, jobs.ErrCancelled):
		if h.logger != nil {
			h.logger.Info("задание выгрузки отменено", fields)
		}
		h.observe("cancelled", duration)
		return err
	case err != nil:
		if h.logger != nil {
			h.logger.Error("задание выгрузки упало", err, fields)
		}
		h.observe("failed", duration)
		return err
	default:
		if h.logger != nil {
			h.logger.Info("задание выгрузки готово", fields)
		}
		h.observe("done", duration)
		return nil
	}
}

// handle исполняет тело задания: разбор payload, сборка среза отчёта, сериализация
// файла и запись результата. По ходу сообщается прогресс (доля и текущий шаг) —
// экран консоли показывает его опросом, пока задание выполняется.
func (h *ExportReport) handle(ctx context.Context, job jobs.Job) error {
	h.report(ctx, job.ID, 10, exportStepParse)
	payload, err := ports.DecodeReportExportPayload(job.Payload)
	if err != nil {
		return newExportJobError(ErrorCodePayloadInvalid, fmt.Errorf("app: разбор payload задания %s: %w", job.ID, err))
	}
	req := payload.Request()
	if err := req.Validate(); err != nil {
		return newExportJobError(ErrorCodePayloadInvalid, fmt.Errorf("app: задание %s: %w", job.ID, err))
	}
	if err := ctx.Err(); err != nil {
		return jobs.ErrCancelled
	}

	h.report(ctx, job.ID, 40, exportStepSlice)
	view := h.reportView(ctx, req)
	if !view.Available {
		return newExportJobError(ErrorCodeReportUnavailable, fmt.Errorf("app: контур отчёта недоступен для задания %s", job.ID))
	}

	h.report(ctx, job.ID, 75, exportStepRender)
	result, err := renderReportExport(req.Format, view, h.now())
	if err != nil {
		return newExportJobError(ErrorCodeRenderFailed, fmt.Errorf("app: сборка файла задания %s: %w", job.ID, err))
	}
	if err := ctx.Err(); err != nil {
		return jobs.ErrCancelled
	}

	h.report(ctx, job.ID, 90, exportStepStore)
	// Результат записывается в отсоединённом контексте: уже собранный файл не
	// теряется из-за отмены на финальном шаге. Хранилище при этом может ответить
	// отказом — тогда задание честно завершается ошибкой.
	if err := h.exports.RecordExportResult(context.WithoutCancel(ctx), job.ID, result); err != nil {
		return newExportJobError(ErrorCodeResultStoreFailed, fmt.Errorf("app: сохранение результата задания %s: %w", job.ID, err))
	}
	return nil
}

// Шаги выполнения задания в формулировке для оператора. Показываются на экране
// прогресса; польза в том, что оператор видит, на каком этапе задание задержалось.
const (
	exportStepParse  = "разбор задания"
	exportStepSlice  = "сбор среза"
	exportStepRender = "сборка файла"
	exportStepStore  = "сохранение результата"
)

// report сообщает о ходе задания. Прогресс вспомогателен: отказ сообщения о ходе
// не срывает саму выгрузку, поэтому ошибка не возвращается.
func (h *ExportReport) report(ctx context.Context, id string, progress int, step string) {
	_ = jobs.ReportProgress(ctx, h.exports, id, progress, step)
}

// reportView собирает срез отчёта по зафиксированному в задании фильтру. Отказ
// источника или отсутствие видимых данных ослабляют срез: Available == false
// означает, что выгрузка не собрана и задание завершается нормализованным
// отказом.
func (h *ExportReport) reportView(ctx context.Context, req ports.ReportExportRequest) query.ReportView {
	handler := query.NewReportHandler(h.source, h.clock)
	return handler.Handle(ctx, query.ReportQuery{
		TenantID:   req.TenantID,
		Role:       req.Role,
		Period:     query.ReportPeriod{From: req.From, To: req.To},
		Segment:    req.Segment,
		Source:     req.Source,
		CampaignID: req.CampaignID,
	})
}

// renderReportExport сериализует срез в файл выбранного формата.
func renderReportExport(format ports.ExportFormat, view query.ReportView, at time.Time) (ports.ReportExportResult, error) {
	switch format {
	case ports.ExportCSV:
		body, err := query.RenderCSV(view)
		if err != nil {
			return ports.ReportExportResult{}, err
		}
		return ports.ReportExportResult{
			ContentType: query.CSVContentType,
			Filename:    query.ExportFilename("csv", at),
			Body:        body,
		}, nil
	case ports.ExportXLSX:
		body, err := query.RenderXLSX(view)
		if err != nil {
			return ports.ReportExportResult{}, err
		}
		return ports.ReportExportResult{
			ContentType: query.XLSXContentType,
			Filename:    query.ExportFilename("xlsx", at),
			Body:        body,
		}, nil
	default:
		return ports.ReportExportResult{}, ports.ErrInvalidExportFormat
	}
}

// now возвращает время сборки файла (для имени файла).
func (h *ExportReport) now() time.Time {
	if h.clock == nil {
		return time.Now()
	}
	return h.clock.Now()
}

// observe фиксирует исход задания в метриках сервиса.
func (h *ExportReport) observe(outcome string, duration time.Duration) {
	if h.metrics == nil {
		return
	}
	h.metrics.Incr("jobs_total", map[string]string{"service": h.service, "outcome": outcome})
	h.metrics.Observe("jobs_duration_seconds", duration.Seconds(), map[string]string{"service": h.service})
}
