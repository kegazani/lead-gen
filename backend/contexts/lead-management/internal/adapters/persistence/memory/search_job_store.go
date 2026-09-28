package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/backend/platform/jobs"
	"lead-gen/contracts/integration-platform/budget"
	"lead-gen/contracts/integration-platform/llm"
)

// searchJobRecord — хранимое состояние задания поиска. Запрос и бюджет хранятся
// отдельными полями, а в jobs.Job переносятся как JSON-закодированный Payload,
// чтобы примитив jobs оставался непрозрачным к содержимому.
type searchJobRecord struct {
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
	createdAt       time.Time
	updatedAt       time.Time
}

// SearchJobStore — in-memory реализация ports.SearchJobStore. Удовлетворяет и
// примитиву jobs.Store (раннер worker), и операциям витрины журнала.
type SearchJobStore struct {
	mu     sync.Mutex
	jobs   map[string]*searchJobRecord
	events []ports.SearchJobEvent
}

// SearchJobStore реализует порт хранилища заданий и примитив jobs.Store.
var (
	_ ports.SearchJobStore = (*SearchJobStore)(nil)
	_ jobs.Store           = (*SearchJobStore)(nil)
)

// NewSearchJobStore создаёт пустое хранилище.
func NewSearchJobStore() *SearchJobStore {
	return &SearchJobStore{jobs: make(map[string]*searchJobRecord)}
}

// recordEventLocked добавляет событие; вызывающий держит блокировку.
func (s *SearchJobStore) recordEventLocked(event ports.SearchJobEvent) {
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	s.events = append(s.events, event)
}

// eventTypeForStatus отображает терминальное состояние в тип события.
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

// Create сохраняет новое задание в состоянии queued (jobs.Store). Запрос и бюджет
// извлекаются из JSON-закодированного Payload; для не-JSON Payload берётся его
// текст, а бюджет считается не заданным. Повторный идентификатор — ошибка.
func (s *SearchJobStore) Create(_ context.Context, job jobs.Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.jobs[job.ID]; exists {
		return errors.New("memory: задание уже существует")
	}
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
	createdAt := job.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	s.jobs[job.ID] = &searchJobRecord{
		id:              job.ID,
		query:           query,
		status:          jobs.StatusQueued,
		resultRef:       []string{},
		errText:         job.LastError,
		errCode:         job.ErrorCode,
		maxCredits:      maxCredits,
		targetCompanies: targetCompanies,
		maxResults:      maxResults,
		plan:            plan,
		interpretation:  interpretation,
		attempts:        job.Attempts,
		createdAt:       createdAt,
		updatedAt:       createdAt,
	}
	s.recordEventLocked(ports.SearchJobEvent{JobID: job.ID, Type: ports.EventEnqueued, CreatedAt: createdAt})
	return nil
}

// TakeQueued берёт старейшее queued-задание и переводит его в running, увеличив
// счётчик попыток. Пустая очередь даёт jobs.ErrNoJob.
func (s *SearchJobStore) TakeQueued(_ context.Context) (*jobs.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var chosen *searchJobRecord
	for _, record := range s.jobs {
		if record.status != jobs.StatusQueued {
			continue
		}
		if chosen == nil || record.createdAt.Before(chosen.createdAt) ||
			(record.createdAt.Equal(chosen.createdAt) && record.id < chosen.id) {
			chosen = record
		}
	}
	if chosen == nil {
		return nil, jobs.ErrNoJob
	}

	chosen.status = jobs.StatusRunning
	chosen.attempts++
	chosen.updatedAt = time.Now().UTC()
	s.recordEventLocked(ports.SearchJobEvent{JobID: chosen.id, Type: ports.EventTaken, CreatedAt: chosen.updatedAt})
	return recordToJob(*chosen), nil
}

// UpdateStatus переводит задание из состояния from в состояние to (jobs.Store).
// Переход применяется только если текущее состояние равно from; иначе —
// jobs.ErrTransitionRejected, и состояние не меняется (терминальные состояния
// неизменяемы). reason фиксирует причину перехода в failed, code — машинный код
// отказа; для прочих состояний оба игнорируются (ошибка сбрасывается).
func (s *SearchJobStore) UpdateStatus(_ context.Context, id string, from, to jobs.Status, reason string, code string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.jobs[id]
	if !ok {
		return fmt.Errorf("memory: %w: %s", jobs.ErrNotFound, id)
	}
	if record.status != from {
		return fmt.Errorf("memory: %w: задание %s в состоянии %q, ожидалось %q", jobs.ErrTransitionRejected, id, record.status, from)
	}
	record.status = to
	if to == jobs.StatusFailed {
		record.errText = reason
		record.errCode = code
	} else {
		record.errText = ""
		record.errCode = ""
	}
	record.updatedAt = time.Now().UTC()
	if t := eventTypeForStatus(to); t != "" {
		s.recordEventLocked(ports.SearchJobEvent{JobID: id, Type: t, Code: code, CreatedAt: record.updatedAt})
	}
	return nil
}

// FindByID возвращает задание по идентификатору (jobs.Store). Отсутствующее
// задание — jobs.ErrNotFound. Чтение нужно для распознавания отмены по данным.
func (s *SearchJobStore) FindByID(_ context.Context, id string) (*jobs.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.jobs[id]
	if !ok {
		return nil, fmt.Errorf("memory: %w: %s", jobs.ErrNotFound, id)
	}
	return recordToJob(*record), nil
}

// RequestCancel фиксирует запрос отмены задания: queued и running переходят в
// cancelled (выполняющееся задание дополнительно получает признак запроса
// отмены, чтобы живой обработчик узнал об отмене), терминальные состояния не
// меняются. Отсутствующее задание — ошибка.
func (s *SearchJobStore) RequestCancel(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.jobs[id]
	if !ok {
		return fmt.Errorf("memory: %w: %s", jobs.ErrNotFound, id)
	}
	switch record.status {
	case jobs.StatusQueued:
		record.status = jobs.StatusCancelled
	case jobs.StatusRunning:
		record.status = jobs.StatusCancelled
		record.cancelRequested = true
	default:
		return nil
	}
	record.updatedAt = time.Now().UTC()
	if record.cancelRequested {
		s.recordEventLocked(ports.SearchJobEvent{JobID: id, Type: ports.EventCancelRequested, CreatedAt: record.updatedAt})
	}
	s.recordEventLocked(ports.SearchJobEvent{JobID: id, Type: ports.EventCancelled, CreatedAt: record.updatedAt})
	return nil
}

// ReconcileRunning согласует задания, оставшиеся в running без живого
// обработчика (jobs.Store). Задание с признаком отмены переводится в cancelled;
// с исчерпанным лимитом попыток — в failed с кодом и формулировкой; прочее
// возвращается в queued. Согласуются только задания с активностью старше
// staleBefore. Возвращает счётчики исходов.
func (s *SearchJobStore) ReconcileRunning(_ context.Context, staleBefore time.Time, maxAttempts int, failCode, failReason string) (jobs.ReconcileResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result jobs.ReconcileResult
	for id, record := range s.jobs {
		if record.status != jobs.StatusRunning {
			continue
		}
		if record.updatedAt.After(staleBefore) {
			continue
		}
		record.updatedAt = time.Now().UTC()
		switch {
		case record.cancelRequested:
			record.status = jobs.StatusCancelled
			s.recordEventLocked(ports.SearchJobEvent{JobID: id, Type: ports.EventCancelled, CreatedAt: record.updatedAt})
			result.Cancelled++
		case record.attempts >= maxAttempts:
			record.status = jobs.StatusFailed
			record.errText = failReason
			record.errCode = failCode
			s.recordEventLocked(ports.SearchJobEvent{JobID: id, Type: ports.EventFailed, Code: failCode, CreatedAt: record.updatedAt})
			result.Failed++
		default:
			record.status = jobs.StatusQueued
			result.Requeued++
		}
	}
	return result, nil
}

// CreateSearchJob ставит задание поиска с запросом и бюджетом и возвращает его
// витринную запись. Идентификатор генерируется адаптером (случайная hex-строка).
func (s *SearchJobStore) CreateSearchJob(ctx context.Context, query string, maxCredits int) (ports.SearchJob, error) {
	id, err := newJobID()
	if err != nil {
		return ports.SearchJob{}, err
	}
	payload, err := ports.EncodeSearchJobPayload(ports.SearchJobPayload{Query: query, MaxCredits: maxCredits})
	if err != nil {
		return ports.SearchJob{}, fmt.Errorf("memory: кодирование запроса: %w", err)
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
// бюджетом кредитов. План хранится в записи и переживает взятие из очереди.
func (s *SearchJobStore) CreateSearchJobWithPlan(ctx context.Context, query string, plan ports.SearchPlan, maxCredits, targetCompanies, maxResults int) (ports.SearchJob, error) {
	id, err := newJobID()
	if err != nil {
		return ports.SearchJob{}, err
	}
	payload, err := ports.EncodeSearchJobPayload(ports.SearchJobPayload{Query: query, MaxCredits: maxCredits, TargetCompanies: targetCompanies, MaxResults: maxResults, Plan: &plan})
	if err != nil {
		return ports.SearchJob{}, fmt.Errorf("memory: кодирование запроса: %w", err)
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

// RecordBudget фиксирует снимок бюджета задачи, не меняя статус.
func (s *SearchJobStore) RecordBudget(_ context.Context, id string, b budget.Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.jobs[id]
	if !ok {
		return fmt.Errorf("memory: задание %s не найдено", id)
	}
	copy := b
	record.budget = &copy
	record.updatedAt = time.Now().UTC()
	return nil
}

// RecordProgress фиксирует прогресс выполнения задания, не меняя статус.
func (s *SearchJobStore) RecordProgress(_ context.Context, id string, progress ports.SearchJobProgress) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.jobs[id]
	if !ok {
		return fmt.Errorf("memory: задание %s не найдено", id)
	}
	copy := progress
	record.progress = &copy
	record.updatedAt = time.Now().UTC()
	return nil
}

// RecordResult фиксирует ссылку на найденные компании, не меняя статус.
func (s *SearchJobStore) RecordResult(_ context.Context, id string, resultRef []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.jobs[id]
	if !ok {
		return fmt.Errorf("memory: задание %s не найдено", id)
	}
	record.resultRef = append([]string(nil), resultRef...)
	record.updatedAt = time.Now().UTC()
	return nil
}

// SetInterpretation фиксирует критерии релевантности задания отдельным полем, не
// меняя статус. nil сохраняется как «интерпретация не задана».
func (s *SearchJobStore) SetInterpretation(_ context.Context, id string, interpretation *llm.Interpretation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.jobs[id]
	if !ok {
		return fmt.Errorf("memory: задание %s не найдено", id)
	}
	record.interpretation = cloneInterpretation(interpretation)
	record.updatedAt = time.Now().UTC()
	return nil
}

// cloneInterpretation копирует интерпретацию по значению (срез-поля тоже
// копируются), чтобы хранилище не разделяло мутабельное состояние с вызывающим.
func cloneInterpretation(i *llm.Interpretation) *llm.Interpretation {
	if i == nil {
		return nil
	}
	copy := *i
	copy.NeedSignals = append([]string(nil), i.NeedSignals...)
	copy.Exclusions = append([]string(nil), i.Exclusions...)
	copy.HypothesisSignals = append([]string(nil), i.HypothesisSignals...)
	return &copy
}

// RecordEvent добавляет событие жизненного цикла задания (append-only).
func (s *SearchJobStore) RecordEvent(_ context.Context, event ports.SearchJobEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recordEventLocked(event)
	return nil
}

// ListEvents возвращает события задания в порядке записи.
func (s *SearchJobStore) ListEvents(_ context.Context, jobID string) ([]ports.SearchJobEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ports.SearchJobEvent, 0)
	for _, event := range s.events {
		if event.JobID == jobID {
			out = append(out, event)
		}
	}
	return out, nil
}

// ListSearchJobs возвращает журнал заданий, новые первыми, не более limit штук.
func (s *SearchJobStore) ListSearchJobs(_ context.Context, limit int) ([]ports.SearchJob, error) {
	if limit <= 0 {
		return []ports.SearchJob{}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	records := make([]*searchJobRecord, 0, len(s.jobs))
	for _, record := range s.jobs {
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].createdAt.Equal(records[j].createdAt) {
			return records[i].id > records[j].id
		}
		return records[i].createdAt.After(records[j].createdAt)
	})
	if len(records) > limit {
		records = records[:limit]
	}

	// Позиция в очереди: queued-записи упорядочиваются по возрастанию createdAt/id.
	var queued []*searchJobRecord
	for _, record := range s.jobs {
		if record.status == jobs.StatusQueued {
			queued = append(queued, record)
		}
	}
	sort.Slice(queued, func(i, j int) bool {
		if queued[i].createdAt.Equal(queued[j].createdAt) {
			return queued[i].id < queued[j].id
		}
		return queued[i].createdAt.Before(queued[j].createdAt)
	})
	position := make(map[string]int, len(queued))
	for i, record := range queued {
		position[record.id] = i + 1
	}

	list := make([]ports.SearchJob, 0, len(records))
	for _, record := range records {
		job := recordToSearchJob(*record)
		job.QueuePosition = position[record.id]
		list = append(list, job)
	}
	return list, nil
}

// recordToJob собирает jobs.Job из хранимого состояния, закодировав запрос,
// бюджет и план в Payload.
func recordToJob(record searchJobRecord) *jobs.Job {
	payload, _ := ports.EncodeSearchJobPayload(ports.SearchJobPayload{Query: record.query, MaxCredits: record.maxCredits, TargetCompanies: record.targetCompanies, MaxResults: record.maxResults, Plan: record.plan, Interpretation: record.interpretation})
	return &jobs.Job{
		ID:              record.id,
		Status:          record.status,
		Payload:         payload,
		Attempts:        record.attempts,
		LastError:       record.errText,
		ErrorCode:       record.errCode,
		CancelRequested: record.cancelRequested,
		CreatedAt:       record.createdAt,
		UpdatedAt:       record.updatedAt,
	}
}

// recordToSearchJob собирает витринную запись из хранимого состояния.
func recordToSearchJob(record searchJobRecord) ports.SearchJob {
	var progress *ports.SearchJobProgress
	if record.progress != nil {
		copy := *record.progress
		progress = &copy
	}
	return ports.SearchJob{
		ID:             record.id,
		Query:          record.query,
		Status:         record.status,
		ResultRef:      append([]string(nil), record.resultRef...),
		Error:          record.errText,
		ErrorCode:      record.errCode,
		Budget:         record.budget,
		MaxCredits:     record.maxCredits,
		Interpretation: record.interpretation,
		Progress:       progress,
		CreatedAt:      record.createdAt,
		UpdatedAt:      record.updatedAt,
	}
}

// newJobID генерирует случайный идентификатор задания (32 hex-символа).
func newJobID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("memory: генерация идентификатора: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
