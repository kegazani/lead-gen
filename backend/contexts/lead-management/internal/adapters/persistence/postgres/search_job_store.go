package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/backend/platform/jobs"
	"lead-gen/contracts/integration-platform/budget"
	"lead-gen/contracts/integration-platform/llm"
)

// SearchJobStore — PostgreSQL-реализация ports.SearchJobStore. Удовлетворяет и
// примитиву jobs.Store (раннер worker), и операциям витрины журнала.
type SearchJobStore struct {
	pool *pgxpool.Pool
}

// SearchJobStore реализует порт хранилища заданий и примитив jobs.Store.
var (
	_ ports.SearchJobStore = (*SearchJobStore)(nil)
	_ jobs.Store           = (*SearchJobStore)(nil)
)

// NewSearchJobStore создаёт хранилище поверх пула соединений.
func NewSearchJobStore(pool *pgxpool.Pool) *SearchJobStore {
	if pool == nil {
		panic("postgres: NewSearchJobStore с nil-пулом")
	}
	return &SearchJobStore{pool: pool}
}

// Create сохраняет новое задание в состоянии queued (jobs.Store). Запрос и бюджет
// извлекаются из JSON-закодированного Payload; для не-JSON Payload берётся его
// текст, а бюджет считается не заданным. Повторный идентификатор — ошибка.
func (s *SearchJobStore) Create(ctx context.Context, job jobs.Job) error {
	query := string(job.Payload)
	maxCredits := 0
	targetCompanies := 0
	maxResults := 0
	var plan *ports.SearchPlan
	var interpretation *llm.Interpretation
	if p, err := ports.DecodeSearchJobPayload(job.Payload); err == nil {
		query = p.Query
		maxCredits = p.MaxCredits
		targetCompanies = p.TargetCompanies
		maxResults = p.MaxResults
		plan = p.Plan
		interpretation = p.Interpretation
	}
	planJSON, err := marshalPlan(plan)
	if err != nil {
		return fmt.Errorf("postgres: сериализация плана задания %s: %w", job.ID, err)
	}
	interpretationJSON, err := marshalInterpretation(interpretation)
	if err != nil {
		return fmt.Errorf("postgres: сериализация интерпретации задания %s: %w", job.ID, err)
	}
	createdAt := job.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	tag, err := s.pool.Exec(ctx, insertSearchJob,
		job.ID,
		query,
		string(jobs.StatusQueued),
		"[]",
		job.LastError,
		job.Attempts,
		createdAt,
		createdAt,
		nullableInt(maxCredits),
		nullableInt(targetCompanies),
		nullableInt(maxResults),
		planJSON,
		interpretationJSON,
	)
	if err != nil {
		return fmt.Errorf("postgres: запись задания %s: %w", job.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres: задание %s уже существует", job.ID)
	}
	_ = s.RecordEvent(ctx, ports.SearchJobEvent{JobID: job.ID, Type: ports.EventEnqueued, CreatedAt: createdAt})
	return nil
}

// TakeQueued берёт старейшее queued-задание и переводит его в running, увеличив
// счётчик попыток. Пустая очередь даёт jobs.ErrNoJob. Взятие атомарно: строка
// блокируется SELECT ... FOR UPDATE SKIP LOCKED в транзакции, затем обновляется,
// поэтому одно задание не выдаётся двум воркерам.
func (s *SearchJobStore) TakeQueued(ctx context.Context) (*jobs.Job, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: открытие транзакции взятия задания: %w", err)
	}
	// После успешного commit повторный Rollback возвращает ErrTxClosed, поэтому
	// ошибка отката здесь намеренно игнорируется.
	defer func() { _ = tx.Rollback(ctx) }()

	var id string
	err = tx.QueryRow(ctx, selectQueuedSearchJobID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, jobs.ErrNoJob
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: выбор задания из очереди: %w", err)
	}

	row, err := scanSearchJob(tx.QueryRow(ctx, updateSearchJobToRunning, id))
	if err != nil {
		return nil, fmt.Errorf("postgres: взятие задания %s: %w", id, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres: коммит взятия задания: %w", err)
	}
	_ = s.RecordEvent(ctx, ports.SearchJobEvent{JobID: id, Type: ports.EventTaken, CreatedAt: time.Now().UTC()})
	return row.toJob(), nil
}

// UpdateStatus переводит задание из состояния from в состояние to (jobs.Store).
// Переход применяется только если текущее состояние равно from; иначе —
// jobs.ErrTransitionRejected, и состояние не меняется. reason фиксирует причину
// перехода в failed, code — машинный код отказа; для прочих состояний оба
// игнорируются (ошибка сбрасывается). Отсутствующее задание — jobs.ErrNotFound.
func (s *SearchJobStore) UpdateStatus(ctx context.Context, id string, from, to jobs.Status, reason string, code string) error {
	errText := ""
	errCode := ""
	if to == jobs.StatusFailed {
		errText = reason
		errCode = code
	}
	tag, err := s.pool.Exec(ctx, updateSearchJobStatus, id, string(from), string(to), errText, errCode)
	if err != nil {
		return fmt.Errorf("postgres: обновление статуса задания %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		// Различаем отсутствующее задание и отклонённый переход: запрос находит
		// запись, но она не в исходном состоянии.
		exists, err := s.jobExists(ctx, id)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("postgres: %w: %s", jobs.ErrNotFound, id)
		}
		return fmt.Errorf("postgres: %w: задание %s не в состоянии %q", jobs.ErrTransitionRejected, id, from)
	}
	if t := eventTypeForStatus(to); t != "" {
		_ = s.RecordEvent(ctx, ports.SearchJobEvent{JobID: id, Type: t, Code: errCode, CreatedAt: time.Now().UTC()})
	}
	return nil
}

// FindByID возвращает задание по идентификатору (jobs.Store). Отсутствующее
// задание — jobs.ErrNotFound. Чтение нужно для распознавания отмены по данным.
func (s *SearchJobStore) FindByID(ctx context.Context, id string) (*jobs.Job, error) {
	row, err := scanSearchJob(s.pool.QueryRow(ctx, selectSearchJobByID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("postgres: %w: %s", jobs.ErrNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение задания %s: %w", id, err)
	}
	return row.toJob(), nil
}

// jobExists сообщает, есть ли задание с таким идентификатором.
func (s *SearchJobStore) jobExists(ctx context.Context, id string) (bool, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx, searchJobExists, id).Scan(&exists); err != nil {
		return false, fmt.Errorf("postgres: проверка задания %s: %w", id, err)
	}
	return exists, nil
}

// RequestCancel фиксирует запрос отмены задания одним атомарным UPDATE: queued и
// running переходят в cancelled (выполняющееся задание дополнительно получает
// признак запроса отмены для живого обработчика), терминальные состояния остаются
// без изменений. Отсутствующее задание — ошибка.
func (s *SearchJobStore) RequestCancel(ctx context.Context, id string) error {
	var oldStatus jobs.Status
	err := s.pool.QueryRow(ctx, requestCancelSearchJob, id).Scan(&oldStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("postgres: %w: %s", jobs.ErrNotFound, id)
	}
	if err != nil {
		return fmt.Errorf("postgres: запрос отмены задания %s: %w", id, err)
	}
	switch oldStatus {
	case jobs.StatusQueued:
		_ = s.RecordEvent(ctx, ports.SearchJobEvent{JobID: id, Type: ports.EventCancelled, CreatedAt: time.Now().UTC()})
	case jobs.StatusRunning:
		_ = s.RecordEvent(ctx, ports.SearchJobEvent{JobID: id, Type: ports.EventCancelRequested, CreatedAt: time.Now().UTC()})
		_ = s.RecordEvent(ctx, ports.SearchJobEvent{JobID: id, Type: ports.EventCancelled, CreatedAt: time.Now().UTC()})
	}
	return nil
}

// ReconcileRunning согласует задания, оставшиеся в running без живого
// обработчика (jobs.Store), одним атомарным запросом. Задание с признаком отмены
// переводится в cancelled; с исчерпанным лимитом попыток — в failed с кодом и
// формулировкой; прочее возвращается в queued. Согласуются только задания с
// активностью старше staleBefore. Возвращает счётчики исходов.
func (s *SearchJobStore) ReconcileRunning(ctx context.Context, staleBefore time.Time, maxAttempts int, failCode, failReason string) (jobs.ReconcileResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return jobs.ReconcileResult{}, fmt.Errorf("postgres: открытие транзакции согласования: %w", err)
	}
	// После успешного commit повторный Rollback возвращает ErrTxClosed, поэтому
	// ошибка отката здесь намеренно игнорируется.
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, reconcileRunningSearchJobs, staleBefore, maxAttempts, failCode, failReason)
	if err != nil {
		return jobs.ReconcileResult{}, fmt.Errorf("postgres: согласование running-заданий: %w", err)
	}
	type outcome struct {
		id     string
		status jobs.Status
		code   string
	}
	var (
		result   jobs.ReconcileResult
		outcomes []outcome
	)
	for rows.Next() {
		var o outcome
		if err := rows.Scan(&o.id, &o.status, &o.code); err != nil {
			rows.Close()
			return jobs.ReconcileResult{}, fmt.Errorf("postgres: разбор согласованного задания: %w", err)
		}
		outcomes = append(outcomes, o)
		switch o.status {
		case jobs.StatusCancelled:
			result.Cancelled++
		case jobs.StatusFailed:
			result.Failed++
		default:
			result.Requeued++
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return jobs.ReconcileResult{}, fmt.Errorf("postgres: согласование running-заданий: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return jobs.ReconcileResult{}, fmt.Errorf("postgres: коммит согласования running-заданий: %w", err)
	}

	// События терминальных исходов пишутся после коммита: согласование уже
	// состоялось, а пропуск события не должен его отменять.
	for _, o := range outcomes {
		var eventType ports.SearchJobEventType
		switch o.status {
		case jobs.StatusCancelled:
			eventType = ports.EventCancelled
		case jobs.StatusFailed:
			eventType = ports.EventFailed
		default:
			continue
		}
		_ = s.RecordEvent(ctx, ports.SearchJobEvent{JobID: o.id, Type: eventType, Code: o.code, CreatedAt: time.Now().UTC()})
	}
	return result, nil
}

// CreateSearchJob ставит задание поиска с запросом и бюджетом кредитов и
// возвращает его витринную запись. Идентификатор генерируется адаптером.
func (s *SearchJobStore) CreateSearchJob(ctx context.Context, query string, maxCredits int) (ports.SearchJob, error) {
	id, err := newJobID()
	if err != nil {
		return ports.SearchJob{}, err
	}
	payload, err := ports.EncodeSearchJobPayload(ports.SearchJobPayload{Query: query, MaxCredits: maxCredits})
	if err != nil {
		return ports.SearchJob{}, fmt.Errorf("postgres: кодирование запроса: %w", err)
	}
	now := time.Now().UTC()
	if err := s.Create(ctx, jobs.Job{
		ID:        id,
		Status:    jobs.StatusQueued,
		Payload:   payload,
		Attempts:  0,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		return ports.SearchJob{}, err
	}
	return ports.SearchJob{
		ID:         id,
		Query:      query,
		Status:     jobs.StatusQueued,
		ResultRef:  []string{},
		Error:      "",
		MaxCredits: maxCredits,
		CreatedAt:  now,
		UpdatedAt:  now,
	}, nil
}

// CreateSearchJobWithPlan ставит задание поиска с согласованным планом обхода и
// бюджетом кредитов. План сохраняется в Payload и в колонке plan, переживая
// взятие из очереди.
func (s *SearchJobStore) CreateSearchJobWithPlan(ctx context.Context, query string, plan ports.SearchPlan, maxCredits, targetCompanies, maxResults int) (ports.SearchJob, error) {
	id, err := newJobID()
	if err != nil {
		return ports.SearchJob{}, err
	}
	payload, err := ports.EncodeSearchJobPayload(ports.SearchJobPayload{Query: query, MaxCredits: maxCredits, TargetCompanies: targetCompanies, MaxResults: maxResults, Plan: &plan})
	if err != nil {
		return ports.SearchJob{}, fmt.Errorf("postgres: кодирование запроса: %w", err)
	}
	now := time.Now().UTC()
	if err := s.Create(ctx, jobs.Job{
		ID:        id,
		Status:    jobs.StatusQueued,
		Payload:   payload,
		Attempts:  0,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		return ports.SearchJob{}, err
	}
	return ports.SearchJob{
		ID:         id,
		Query:      query,
		Status:     jobs.StatusQueued,
		ResultRef:  []string{},
		Error:      "",
		MaxCredits: maxCredits,
		CreatedAt:  now,
		UpdatedAt:  now,
	}, nil
}

// RecordBudget фиксирует снимок бюджета Firecrawl задачи, не меняя статус.
func (s *SearchJobStore) RecordBudget(ctx context.Context, id string, b budget.Snapshot) error {
	raw, err := json.Marshal(b)
	if err != nil {
		return fmt.Errorf("postgres: сериализация бюджета задания: %w", err)
	}
	tag, err := s.pool.Exec(ctx, updateSearchJobBudget, id, string(raw))
	if err != nil {
		return fmt.Errorf("postgres: запись бюджета задания %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres: задание %s не найдено", id)
	}
	return nil
}

// RecordProgress фиксирует прогресс выполнения задания, не меняя статус.
func (s *SearchJobStore) RecordProgress(ctx context.Context, id string, progress ports.SearchJobProgress) error {
	raw, err := json.Marshal(progress)
	if err != nil {
		return fmt.Errorf("postgres: сериализация прогресса задания: %w", err)
	}
	tag, err := s.pool.Exec(ctx, updateSearchJobProgress, id, string(raw))
	if err != nil {
		return fmt.Errorf("postgres: запись прогресса задания %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres: задание %s не найдено", id)
	}
	return nil
}

// RecordResult фиксирует ссылку на найденные компании, не меняя статус.
func (s *SearchJobStore) RecordResult(ctx context.Context, id string, resultRef []string) error {
	raw, err := marshalResultRef(resultRef)
	if err != nil {
		return fmt.Errorf("postgres: сериализация ссылок результата: %w", err)
	}
	tag, err := s.pool.Exec(ctx, updateSearchJobResultRef, id, string(raw))
	if err != nil {
		return fmt.Errorf("postgres: запись результата задания %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres: задание %s не найдено", id)
	}
	return nil
}

// SetInterpretation фиксирует критерии релевантности задания отдельным полем,
// не меняя статус. nil записывается как SQL NULL — «интерпретация не задана».
func (s *SearchJobStore) SetInterpretation(ctx context.Context, id string, interpretation *llm.Interpretation) error {
	raw, err := marshalInterpretation(interpretation)
	if err != nil {
		return fmt.Errorf("postgres: сериализация интерпретации задания: %w", err)
	}
	tag, err := s.pool.Exec(ctx, updateSearchJobInterpretation, id, raw)
	if err != nil {
		return fmt.Errorf("postgres: запись интерпретации задания %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres: задание %s не найдено", id)
	}
	return nil
}

// RecordEvent добавляет событие жизненного цикла задания (append-only).
func (s *SearchJobStore) RecordEvent(ctx context.Context, event ports.SearchJobEvent) error {
	createdAt := event.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	var payload any
	if event.Progress != nil {
		raw, err := json.Marshal(event.Progress)
		if err != nil {
			return fmt.Errorf("postgres: сериализация события задания: %w", err)
		}
		payload = string(raw)
	}
	var code any
	if event.Code != "" {
		code = event.Code
	}
	if _, err := s.pool.Exec(ctx, insertSearchJobEvent, event.JobID, string(event.Type), code, payload, createdAt); err != nil {
		return fmt.Errorf("postgres: запись события задания %s: %w", event.JobID, err)
	}
	return nil
}

// eventTypeForStatus отображает терминальное состояние в тип события; для
// не-терминальных состояний возвращает пустую строку (события не пишутся).
func eventTypeForStatus(status jobs.Status) ports.SearchJobEventType {
	switch status {
	case jobs.StatusDone:
		return ports.EventDone
	case jobs.StatusFailed:
		return ports.EventFailed
	case jobs.StatusCancelled:
		return ports.EventCancelled
	default:
		return ""
	}
}

// ListEvents возвращает события задания в порядке записи.
func (s *SearchJobStore) ListEvents(ctx context.Context, jobID string) ([]ports.SearchJobEvent, error) {
	rows, err := s.pool.Query(ctx, selectSearchJobEvents, jobID)
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение событий задания %s: %w", jobID, err)
	}
	defer rows.Close()

	events := make([]ports.SearchJobEvent, 0)
	for rows.Next() {
		var e ports.SearchJobEvent
		var eventType string
		var code *string
		var payloadJSON *string
		if err := rows.Scan(&e.JobID, &eventType, &code, &payloadJSON, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("postgres: разбор события задания: %w", err)
		}
		e.Type = ports.SearchJobEventType(eventType)
		if code != nil {
			e.Code = *code
		}
		if payloadJSON != nil {
			p, err := unmarshalProgress([]byte(*payloadJSON))
			if err != nil {
				return nil, err
			}
			e.Progress = p
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: чтение событий задания %s: %w", jobID, err)
	}
	return events, nil
}

// ListSearchJobs возвращает журнал заданий, новые первыми, не более limit штук.
func (s *SearchJobStore) ListSearchJobs(ctx context.Context, limit int) ([]ports.SearchJob, error) {
	if limit <= 0 {
		return []ports.SearchJob{}, nil
	}
	rows, err := s.pool.Query(ctx, selectSearchJobs, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение журнала заданий: %w", err)
	}
	defer rows.Close()

	list := make([]ports.SearchJob, 0)
	for rows.Next() {
		row, err := scanSearchJob(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: разбор задания журнала: %w", err)
		}
		list = append(list, row.toSearchJob())
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: чтение журнала заданий: %w", err)
	}
	return list, nil
}

// searchJobRow — строка таблицы search_job с уже разобранными JSON-полями.
type searchJobRow struct {
	id              string
	query           string
	status          jobs.Status
	resultRef       []string
	errText         string
	errCode         string
	budget          *budget.Snapshot
	maxCredits      int
	targetCompanies int
	maxResults      int
	plan            *ports.SearchPlan
	interpretation  *llm.Interpretation
	cancelRequested bool
	progress        *ports.SearchJobProgress
	attempts        int
	queuePosition   int
	createdAt       time.Time
	updatedAt       time.Time
}

// toJob собирает jobs.Job из строки, закодировав запрос, бюджет и план в Payload.
func (r searchJobRow) toJob() *jobs.Job {
	payload, _ := ports.EncodeSearchJobPayload(ports.SearchJobPayload{Query: r.query, MaxCredits: r.maxCredits, TargetCompanies: r.targetCompanies, MaxResults: r.maxResults, Plan: r.plan, Interpretation: r.interpretation})
	return &jobs.Job{
		ID:              r.id,
		Status:          r.status,
		Payload:         payload,
		Attempts:        r.attempts,
		LastError:       r.errText,
		ErrorCode:       r.errCode,
		CancelRequested: r.cancelRequested,
		CreatedAt:       r.createdAt,
		UpdatedAt:       r.updatedAt,
	}
}

// toSearchJob собирает витринную запись из строки.
func (r searchJobRow) toSearchJob() ports.SearchJob {
	return ports.SearchJob{
		ID:             r.id,
		Query:          r.query,
		Status:         r.status,
		ResultRef:      r.resultRef,
		Error:          r.errText,
		ErrorCode:      r.errCode,
		QueuePosition:  r.queuePosition,
		Budget:         r.budget,
		MaxCredits:     r.maxCredits,
		Interpretation: r.interpretation,
		Progress:       r.progress,
		CreatedAt:      r.createdAt,
		UpdatedAt:      r.updatedAt,
	}
}

// rowScanner — минимальная поверхность чтения строки; ей удовлетворяют и
// pgx.Row (одна строка), и pgx.Rows (набор строк).
type rowScanner interface {
	Scan(dest ...any) error
}

// scanSearchJob разбирает строку задания. Запросы заранее COALESCE-руют
// result_ref, error, budget, max_credits и progress, поэтому значения не-null при
// сканировании.
func scanSearchJob(scanner rowScanner) (searchJobRow, error) {
	var r searchJobRow
	var resultRefJSON, budgetJSON, progressJSON, planJSON, interpretationJSON string
	if err := scanner.Scan(
		&r.id,
		&r.query,
		&r.status,
		&resultRefJSON,
		&r.errText,
		&r.errCode,
		&r.attempts,
		&r.createdAt,
		&r.updatedAt,
		&budgetJSON,
		&r.maxCredits,
		&r.targetCompanies,
		&r.maxResults,
		&planJSON,
		&interpretationJSON,
		&r.cancelRequested,
		&progressJSON,
		&r.queuePosition,
	); err != nil {
		return searchJobRow{}, err
	}
	ref, err := unmarshalResultRef([]byte(resultRefJSON))
	if err != nil {
		return searchJobRow{}, err
	}
	r.resultRef = ref
	b, err := unmarshalBudget([]byte(budgetJSON))
	if err != nil {
		return searchJobRow{}, err
	}
	r.budget = b
	plan, err := unmarshalPlan([]byte(planJSON))
	if err != nil {
		return searchJobRow{}, err
	}
	r.plan = plan
	interpretation, err := unmarshalInterpretation([]byte(interpretationJSON))
	if err != nil {
		return searchJobRow{}, err
	}
	r.interpretation = interpretation
	progress, err := unmarshalProgress([]byte(progressJSON))
	if err != nil {
		return searchJobRow{}, err
	}
	r.progress = progress
	return r, nil
}

// marshalResultRef кодирует ссылки результата как JSON-массив; nil и пустой
// срез неразличимы и кодируются как [].
func marshalResultRef(ref []string) ([]byte, error) {
	if ref == nil {
		ref = []string{}
	}
	return json.Marshal(ref)
}

// nullableInt возвращает nil для нуля (бюджет не задан) и значение иначе.
func nullableInt(v int) any {
	if v <= 0 {
		return nil
	}
	return v
}

// unmarshalBudget разбирает JSON-снимок бюджета; пустой ввод даёт nil (бюджет
// не применялся).
func unmarshalBudget(raw []byte) (*budget.Snapshot, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var b budget.Snapshot
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

// marshalPlan кодирует план задания как JSON; nil-план кодируется как SQL NULL.
func marshalPlan(plan *ports.SearchPlan) (any, error) {
	if plan == nil {
		return nil, nil
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		return nil, err
	}
	return string(raw), nil
}

// unmarshalPlan разбирает JSON-план; пустой ввод даёт nil (плана нет, работает
// фолбэк на планирование по запросу).
func unmarshalPlan(raw []byte) (*ports.SearchPlan, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var plan ports.SearchPlan
	if err := json.Unmarshal(raw, &plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

// marshalInterpretation кодирует интерпретацию задания как JSON; nil кодируется
// как SQL NULL.
func marshalInterpretation(i *llm.Interpretation) (any, error) {
	if i == nil {
		return nil, nil
	}
	raw, err := json.Marshal(i)
	if err != nil {
		return nil, err
	}
	return string(raw), nil
}

// unmarshalInterpretation разбирает JSON-интерпретацию; пустой ввод даёт nil
// (интерпретация не задана).
func unmarshalInterpretation(raw []byte) (*llm.Interpretation, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var i llm.Interpretation
	if err := json.Unmarshal(raw, &i); err != nil {
		return nil, err
	}
	return &i, nil
}

// unmarshalProgress разбирает JSON-снимок прогресса; пустой ввод даёт nil
// (прогресс не публиковался).
func unmarshalProgress(raw []byte) (*ports.SearchJobProgress, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var p ports.SearchJobProgress
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// unmarshalResultRef разбирает JSON-массив ссылок; null и пустой ввод дают
// пустой срез.
func unmarshalResultRef(raw []byte) ([]string, error) {
	if len(raw) == 0 {
		return []string{}, nil
	}
	var ref []string
	if err := json.Unmarshal(raw, &ref); err != nil {
		return nil, err
	}
	if ref == nil {
		ref = []string{}
	}
	return ref, nil
}

// newJobID генерирует случайный идентификатор задания (32 hex-символа).
func newJobID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("postgres: генерация идентификатора: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// SQL-запросы адаптера заданий. result_ref, error, budget, max_credits и progress
// в SELECT/RETURNING заранее COALESCE-руются, чтобы NULL не ломал сканирование.
const (
	insertSearchJob = `
INSERT INTO "lead-management".search_job
    (id, query, status, result_ref, error, attempts, created_at, updated_at, max_credits, target_companies, max_results, plan, interpretation)
VALUES ($1, $2, $3, $4::jsonb, $5, $6, $7, $8, $9, $10, $11, $12::jsonb, $13::jsonb)
ON CONFLICT (id) DO NOTHING`

	selectQueuedSearchJobID = `
SELECT id
FROM "lead-management".search_job
WHERE status = 'queued'
ORDER BY created_at, id
LIMIT 1
FOR UPDATE SKIP LOCKED`

	updateSearchJobToRunning = `
UPDATE "lead-management".search_job
SET status = 'running', attempts = attempts + 1, updated_at = now()
WHERE id = $1
RETURNING id, query, status, COALESCE(result_ref::text, '[]'), COALESCE(error, ''), COALESCE(error_code, ''), attempts, created_at, updated_at, COALESCE(budget::text, ''), COALESCE(max_credits, 0), COALESCE(target_companies, 0), COALESCE(max_results, 0), COALESCE(plan::text, ''), COALESCE(interpretation::text, ''), cancel_requested, COALESCE(progress::text, ''), 0`

	updateSearchJobStatus = `
UPDATE "lead-management".search_job
SET status = $3, error = $4, error_code = $5, updated_at = now()
WHERE id = $1 AND status = $2`

	selectSearchJobByID = `
SELECT id, query, status, COALESCE(result_ref::text, '[]'), COALESCE(error, ''), COALESCE(error_code, ''), attempts, created_at, updated_at, COALESCE(budget::text, ''), COALESCE(max_credits, 0), COALESCE(target_companies, 0), COALESCE(max_results, 0), COALESCE(plan::text, ''), COALESCE(interpretation::text, ''), cancel_requested, COALESCE(progress::text, ''), 0
FROM "lead-management".search_job
WHERE id = $1`

	searchJobExists = `
SELECT EXISTS (SELECT 1 FROM "lead-management".search_job WHERE id = $1)`

	reconcileRunningSearchJobs = `
UPDATE "lead-management".search_job
SET status = CASE
        WHEN cancel_requested THEN 'cancelled'
        WHEN attempts >= $2 THEN 'failed'
        ELSE 'queued'
    END,
    error = CASE WHEN NOT cancel_requested AND attempts >= $2 THEN $4 ELSE error END,
    error_code = CASE WHEN NOT cancel_requested AND attempts >= $2 THEN $3 ELSE error_code END,
    updated_at = now()
WHERE status = 'running' AND updated_at < $1
RETURNING id, status, COALESCE(error_code, '')`

	requestCancelSearchJob = `
WITH old_status AS (
    SELECT status FROM "lead-management".search_job WHERE id = $1
)
UPDATE "lead-management".search_job
SET
    status = CASE WHEN status IN ('queued','running') THEN 'cancelled' ELSE status END,
    cancel_requested = CASE WHEN status = 'running' THEN TRUE ELSE cancel_requested END,
    updated_at = CASE WHEN status IN ('queued','running') THEN now() ELSE updated_at END
WHERE id = $1
RETURNING (SELECT status FROM old_status)`

	updateSearchJobResultRef = `
UPDATE "lead-management".search_job
SET result_ref = $2::jsonb, updated_at = now()
WHERE id = $1`

	updateSearchJobBudget = `
UPDATE "lead-management".search_job
SET budget = $2::jsonb, updated_at = now()
WHERE id = $1`

	updateSearchJobProgress = `
UPDATE "lead-management".search_job
SET progress = $2::jsonb, updated_at = now()
WHERE id = $1`

	updateSearchJobInterpretation = `
UPDATE "lead-management".search_job
SET interpretation = $2::jsonb, updated_at = now()
WHERE id = $1`

	selectSearchJobs = `
SELECT id, query, status, COALESCE(result_ref::text, '[]'), COALESCE(error, ''), COALESCE(error_code, ''), attempts, created_at, updated_at, COALESCE(budget::text, ''), COALESCE(max_credits, 0), COALESCE(target_companies, 0), COALESCE(max_results, 0), COALESCE(plan::text, ''), COALESCE(interpretation::text, ''), cancel_requested, COALESCE(progress::text, ''),
    CASE WHEN status = 'queued' THEN
        (SELECT COUNT(*) + 1 FROM "lead-management".search_job q
          WHERE q.status = 'queued'
            AND (q.created_at < j.created_at OR (q.created_at = j.created_at AND q.id < j.id)))
    ELSE 0 END
FROM "lead-management".search_job j
ORDER BY created_at DESC, id DESC
LIMIT $1`

	insertSearchJobEvent = `
INSERT INTO "lead-management".search_job_event (job_id, type, code, payload, created_at)
VALUES ($1, $2, $3, $4, $5)`

	selectSearchJobEvents = `
SELECT job_id, type, code, payload::text, created_at
FROM "lead-management".search_job_event
WHERE job_id = $1
ORDER BY seq`
)
