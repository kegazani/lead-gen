package inmemory_test

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/analytics/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/analytics/internal/ports"
	"lead-gen/backend/platform/jobs"
)

// TestReportExportStoreLifecycle проверяет хранилище заданий выгрузки: постановка
// даёт queued-задание без результата, взятие из очереди — running, после записи
// файла и завершения ссылка на результат отдаёт тело, а до записи — честный отказ.
func TestReportExportStoreLifecycle(t *testing.T) {
	ctx := context.Background()
	store := inmemory.NewReportExportStore()

	record, err := store.CreateReportExport(ctx, ports.ReportExportRequest{
		TenantID: "t1",
		Format:   ports.ExportCSV,
	})
	if err != nil {
		t.Fatalf("CreateReportExport: %v", err)
	}
	if record.ID == "" || record.Status != jobs.StatusQueued {
		t.Fatalf("постановка вернула %+v", record)
	}
	if record.ResultAvailable {
		t.Fatalf("у нового задания результата быть не должно: %+v", record)
	}

	// Результата ещё нет: ссылка на него не действует.
	if _, err := store.ExportResult(ctx, record.ID); !errors.Is(err, ports.ErrReportExportNoResult) {
		t.Fatalf("ExportResult до записи = %v, ожидалась ErrReportExportNoResult", err)
	}

	if _, err := store.TakeQueued(ctx); err != nil {
		t.Fatalf("TakeQueued: %v", err)
	}
	body := []byte("camp-1,2\n")
	if err := store.RecordExportResult(ctx, record.ID, ports.ReportExportResult{
		ContentType: "text/csv; charset=utf-8",
		Filename:    "report-20260110.csv",
		Body:        body,
	}); err != nil {
		t.Fatalf("RecordExportResult: %v", err)
	}
	if err := store.UpdateStatus(ctx, record.ID, jobs.StatusRunning, jobs.StatusDone, "", ""); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	got, err := store.ExportByID(ctx, record.ID)
	if err != nil {
		t.Fatalf("ExportByID: %v", err)
	}
	if got.Status != jobs.StatusDone || !got.ResultAvailable {
		t.Fatalf("завершённое задание = %+v", got)
	}
	result, err := store.ExportResult(ctx, record.ID)
	if err != nil {
		t.Fatalf("ExportResult: %v", err)
	}
	if string(result.Body) != string(body) || result.Filename != "report-20260110.csv" {
		t.Fatalf("файл результата = %+v", result)
	}
}

// TestReportExportStoreNotFound проверяет, что отсутствующее задание отличается от
// задания без результата.
func TestReportExportStoreNotFound(t *testing.T) {
	store := inmemory.NewReportExportStore()
	if _, err := store.ExportByID(context.Background(), "нет-такого"); !errors.Is(err, ports.ErrReportExportNotFound) {
		t.Fatalf("ExportByID отсутствующего = %v, ожидалась ErrReportExportNotFound", err)
	}
	if _, err := store.ExportResult(context.Background(), "нет-такого"); !errors.Is(err, ports.ErrReportExportNotFound) {
		t.Fatalf("ExportResult отсутствующего = %v, ожидалась ErrReportExportNotFound", err)
	}
}

// TestReportExportRequestValidation проверяет, что неизвестный формат отклоняется
// портом до постановки.
func TestReportExportRequestValidation(t *testing.T) {
	err := (ports.ReportExportRequest{Format: ports.ExportFormat("pdf")}).Validate()
	if !errors.Is(err, ports.ErrInvalidExportFormat) {
		t.Fatalf("Validate неизвестного формата = %v, ожидалась ErrInvalidExportFormat", err)
	}
}

// TestReportExportStoreProgress проверяет ход задания: доля выполнения и текущий шаг
// сообщаются только для running-задания, значение вне диапазона приводится к
// границам, а завершённое задание показывает 100 %.
func TestReportExportStoreProgress(t *testing.T) {
	ctx := context.Background()
	store := inmemory.NewReportExportStore()
	record, err := store.CreateReportExport(ctx, ports.ReportExportRequest{TenantID: "t1", Format: ports.ExportCSV})
	if err != nil {
		t.Fatalf("CreateReportExport: %v", err)
	}

	// Прогресс queued-задания отклоняется: задание ещё не выполняется.
	if err := store.UpdateProgress(ctx, record.ID, 10, "разбор"); !errors.Is(err, jobs.ErrTransitionRejected) {
		t.Fatalf("UpdateProgress queued = %v, ожидалась ErrTransitionRejected", err)
	}
	if _, err := store.TakeQueued(ctx); err != nil {
		t.Fatalf("TakeQueued: %v", err)
	}
	if err := store.UpdateProgress(ctx, record.ID, 150, "сбор среза"); err != nil {
		t.Fatalf("UpdateProgress: %v", err)
	}
	half, err := store.ExportByID(ctx, record.ID)
	if err != nil {
		t.Fatalf("ExportByID: %v", err)
	}
	if half.Progress != jobs.ProgressMax || half.Step != "сбор среза" {
		t.Fatalf("прогресс = %d, шаг %q, ожидалось 100/сбор среза", half.Progress, half.Step)
	}
	if half.StartedAt.IsZero() {
		t.Fatalf("время начала не зафиксировано: %+v", half)
	}

	if err := store.UpdateStatus(ctx, record.ID, jobs.StatusRunning, jobs.StatusDone, "", ""); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	done, err := store.ExportByID(ctx, record.ID)
	if err != nil {
		t.Fatalf("ExportByID: %v", err)
	}
	if done.Progress != jobs.ProgressMax {
		t.Fatalf("прогресс завершённого = %d, ожидалось 100", done.Progress)
	}

	if err := store.UpdateProgress(ctx, "нет-такого", 10, "шаг"); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("прогресс отсутствующего = %v, ожидалась ErrNotFound", err)
	}
}

// TestReportExportStoreDLQListAndRetry проверяет видимость DLQ и явный повтор:
// отбор возвращает только failed-задания с исчерпанными попытками, а повтор
// возвращает задание в очередь с новым бюджетом попыток.
func TestReportExportStoreDLQListAndRetry(t *testing.T) {
	ctx := context.Background()
	store := inmemory.NewReportExportStore()

	// Задание, исчерпавшее попытки: failed при attempts >= порога.
	if err := store.Create(ctx, jobs.Job{ID: "dead", Payload: []byte("x"), Attempts: 2}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.UpdateStatus(ctx, "dead", jobs.StatusQueued, jobs.StatusFailed, "Не удалось собрать файл выгрузки", "export.render.failed"); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	// Задание с одной попыткой в DLQ не попадает: бюджет не исчерпан.
	if err := store.Create(ctx, jobs.Job{ID: "single", Payload: []byte("x"), Attempts: 1}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.UpdateStatus(ctx, "single", jobs.StatusQueued, jobs.StatusFailed, "Сбой", "x"); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	list, err := store.ListJobs(ctx, jobs.DLQFilter(2, 0))
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(list) != 1 || list[0].ID != "dead" {
		t.Fatalf("DLQ = %+v, ожидалось только задание dead", list)
	}
	if list[0].LastError != "Не удалось собрать файл выгрузки" {
		t.Fatalf("причина DLQ = %q", list[0].LastError)
	}

	if err := store.RetryFailed(ctx, "dead"); err != nil {
		t.Fatalf("RetryFailed: %v", err)
	}
	requeued, err := store.ExportByID(ctx, "dead")
	if err != nil {
		t.Fatalf("ExportByID: %v", err)
	}
	if requeued.Status != jobs.StatusQueued || requeued.Error != "" || requeued.Progress != 0 {
		t.Fatalf("после повтора задание = %+v", requeued)
	}
	job, err := store.FindByID(ctx, "dead")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if job.Attempts != 0 {
		t.Fatalf("бюджет попыток после повтора = %d, ожидался 0", job.Attempts)
	}

	if err := store.RetryFailed(ctx, "dead"); !errors.Is(err, jobs.ErrTransitionRejected) {
		t.Fatalf("повтор queued-задания = %v, ожидалась ErrTransitionRejected", err)
	}
	if err := store.RetryFailed(ctx, "нет-такого"); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("повтор отсутствующего = %v, ожидалась ErrNotFound", err)
	}
}
