package query

import (
	"context"

	"lead-gen/backend/platform/jobs"
)

// JobsHandler — чтение очереди заданий, исчерпавших попытки (DLQ), и явный повтор
// упавшего задания. Это тонкая проекция примитива platform/jobs: правила отбора и
// сброса попыток живут в примитиве, здесь — только доступ к хранилищу своего
// контекста. Технические детали в записи не раскрываются: причина — нормализованная
// формулировка, которую положил раннер.
type JobsHandler struct {
	store jobs.Store
}

// NewJobsHandler собирает use case чтения заданий поверх хранилища.
func NewJobsHandler(store jobs.Store) *JobsHandler {
	return &JobsHandler{store: store}
}

// DLQ возвращает задания, исчерпавшие попытки, в стабильном порядке. Хранилище без
// поддержки списка даёт jobs.ErrUnsupported — вызывающий обязан честно отразить
// отсутствие возможности, а не выдумывать пустой список.
func (h *JobsHandler) DLQ(ctx context.Context, maxAttempts, limit int) ([]jobs.Job, error) {
	if h == nil || h.store == nil {
		return nil, jobs.ErrUnsupported
	}
	return jobs.ListDLQ(ctx, h.store, maxAttempts, limit)
}

// Retry инициирует повтор упавшего задания. Отсутствующее задание —
// jobs.ErrNotFound, задание не в состоянии отказа — jobs.ErrTransitionRejected,
// хранилище без поддержки повтора — jobs.ErrUnsupported.
func (h *JobsHandler) Retry(ctx context.Context, id string) error {
	if h == nil || h.store == nil {
		return jobs.ErrUnsupported
	}
	return jobs.RetryJob(ctx, h.store, id)
}
