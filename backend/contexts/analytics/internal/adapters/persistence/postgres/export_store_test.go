package postgres_test

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/analytics/internal/adapters/persistence/postgres"
	"lead-gen/backend/contexts/analytics/internal/ports"
	"lead-gen/backend/platform/jobs"
)

// truncateReportExport — очистка таблицы заданий выгрузки между прогонами.
const truncateReportExport = `TRUNCATE "analytics".report_export_job`

// TestReportExportStorePostgres проверяет PostgreSQL-реализацию заданий выгрузки
// против реальной базы: постановку, взятие, отчёт о ходе (progress/step),
// завершение, отбор DLQ (failed с исчерпанными попытками) и явный повтор.
func TestReportExportStorePostgres(t *testing.T) {
	if testing.Short() {
		t.Skip("PostgreSQL-тест пропущен в коротком режиме")
	}
	ctx := context.Background()
	pool := testPool(t)
	if _, err := pool.Exec(ctx, truncateReportExport); err != nil {
		t.Fatalf("очистка заданий выгрузки: %v", err)
	}
	store := postgres.NewReportExportStore(pool)

	record, err := store.CreateReportExport(ctx, ports.ReportExportRequest{TenantID: "t1", Format: ports.ExportCSV})
	if err != nil {
		t.Fatalf("CreateReportExport: %v", err)
	}
	if record.Status != jobs.StatusQueued {
		t.Fatalf("постановка вернула %+v", record)
	}

	taken, err := store.TakeQueued(ctx)
	if err != nil {
		t.Fatalf("TakeQueued: %v", err)
	}
	if taken.ID != record.ID {
		t.Fatalf("взято задание %s, ожидалось %s", taken.ID, record.ID)
	}
	if err := store.UpdateProgress(ctx, record.ID, 150, "сбор среза"); err != nil {
		t.Fatalf("UpdateProgress: %v", err)
	}
	half, err := store.ExportByID(ctx, record.ID)
	if err != nil {
		t.Fatalf("ExportByID: %v", err)
	}
	if half.Progress != jobs.ProgressMax || half.Step != "сбор среза" || half.StartedAt.IsZero() {
		t.Fatalf("ход задания = %+v, ожидалось 100/сбор среза и время начала", half)
	}
	if err := store.UpdateStatus(ctx, record.ID, jobs.StatusRunning, jobs.StatusDone, "", ""); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	done, err := store.ExportByID(ctx, record.ID)
	if err != nil {
		t.Fatalf("ExportByID: %v", err)
	}
	if done.Status != jobs.StatusDone || done.Progress != jobs.ProgressMax {
		t.Fatalf("завершённое задание = %+v", done)
	}

	// Задание, исчерпавшее попытки: failed при attempts >= порога.
	if err := store.Create(ctx, jobs.Job{ID: "dead", Payload: []byte("x"), Attempts: 2}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.UpdateStatus(ctx, "dead", jobs.StatusQueued, jobs.StatusFailed, "Не удалось собрать файл выгрузки", "export.render.failed"); err != nil {
		t.Fatalf("UpdateStatus failed: %v", err)
	}
	list, err := store.ListJobs(ctx, jobs.DLQFilter(2, 0))
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(list) != 1 || list[0].ID != "dead" || list[0].LastError != "Не удалось собрать файл выгрузки" {
		t.Fatalf("DLQ = %+v, ожидалось задание dead с причиной", list)
	}

	if err := store.RetryFailed(ctx, "dead"); err != nil {
		t.Fatalf("RetryFailed: %v", err)
	}
	requeued, err := store.FindByID(ctx, "dead")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if requeued.Status != jobs.StatusQueued || requeued.Attempts != 0 || requeued.LastError != "" {
		t.Fatalf("после повтора задание = %+v", requeued)
	}
	if err := store.RetryFailed(ctx, "dead"); !errors.Is(err, jobs.ErrTransitionRejected) {
		t.Fatalf("повтор queued-задания = %v, ожидалась ErrTransitionRejected", err)
	}
	if err := store.RetryFailed(ctx, "нет-такого"); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("повтор отсутствующего = %v, ожидалась ErrNotFound", err)
	}
}
