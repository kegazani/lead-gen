package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/analytics/internal/ports"
	"lead-gen/backend/platform/jobs"
)

// ReportExportStore — PostgreSQL-реализация ports.ReportExportStore. Удовлетворяет
// и примитиву jobs.Store (раннер worker), и операциям длительной выгрузки.
type ReportExportStore struct {
	pool *pgxpool.Pool
}

// ReportExportStore реализует порт и примитив.
var (
	_ ports.ReportExportStore = (*ReportExportStore)(nil)
	_ jobs.Store              = (*ReportExportStore)(nil)
)

// NewReportExportStore создаёт хранилище заданий выгрузки поверх пула соединений.
func NewReportExportStore(pool *pgxpool.Pool) *ReportExportStore {
	if pool == nil {
		panic("postgres: NewReportExportStore с nil-пулом")
	}
	return &ReportExportStore{pool: pool}
}

// Readiness проверяет пул без чтения или изменения заданий.
func (s *ReportExportStore) Readiness(ctx context.Context) error { return s.pool.Ping(ctx) }

// Create сохраняет новое задание в состоянии queued (jobs.Store). Повторный
// идентификатор — ошибка.
func (s *ReportExportStore) Create(ctx context.Context, job jobs.Job) error {
	createdAt := job.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	tag, err := s.pool.Exec(ctx, insertReportExport,
		job.ID,
		job.Payload,
		string(jobs.StatusQueued),
		job.LastError,
		job.ErrorCode,
		job.Attempts,
		createdAt,
		createdAt,
	)
	if err != nil {
		return fmt.Errorf("postgres: запись задания выгрузки %s: %w", job.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres: задание выгрузки %s уже существует", job.ID)
	}
	return nil
}

// TakeQueued берёт старейшее queued-задание и переводит его в running, увеличив
// счётчик попыток. Пустая очередь даёт jobs.ErrNoJob. Взятие атомарно: строка
// блокируется SELECT ... FOR UPDATE SKIP LOCKED в транзакции, затем обновляется.
func (s *ReportExportStore) TakeQueued(ctx context.Context) (*jobs.Job, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: открытие транзакции взятия задания выгрузки: %w", err)
	}
	// После успешного commit повторный Rollback возвращает ErrTxClosed.
	defer func() { _ = tx.Rollback(ctx) }()

	var id string
	err = tx.QueryRow(ctx, selectQueuedReportExportID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, jobs.ErrNoJob
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: выбор задания выгрузки из очереди: %w", err)
	}

	row, err := scanReportExport(tx.QueryRow(ctx, updateReportExportToRunning, id))
	if err != nil {
		return nil, fmt.Errorf("postgres: взятие задания выгрузки %s: %w", id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres: коммит взятия задания выгрузки: %w", err)
	}
	return row.toJob(), nil
}

// UpdateStatus переводит задание из состояния from в состояние to (jobs.Store).
// Переход охраняется исходным состоянием: уже терминальное задание не
// перезаписывается. reason и code фиксируются только для failed.
func (s *ReportExportStore) UpdateStatus(ctx context.Context, id string, from, to jobs.Status, reason, code string) error {
	errText := ""
	errCode := ""
	if to == jobs.StatusFailed {
		errText = reason
		errCode = code
	}
	tag, err := s.pool.Exec(ctx, updateReportExportStatus, id, string(from), string(to), errText, errCode)
	if err != nil {
		return fmt.Errorf("postgres: обновление статуса задания выгрузки %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		exists, err := s.exportExists(ctx, id)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("postgres: %w: %s", jobs.ErrNotFound, id)
		}
		return fmt.Errorf("postgres: %w: задание %s не в состоянии %q", jobs.ErrTransitionRejected, id, from)
	}
	return nil
}

// FindByID возвращает задание по идентификатору (jobs.Store). Отсутствующее —
// jobs.ErrNotFound.
func (s *ReportExportStore) FindByID(ctx context.Context, id string) (*jobs.Job, error) {
	row, err := scanReportExport(s.pool.QueryRow(ctx, selectReportExportByID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("postgres: %w: %s", jobs.ErrNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение задания выгрузки %s: %w", id, err)
	}
	return row.toJob(), nil
}

// RequestCancel фиксирует запрос отмены задания одним атомарным UPDATE: queued и
// running переходят в cancelled, терминальные состояния остаются без изменений.
func (s *ReportExportStore) RequestCancel(ctx context.Context, id string) error {
	var oldStatus jobs.Status
	err := s.pool.QueryRow(ctx, requestCancelReportExport, id).Scan(&oldStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("postgres: %w: %s", jobs.ErrNotFound, id)
	}
	if err != nil {
		return fmt.Errorf("postgres: запрос отмены задания выгрузки %s: %w", id, err)
	}
	return nil
}

// ReconcileRunning согласует задания, оставшиеся в running без живого
// обработчика (jobs.Store), одним атомарным запросом.
func (s *ReportExportStore) ReconcileRunning(ctx context.Context, staleBefore time.Time, maxAttempts int, failCode, failReason string) (jobs.ReconcileResult, error) {
	rows, err := s.pool.Query(ctx, reconcileRunningReportExports, staleBefore, maxAttempts, failCode, failReason)
	if err != nil {
		return jobs.ReconcileResult{}, fmt.Errorf("postgres: согласование running-заданий выгрузки: %w", err)
	}
	defer rows.Close()

	var result jobs.ReconcileResult
	for rows.Next() {
		var (
			id     string
			status jobs.Status
		)
		if err := rows.Scan(&id, &status); err != nil {
			return jobs.ReconcileResult{}, fmt.Errorf("postgres: разбор согласованного задания выгрузки: %w", err)
		}
		switch status {
		case jobs.StatusCancelled:
			result.Cancelled++
		case jobs.StatusFailed:
			result.Failed++
		default:
			result.Requeued++
		}
	}
	if err := rows.Err(); err != nil {
		return jobs.ReconcileResult{}, fmt.Errorf("postgres: согласование running-заданий выгрузки: %w", err)
	}
	return result, nil
}

// CreateReportExport ставит задание выгрузки с зафиксированным запросом.
func (s *ReportExportStore) CreateReportExport(ctx context.Context, req ports.ReportExportRequest) (ports.ReportExport, error) {
	id, err := newExportJobID()
	if err != nil {
		return ports.ReportExport{}, err
	}
	payload, err := ports.EncodeReportExportPayload(ports.ReportExportPayloadFromRequest(req))
	if err != nil {
		return ports.ReportExport{}, fmt.Errorf("postgres: кодирование запроса выгрузки: %w", err)
	}
	now := time.Now().UTC()
	if err := s.Create(ctx, jobs.Job{
		ID:        id,
		Status:    jobs.StatusQueued,
		Payload:   payload,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		return ports.ReportExport{}, err
	}
	return ports.ReportExport{
		ID:        id,
		Format:    req.Format,
		Status:    jobs.StatusQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// RecordExportResult сохраняет готовый файл задания, не меняя статус.
func (s *ReportExportStore) RecordExportResult(ctx context.Context, id string, result ports.ReportExportResult) error {
	tag, err := s.pool.Exec(ctx, updateReportExportResult, id, result.ContentType, result.Filename, result.Body)
	if err != nil {
		return fmt.Errorf("postgres: сохранение результата задания выгрузки %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres: %w: %s", ports.ErrReportExportNotFound, id)
	}
	return nil
}

// ExportByID возвращает операционную запись задания.
func (s *ReportExportStore) ExportByID(ctx context.Context, id string) (ports.ReportExport, error) {
	var (
		payload   []byte
		status    string
		errText   string
		errCode   string
		hasBody   bool
		progress  int
		step      string
		startedAt *time.Time
		createdAt time.Time
		updatedAt time.Time
	)
	err := s.pool.QueryRow(ctx, selectReportExportView, id).Scan(
		&id, &payload, &status, &errText, &errCode, &hasBody, &progress, &step, &startedAt, &createdAt, &updatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.ReportExport{}, fmt.Errorf("postgres: %w: %s", ports.ErrReportExportNotFound, id)
	}
	if err != nil {
		return ports.ReportExport{}, fmt.Errorf("postgres: чтение задания выгрузки %s: %w", id, err)
	}
	format := ports.ExportFormat("")
	if decoded, err := ports.DecodeReportExportPayload(payload); err == nil {
		format = decoded.Format
	}
	record := ports.ReportExport{
		ID:              id,
		Format:          format,
		Status:          jobs.Status(status),
		Error:           errText,
		ErrorCode:       errCode,
		ResultAvailable: hasBody,
		Progress:        progress,
		Step:            step,
		CreatedAt:       createdAt,
		UpdatedAt:       updatedAt,
	}
	if startedAt != nil {
		record.StartedAt = *startedAt
	}
	return record, nil
}

// ExportResult возвращает готовый файл задания.
func (s *ReportExportStore) ExportResult(ctx context.Context, id string) (ports.ReportExportResult, error) {
	var (
		contentType string
		filename    string
		body        []byte
		available   bool
	)
	err := s.pool.QueryRow(ctx, selectReportExportResult, id).Scan(&contentType, &filename, &body, &available)
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.ReportExportResult{}, fmt.Errorf("postgres: %w: %s", ports.ErrReportExportNotFound, id)
	}
	if err != nil {
		return ports.ReportExportResult{}, fmt.Errorf("postgres: чтение результата задания выгрузки %s: %w", id, err)
	}
	if !available {
		return ports.ReportExportResult{}, fmt.Errorf("postgres: %w: %s", ports.ErrReportExportNoResult, id)
	}
	return ports.ReportExportResult{
		ContentType: contentType,
		Filename:    filename,
		Body:        body,
	}, nil
}

// exportExists сообщает, есть ли задание с таким идентификатором.
func (s *ReportExportStore) exportExists(ctx context.Context, id string) (bool, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx, reportExportExists, id).Scan(&exists); err != nil {
		return false, fmt.Errorf("postgres: проверка задания выгрузки %s: %w", id, err)
	}
	return exists, nil
}

// UpdateProgress фиксирует долю выполнения и текущий шаг (jobs.ProgressStore).
// Обновляется только running-задание; иначе — jobs.ErrTransitionRejected,
// отсутствующее — jobs.ErrNotFound.
func (s *ReportExportStore) UpdateProgress(ctx context.Context, id string, progress int, step string) error {
	tag, err := s.pool.Exec(ctx, updateReportExportProgress, id, jobs.ClampProgress(progress), step)
	if err != nil {
		return fmt.Errorf("postgres: обновление прогресса задания выгрузки %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		exists, err := s.exportExists(ctx, id)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("postgres: %w: %s", jobs.ErrNotFound, id)
		}
		return fmt.Errorf("postgres: %w: задание %s не в состоянии %q", jobs.ErrTransitionRejected, id, jobs.StatusRunning)
	}
	return nil
}

// ListJobs возвращает задания по отбору в стабильном порядке: по убыванию
// UpdatedAt, при равенстве — по возрастанию ID (jobs.JobLister).
func (s *ReportExportStore) ListJobs(ctx context.Context, filter jobs.JobFilter) ([]jobs.Job, error) {
	statuses := make([]string, 0, len(filter.Statuses))
	for _, status := range filter.Statuses {
		statuses = append(statuses, string(status))
	}
	rows, err := s.pool.Query(ctx, listReportExports, statuses, filter.MinAttempts, filter.Limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение списка заданий выгрузки: %w", err)
	}
	defer rows.Close()

	out := make([]jobs.Job, 0)
	for rows.Next() {
		row, err := scanReportExport(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: разбор задания выгрузки: %w", err)
		}
		out = append(out, *row.toJob())
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: чтение списка заданий выгрузки: %w", err)
	}
	return out, nil
}

// RetryFailed возвращает failed-задание в очередь с новым бюджетом попыток
// (jobs.JobRetrier): счётчик попыток и причина сбрасываются, прогресс, шаг и
// признак отмены очищаются. Задание с иным состоянием — jobs.ErrTransitionRejected,
// отсутствующее — jobs.ErrNotFound.
func (s *ReportExportStore) RetryFailed(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, retryFailedReportExport, id)
	if err != nil {
		return fmt.Errorf("postgres: повтор задания выгрузки %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		exists, err := s.exportExists(ctx, id)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("postgres: %w: %s", jobs.ErrNotFound, id)
		}
		return fmt.Errorf("postgres: %w: задание %s не в состоянии %q", jobs.ErrTransitionRejected, id, jobs.StatusFailed)
	}
	return nil
}

// reportExportRow — строка задания выгрузки, читаемая примитивом jobs.
type reportExportRow struct {
	id        string
	payload   []byte
	status    jobs.Status
	errText   string
	errCode   string
	attempts  int
	cancel    bool
	progress  int
	step      string
	startedAt *time.Time
	createdAt time.Time
	updatedAt time.Time
}

// toJob собирает jobs.Job из строки.
func (r reportExportRow) toJob() *jobs.Job {
	job := &jobs.Job{
		ID:              r.id,
		Status:          r.status,
		Payload:         r.payload,
		Attempts:        r.attempts,
		LastError:       r.errText,
		ErrorCode:       r.errCode,
		Progress:        r.progress,
		Step:            r.step,
		CancelRequested: r.cancel,
		CreatedAt:       r.createdAt,
		UpdatedAt:       r.updatedAt,
	}
	if r.startedAt != nil {
		job.StartedAt = *r.startedAt
	}
	return job
}

// rowScanner — минимальная поверхность строки, нужная сканированию.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanReportExport читает строку задания выгрузки.
func scanReportExport(row rowScanner) (reportExportRow, error) {
	var r reportExportRow
	if err := row.Scan(
		&r.id,
		&r.payload,
		&r.status,
		&r.errText,
		&r.errCode,
		&r.attempts,
		&r.cancel,
		&r.progress,
		&r.step,
		&r.startedAt,
		&r.createdAt,
		&r.updatedAt,
	); err != nil {
		return reportExportRow{}, err
	}
	return r, nil
}

// newExportJobID генерирует случайный идентификатор задания (32 hex-символа).
func newExportJobID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("postgres: генерация идентификатора: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// SQL-запросы адаптера заданий выгрузки. result_body в SELECT заранее не
// COALESCE-руется: NULL означает несформированный результат.
const (
	insertReportExport = `
INSERT INTO "analytics".report_export_job
    (id, payload, status, error_text, error_code, attempts, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (id) DO NOTHING`

	selectQueuedReportExportID = `
SELECT id
FROM "analytics".report_export_job
WHERE status = 'queued'
ORDER BY created_at, id
LIMIT 1
FOR UPDATE SKIP LOCKED`

	updateReportExportToRunning = `
UPDATE "analytics".report_export_job
SET status = 'running', attempts = attempts + 1, started_at = now(), updated_at = now()
WHERE id = $1
RETURNING id, payload, status, COALESCE(error_text, ''), COALESCE(error_code, ''), attempts, cancel_requested, progress, COALESCE(step, ''), started_at, created_at, updated_at`

	updateReportExportStatus = `
UPDATE "analytics".report_export_job
SET status = $3, error_text = $4, error_code = $5,
    progress = CASE WHEN $3 = 'done' THEN 100 ELSE progress END, updated_at = now()
WHERE id = $1 AND status = $2`

	updateReportExportProgress = `
UPDATE "analytics".report_export_job
SET progress = $2, step = $3, updated_at = now()
WHERE id = $1 AND status = 'running'`

	selectReportExportByID = `
SELECT id, payload, status, COALESCE(error_text, ''), COALESCE(error_code, ''), attempts, cancel_requested, progress, COALESCE(step, ''), started_at, created_at, updated_at
FROM "analytics".report_export_job
WHERE id = $1`

	listReportExports = `
SELECT id, payload, status, COALESCE(error_text, ''), COALESCE(error_code, ''), attempts, cancel_requested, progress, COALESCE(step, ''), started_at, created_at, updated_at
FROM "analytics".report_export_job
WHERE (cardinality($1::text[]) = 0 OR status = ANY($1::text[]))
  AND ($2 <= 0 OR attempts >= $2)
ORDER BY updated_at DESC, id ASC
LIMIT CASE WHEN $3 > 0 THEN $3 ELSE NULL END`

	retryFailedReportExport = `
UPDATE "analytics".report_export_job
SET status = 'queued', attempts = 0, error_text = '', error_code = '',
    progress = 0, step = '', cancel_requested = FALSE, started_at = NULL, updated_at = now()
WHERE id = $1 AND status = 'failed'`

	reportExportExists = `
SELECT EXISTS (SELECT 1 FROM "analytics".report_export_job WHERE id = $1)`

	reconcileRunningReportExports = `
UPDATE "analytics".report_export_job
SET status = CASE
        WHEN cancel_requested THEN 'cancelled'
        WHEN attempts >= $2 THEN 'failed'
        ELSE 'queued'
    END,
    error_text = CASE WHEN NOT cancel_requested AND attempts >= $2 THEN $4 ELSE error_text END,
    error_code = CASE WHEN NOT cancel_requested AND attempts >= $2 THEN $3 ELSE error_code END,
    updated_at = now()
WHERE status = 'running' AND updated_at < $1
RETURNING id, status`

	requestCancelReportExport = `
WITH old_status AS (
    SELECT status FROM "analytics".report_export_job WHERE id = $1
)
UPDATE "analytics".report_export_job
SET
    status = CASE WHEN status IN ('queued','running') THEN 'cancelled' ELSE status END,
    cancel_requested = CASE WHEN status = 'running' THEN TRUE ELSE cancel_requested END,
    updated_at = CASE WHEN status IN ('queued','running') THEN now() ELSE updated_at END
WHERE id = $1
RETURNING (SELECT status FROM old_status)`

	updateReportExportResult = `
UPDATE "analytics".report_export_job
SET result_content_type = $2, result_filename = $3, result_body = $4, updated_at = now()
WHERE id = $1`

	selectReportExportView = `
SELECT id, payload, status, COALESCE(error_text, ''), COALESCE(error_code, ''), (result_body IS NOT NULL), progress, COALESCE(step, ''), started_at, created_at, updated_at
FROM "analytics".report_export_job
WHERE id = $1`

	selectReportExportResult = `
SELECT COALESCE(result_content_type, ''), COALESCE(result_filename, ''), COALESCE(result_body, ''::bytea), (result_body IS NOT NULL)
FROM "analytics".report_export_job
WHERE id = $1`
)
