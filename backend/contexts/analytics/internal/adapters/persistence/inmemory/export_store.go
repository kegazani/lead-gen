// Package inmemory реализует хранилища сервиса analytics поверх оперативной
// памяти.
package inmemory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"

	"lead-gen/backend/contexts/analytics/internal/ports"
	"lead-gen/backend/platform/jobs"
)

// exportRecord — хранимое состояние задания выгрузки. Запрос хранится в
// непрозрачном Payload, а готовый файл — отдельным полем, чтобы примитив jobs
// оставался непрозрачным к содержимому.
type exportRecord struct {
	id              string
	payload         []byte
	status          jobs.Status
	errText         string
	errCode         string
	attempts        int
	progress        int
	step            string
	cancelRequested bool
	result          *ports.ReportExportResult
	startedAt       time.Time
	createdAt       time.Time
	updatedAt       time.Time
}

// ReportExportStore — in-memory реализация ports.ReportExportStore. Удовлетворяет
// и примитиву jobs.Store (раннер worker), и операциям длительной выгрузки.
type ReportExportStore struct {
	mu      sync.Mutex
	records map[string]*exportRecord
}

// ReportExportStore реализует порт и примитив.
var (
	_ ports.ReportExportStore = (*ReportExportStore)(nil)
	_ jobs.Store              = (*ReportExportStore)(nil)
)

// NewReportExportStore создаёт пустое хранилище заданий выгрузки.
func NewReportExportStore() *ReportExportStore {
	return &ReportExportStore{records: make(map[string]*exportRecord)}
}

// Create сохраняет новое задание в состоянии queued (jobs.Store). Повторный
// идентификатор — ошибка.
func (s *ReportExportStore) Create(_ context.Context, job jobs.Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.records[job.ID]; exists {
		return fmt.Errorf("memory: задание выгрузки %s уже существует", job.ID)
	}
	createdAt := job.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	s.records[job.ID] = &exportRecord{
		id:        job.ID,
		payload:   append([]byte(nil), job.Payload...),
		status:    jobs.StatusQueued,
		errText:   job.LastError,
		errCode:   job.ErrorCode,
		attempts:  job.Attempts,
		createdAt: createdAt,
		updatedAt: createdAt,
	}
	return nil
}

// TakeQueued берёт старейшее queued-задание и переводит его в running, увеличив
// счётчик попыток. Пустая очередь даёт jobs.ErrNoJob.
func (s *ReportExportStore) TakeQueued(_ context.Context) (*jobs.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var chosen *exportRecord
	for _, record := range s.records {
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
	chosen.startedAt = time.Now().UTC()
	chosen.updatedAt = chosen.startedAt
	return recordToJob(*chosen), nil
}

// UpdateStatus переводит задание из состояния from в состояние to (jobs.Store).
// Переход применяется только если текущее состояние равно from; иначе —
// jobs.ErrTransitionRejected. reason и code фиксируются только для failed.
func (s *ReportExportStore) UpdateStatus(_ context.Context, id string, from, to jobs.Status, reason, code string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[id]
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
	if to == jobs.StatusDone {
		record.progress = jobs.ProgressMax
	}
	record.updatedAt = time.Now().UTC()
	return nil
}

// UpdateProgress фиксирует долю выполнения и текущий шаг (jobs.ProgressStore).
// Значение вне диапазона приводится к границам; обновляется только running-задание,
// иначе — jobs.ErrTransitionRejected, отсутствующее — jobs.ErrNotFound.
func (s *ReportExportStore) UpdateProgress(_ context.Context, id string, progress int, step string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[id]
	if !ok {
		return fmt.Errorf("memory: %w: %s", jobs.ErrNotFound, id)
	}
	if record.status != jobs.StatusRunning {
		return fmt.Errorf("memory: %w: задание %s в состоянии %q, ожидалось %q", jobs.ErrTransitionRejected, id, record.status, jobs.StatusRunning)
	}
	record.progress = jobs.ClampProgress(progress)
	record.step = step
	record.updatedAt = time.Now().UTC()
	return nil
}

// ListJobs возвращает задания по отбору в стабильном порядке: по убыванию
// UpdatedAt, при равенстве — по возрастанию ID (jobs.JobLister). Limit применяется
// к уже упорядоченному списку.
func (s *ReportExportStore) ListJobs(_ context.Context, filter jobs.JobFilter) ([]jobs.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]jobs.Job, 0, len(s.records))
	for _, record := range s.records {
		job := *recordToJob(*record)
		if !filter.Match(job) {
			continue
		}
		out = append(out, job)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		}
		return out[i].ID < out[j].ID
	})
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

// RetryFailed возвращает failed-задание в очередь с новым бюджетом попыток
// (jobs.JobRetrier): счётчик попыток и причина сбрасываются, прогресс, шаг и
// признак отмены очищаются. Задание с иным состоянием — jobs.ErrTransitionRejected,
// отсутствующее — jobs.ErrNotFound.
func (s *ReportExportStore) RetryFailed(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[id]
	if !ok {
		return fmt.Errorf("memory: %w: %s", jobs.ErrNotFound, id)
	}
	if record.status != jobs.StatusFailed {
		return fmt.Errorf("memory: %w: задание %s в состоянии %q, ожидалось %q", jobs.ErrTransitionRejected, id, record.status, jobs.StatusFailed)
	}
	record.status = jobs.StatusQueued
	record.attempts = 0
	record.errText = ""
	record.errCode = ""
	record.progress = 0
	record.step = ""
	record.cancelRequested = false
	record.startedAt = time.Time{}
	record.updatedAt = time.Now().UTC()
	return nil
}

// FindByID возвращает задание по идентификатору (jobs.Store). Отсутствующее
// задание — jobs.ErrNotFound.
func (s *ReportExportStore) FindByID(_ context.Context, id string) (*jobs.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[id]
	if !ok {
		return nil, fmt.Errorf("memory: %w: %s", jobs.ErrNotFound, id)
	}
	return recordToJob(*record), nil
}

// RequestCancel фиксирует запрос отмены задания: queued и running переходят в
// cancelled (выполняющееся задание дополнительно получает признак запроса отмены),
// терминальные состояния не меняются.
func (s *ReportExportStore) RequestCancel(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[id]
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
	return nil
}

// ReconcileRunning согласует задания, оставшиеся в running без живого обработчика
// (jobs.Store): с признаком отмены — в cancelled, с исчерпанным лимитом попыток —
// в failed, прочие — в queued. Согласуются только задания старше staleBefore.
func (s *ReportExportStore) ReconcileRunning(_ context.Context, staleBefore time.Time, maxAttempts int, failCode, failReason string) (jobs.ReconcileResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result jobs.ReconcileResult
	for _, record := range s.records {
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
			result.Cancelled++
		case record.attempts >= maxAttempts:
			record.status = jobs.StatusFailed
			record.errText = failReason
			record.errCode = failCode
			result.Failed++
		default:
			record.status = jobs.StatusQueued
			result.Requeued++
		}
	}
	return result, nil
}

// CreateReportExport ставит задание выгрузки с зафиксированным запросом и
// возвращает его операционную запись. Идентификатор генерирует адаптер.
func (s *ReportExportStore) CreateReportExport(ctx context.Context, req ports.ReportExportRequest) (ports.ReportExport, error) {
	id, err := newExportJobID()
	if err != nil {
		return ports.ReportExport{}, err
	}
	payload, err := ports.EncodeReportExportPayload(ports.ReportExportPayloadFromRequest(req))
	if err != nil {
		return ports.ReportExport{}, fmt.Errorf("memory: кодирование запроса выгрузки: %w", err)
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
func (s *ReportExportStore) RecordExportResult(_ context.Context, id string, result ports.ReportExportResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[id]
	if !ok {
		return fmt.Errorf("memory: %w: %s", ports.ErrReportExportNotFound, id)
	}
	copy := ports.ReportExportResult{
		ContentType: result.ContentType,
		Filename:    result.Filename,
		Body:        append([]byte(nil), result.Body...),
	}
	record.result = &copy
	record.updatedAt = time.Now().UTC()
	return nil
}

// ExportByID возвращает операционную запись задания. Отсутствующее —
// ports.ErrReportExportNotFound.
func (s *ReportExportStore) ExportByID(_ context.Context, id string) (ports.ReportExport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[id]
	if !ok {
		return ports.ReportExport{}, fmt.Errorf("memory: %w: %s", ports.ErrReportExportNotFound, id)
	}
	return recordToExport(*record), nil
}

// ExportResult возвращает готовый файл задания. Отсутствующее задание —
// ports.ErrReportExportNotFound, несформированный результат —
// ports.ErrReportExportNoResult.
func (s *ReportExportStore) ExportResult(_ context.Context, id string) (ports.ReportExportResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[id]
	if !ok {
		return ports.ReportExportResult{}, fmt.Errorf("memory: %w: %s", ports.ErrReportExportNotFound, id)
	}
	if record.result == nil {
		return ports.ReportExportResult{}, fmt.Errorf("memory: %w: %s", ports.ErrReportExportNoResult, id)
	}
	return ports.ReportExportResult{
		ContentType: record.result.ContentType,
		Filename:    record.result.Filename,
		Body:        append([]byte(nil), record.result.Body...),
	}, nil
}

// recordToJob собирает jobs.Job из хранимого состояния.
func recordToJob(record exportRecord) *jobs.Job {
	return &jobs.Job{
		ID:              record.id,
		Status:          record.status,
		Payload:         append([]byte(nil), record.payload...),
		Attempts:        record.attempts,
		LastError:       record.errText,
		ErrorCode:       record.errCode,
		Progress:        record.progress,
		Step:            record.step,
		StartedAt:       record.startedAt,
		CancelRequested: record.cancelRequested,
		CreatedAt:       record.createdAt,
		UpdatedAt:       record.updatedAt,
	}
}

// recordToExport собирает операционную запись из хранимого состояния.
func recordToExport(record exportRecord) ports.ReportExport {
	format := ports.ExportFormat("")
	if payload, err := ports.DecodeReportExportPayload(record.payload); err == nil {
		format = payload.Format
	}
	return ports.ReportExport{
		ID:              record.id,
		Format:          format,
		Status:          record.status,
		Error:           record.errText,
		ErrorCode:       record.errCode,
		ResultAvailable: record.result != nil,
		Progress:        record.progress,
		Step:            record.step,
		StartedAt:       record.startedAt,
		CreatedAt:       record.createdAt,
		UpdatedAt:       record.updatedAt,
	}
}

// newExportJobID генерирует случайный идентификатор задания (32 hex-символа).
func newExportJobID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("memory: генерация идентификатора: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
