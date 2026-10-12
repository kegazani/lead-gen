package jobs

import (
	"context"
	"errors"
)

// ErrUnsupported — сигнал «хранилище не поддерживает операцию». Его возвращают
// удобные функции примитива, когда Store не реализует нужную возможность
// (ProgressStore, JobLister, JobRetrier). Это не сбой хранилища и не заглушка:
// вызывающий обязан честно отразить отсутствие возможности (например, ответить
// 503), а не выдумывать результат.
var ErrUnsupported = errors.New("jobs: операция не поддерживается хранилищем")

// ProgressMax — верхняя граница доли выполнения, в процентах.
const ProgressMax = 100

// ClampProgress приводит значение к диапазону [0, ProgressMax].
func ClampProgress(progress int) int {
	switch {
	case progress < 0:
		return 0
	case progress > ProgressMax:
		return ProgressMax
	default:
		return progress
	}
}

// ProgressStore — расширение Store: обновление прогресса выполняющегося задания.
// Реализуется хранилищем контекста, чьи длительные операции сообщают о ходе
// работы (импорт, обход, выгрузка, построение отчёта), и не входит в базовый
// контракт Store, поэтому существующие хранилища остаются совместимыми.
type ProgressStore interface {
	// UpdateProgress фиксирует долю выполнения (0..100) и текущий шаг задания.
	// Значение вне диапазона хранилище приводит к границам (ClampProgress).
	// Задание вне состояния running даёт ErrTransitionRejected; отсутствующее —
	// ErrNotFound.
	UpdateProgress(ctx context.Context, id string, progress int, step string) error
}

// JobFilter — отбор заданий при чтении списка. Нулевое значение отбирает все
// задания. Отбор нужен для видимости очереди и заданий, исчерпавших попытки
// (DLQ), администратором.
type JobFilter struct {
	// Statuses — допустимые состояния. Пустой список — любое состояние.
	Statuses []Status
	// MinAttempts — минимальное число попыток: задание проходит отбор при
	// Attempts >= MinAttempts. Неположительное значение — без ограничения.
	MinAttempts int
	// Limit — предел числа строк. Неположительное значение — без ограничения.
	Limit int
}

// Match сообщает, попадает ли задание под отбор. Limit в отборе не участвует:
// его применяет хранилище к уже упорядоченному списку.
func (f JobFilter) Match(job Job) bool {
	if f.MinAttempts > 0 && job.Attempts < f.MinAttempts {
		return false
	}
	if len(f.Statuses) == 0 {
		return true
	}
	for _, s := range f.Statuses {
		if job.Status == s {
			return true
		}
	}
	return false
}

// JobLister — расширение Store: чтение списка заданий. Чтение (и список) нужны
// модели «команда → идентификатор → прогресс → результат» и видимости DLQ;
// базовый контракт Store их не содержит, поэтому расширение необязательно.
type JobLister interface {
	// ListJobs возвращает задания по отбору в стабильном порядке: по убыванию
	// UpdatedAt, при равенстве — по возрастанию ID. Стабильный порядок обязателен,
	// чтобы страница DLQ не «прыгала» между запросами. Limit<=0 — без ограничения.
	ListJobs(ctx context.Context, filter JobFilter) ([]Job, error)
}

// JobRetrier — расширение Store: явный повтор упавшего задания. Повтор —
// отдельное действие оператора, а не автоматическая политика: администратор
// осознанно возвращает задание из DLQ в работу.
type JobRetrier interface {
	// RetryFailed возвращает failed-задание в очередь с новым бюджетом попыток:
	// Attempts=0, причина и код отказа очищаются, прогресс, шаг и признак отмены
	// сбрасываются, UpdatedAt обновляется. Задание с иным состоянием даёт
	// ErrTransitionRejected; отсутствующее — ErrNotFound.
	RetryFailed(ctx context.Context, id string) error
}

// DLQFilter собирает отбор заданий, исчерпавших попытки: состояние failed и
// число попыток не меньше maxAttempts. maxAttempts < 1 приводится к 1.
func DLQFilter(maxAttempts, limit int) JobFilter {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	return JobFilter{
		Statuses:    []Status{StatusFailed},
		MinAttempts: maxAttempts,
		Limit:       limit,
	}
}

// ReportProgress сообщает прогресс задания через хранилище. Если Store не
// реализует ProgressStore, возвращает ErrUnsupported.
func ReportProgress(ctx context.Context, store Store, id string, progress int, step string) error {
	reporter, ok := store.(ProgressStore)
	if !ok {
		return ErrUnsupported
	}
	return reporter.UpdateProgress(normalizeCtx(ctx), id, ClampProgress(progress), step)
}

// ListDLQ возвращает задания, исчерпавшие попытки, в стабильном порядке. Если
// Store не реализует JobLister, возвращает ErrUnsupported.
func ListDLQ(ctx context.Context, store Store, maxAttempts, limit int) ([]Job, error) {
	lister, ok := store.(JobLister)
	if !ok {
		return nil, ErrUnsupported
	}
	return lister.ListJobs(normalizeCtx(ctx), DLQFilter(maxAttempts, limit))
}

// RetryJob инициирует повтор упавшего задания. Если Store не реализует
// JobRetrier, возвращает ErrUnsupported.
func RetryJob(ctx context.Context, store Store, id string) error {
	retrier, ok := store.(JobRetrier)
	if !ok {
		return ErrUnsupported
	}
	return retrier.RetryFailed(normalizeCtx(ctx), id)
}

// normalizeCtx подставляет фоновый контекст вместо nil, чтобы вызов хранилища не
// паниковал на nil-контексте.
func normalizeCtx(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
