// Package jobs — примитив единого механизма заданий без доменных терминов:
// состояние задания, хранилище и раннер с интервалом опроса.
//
// Примитив не знает о контекстах и контрактах: он задаёт жизненный цикл задания
// (queued → running → done/failed/cancelled), а обработчик, зарегистрированный
// прикладным слоем, разбирает непрозрачный Payload. Это требование готовности к
// следующему срезу: механизм переиспользует рассылка, поэтому он обязан жить в
// backend/platform, а не внутри конкретного контекста.
package jobs

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// Status — состояние задания.
type Status string

const (
	// StatusQueued — задание поставлено и ждёт обработчика.
	StatusQueued Status = "queued"
	// StatusRunning — задание взято обработчиком.
	StatusRunning Status = "running"
	// StatusDone — задание завершилось успешно.
	StatusDone Status = "done"
	// StatusFailed — задание завершилось ошибкой.
	StatusFailed Status = "failed"
	// StatusCancelled — задание отменено оператором. Терминальное состояние, не
	// являющееся ошибкой: отмена не несёт причины отказа.
	StatusCancelled Status = "cancelled"
)

// Valid сообщает, является ли значение известным состоянием.
func (s Status) Valid() bool {
	switch s {
	case StatusQueued, StatusRunning, StatusDone, StatusFailed, StatusCancelled:
		return true
	default:
		return false
	}
}

// Terminal сообщает, является ли состояние терминальным (задание больше не будет
// взято в обработку).
func (s Status) Terminal() bool {
	switch s {
	case StatusDone, StatusFailed, StatusCancelled:
		return true
	default:
		return false
	}
}

// ErrNoJob — сигнал «в очереди нет заданий». Отличает пустую очередь от отказа
// хранилища, поэтому вызывающий не путает штатное бездействие с ошибкой.
var ErrNoJob = errors.New("jobs: нет заданий в очереди")

// ErrCancelled — сигнал «задание остановлено отменой». Обработчик возвращает его,
// когда прекратил работу из-за отменённого контекста задания; раннер отличает
// отмену от ошибки и переводит задание в StatusCancelled. Остановка по отмене
// контекста процесса (остановка сервиса) отменой задания не считается.
var ErrCancelled = errors.New("jobs: задание отменено")

// ErrNotFound — сигнал «задания с таким идентификатором нет». Отличает отсутствие
// записи от отказа хранилища, чтобы вызывающий не путал их.
var ErrNotFound = errors.New("jobs: задание не найдено")

// ErrTransitionRejected — сигнал «переход состояния отклонён». Хранилище
// возвращает его, когда текущее состояние задания не равно исходному состоянию
// перехода (например, задание уже терминально). Это не сбой хранилища: отклонение
// защищает терминальные состояния от перезаписи поздним завершением.
var ErrTransitionRejected = errors.New("jobs: переход состояния отклонён")

// CodedError — ошибка задания, несущая стабильный машинный код и сообщение для
// оператора. Примитив различает код и сообщение, но не знает их значений: коды
// определяет прикладной слой. Обычная ошибка без этого интерфейса даёт пустой код
// и свой текст как сообщение.
type CodedError interface {
	error
	// Code — стабильный машинный код исхода.
	Code() string
	// Message — сообщение для оператора (бизнес-формулировка).
	Message() string
}

// Job — задание. Payload непрозрачен для примитива: его разбирает обработчик.
type Job struct {
	// ID — идентификатор задания.
	ID string
	// Status — текущее состояние.
	Status Status
	// Payload — непрозрачные данные задания.
	Payload []byte
	// Attempts — сколько раз задание было взято из очереди.
	Attempts int
	// LastError — причина последней ошибки (для failed).
	LastError string
	// ErrorCode — стабильный машинный код отказа (для failed). Пуст для ошибок без
	// кода и для прочих состояний.
	ErrorCode string
	// CancelRequested — для выполняющегося задания: запрошена ли его отмена.
	// Задание в очереди при запросе отмены сразу переходит в cancelled, поэтому
	// для него флаг не выставляется.
	CancelRequested bool
	// CreatedAt — момент постановки.
	CreatedAt time.Time
	// UpdatedAt — момент последнего изменения состояния.
	UpdatedAt time.Time
}

// Store — хранилище заданий. Реализуется прикладным слоем (pgx, in-memory);
// интерфейс не содержит доменных терминов.
type Store interface {
	// Create сохраняет новое задание в состоянии queued.
	Create(ctx context.Context, job Job) error
	// TakeQueued атомарно берёт одно queued-задание и переводит его в running.
	// Возвращает ErrNoJob, если очередь пуста.
	TakeQueued(ctx context.Context) (*Job, error)
	// FindByID возвращает задание по идентификатору. Отсутствующее задание —
	// ErrNotFound. Чтение нужно для распознавания отмены по данным: обработчик
	// сверяет состояние задания из хранилища на границах шага, не полагаясь
	// только на сигнал в процессе.
	FindByID(ctx context.Context, id string) (*Job, error)
	// UpdateStatus переводит задание из состояния from в состояние to. Переход
	// применяется только если текущее состояние задания равно from; иначе
	// возвращается ErrTransitionRejected, и состояние не меняется (терминальные
	// состояния неизменяемы). reason фиксирует причину перехода в failed, code —
	// стабильный машинный код отказа; для прочих состояний оба игнорируются.
	UpdateStatus(ctx context.Context, id string, from, to Status, reason string, code string) error
	// RequestCancel фиксирует запрос отмены задания. Задание в очереди
	// (queued) переводится в cancelled; выполняющееся (running) переводится в
	// cancelled и получает признак запроса отмены, чтобы живой обработчик узнал
	// об отмене; терминальные состояния остаются без изменений (операция
	// идемпотентна). Отсутствующее задание — ошибка.
	RequestCancel(ctx context.Context, id string) error
	// ReconcileRunning согласует задания, оставшиеся в running без живого
	// обработчика — после перезапуска процесса или сбоя. Задание с признаком
	// запроса отмены переводится в cancelled. Прочие: с исчерпанным лимитом
	// попыток (Attempts >= maxAttempts) — в failed с кодом failCode и
	// формулировкой failReason (значения определяет прикладной слой, примитив их не
	// знает); иначе возвращаются в queued. Согласуются только задания с активностью
	// (UpdatedAt) старше staleBefore: это не даёт отобрать задание у живой копии
	// обработчика. Возвращает счётчики исходов.
	ReconcileRunning(ctx context.Context, staleBefore time.Time, maxAttempts int, failCode, failReason string) (ReconcileResult, error)
}

// ReconcileResult — счётчики исходов согласования осиротевших заданий: сколько
// возвращено в очередь, отменено и завершено отказом.
type ReconcileResult struct {
	// Requeued — возвращено в очередь.
	Requeued int
	// Cancelled — отменено (по запросу отмены).
	Cancelled int
	// Failed — завершено отказом (исчерпаны попытки).
	Failed int
}

// Total возвращает общее число согласованных заданий.
func (r ReconcileResult) Total() int { return r.Requeued + r.Cancelled + r.Failed }

// Handler обрабатывает одно взятое задание. Возвращённая ошибка переводит
// задание в failed с её текстом; sentinel ErrCancelled переводит его в
// cancelled без текста ошибки. Обработчик обязан уважать отмену контекста задания
// и завершаться, сохраняя уже достигнутый результат.
type Handler func(ctx context.Context, job Job) error

// Runner — цикл обработки заданий с интервалом опроса.
type Runner struct {
	// Store — хранилище заданий.
	Store Store
	// Handler — обработчик задания.
	Handler Handler
	// Interval — интервал опроса очереди. Неположительное значение заменяется
	// на секунду.
	Interval time.Duration
	// Signals — реестр отмены выполняющихся заданий. nil — отмена через
	// сигнал недоступна; распознавание отмены по данным хранилища работает всё
	// равно (watchCancellation).
	Signals *CancelRegistry
	// Rejected, если задан, вызывается, когда завершение задания отклонено
	// охраной перехода (задание уже терминально). nil — исход игнорируется.
	// Сам примитив сообщения не пишет: наблюдаемость — дело прикладного слоя.
	Rejected func(jobID string, to Status)
	// LeaseTTL — срок аренды задания: осиротевшими считаются задания, активность
	// по которым не обновлялась дольше этого срока. Неположительное значение
	// заменяется значением по умолчанию.
	LeaseTTL time.Duration
	// MaxAttempts — предел попыток прерванного задания: при исчерпании оно
	// завершается отказом с кодом FailCode. Неположительное значение заменяется
	// значением по умолчанию.
	MaxAttempts int
	// FailCode и FailReason — код и формулировка отказа для прерванного задания,
	// исчерпавшего попытки. Значения определяет прикладной слой.
	FailCode   string
	FailReason string
}

// Значения по умолчанию для согласования осиротевших заданий.
const (
	// defaultLeaseTTL — срок аренды по умолчанию: заведомо больше интервала
	// обновления прогресса, чтобы живое задание не считалось осиротевшим.
	defaultLeaseTTL = 5 * time.Minute
	// defaultMaxAttempts — предел попыток по умолчанию: одна повторная попытка.
	defaultMaxAttempts = 2
)

// RunOnce обрабатывает одно задание из очереди, если оно есть, и возвращает
// число обработанных заданий (взятых и доведённых до терминального состояния).
// Пустая очередь — не ошибка. Ошибка обработчика не останавливает цикл: задание
// помечается failed, а раннер продолжает. Отмена задания переводит его в
// cancelled и не считается ошибкой.
func (r *Runner) RunOnce(ctx context.Context) (int, error) {
	job, err := r.Store.TakeQueued(ctx)
	if errors.Is(err, ErrNoJob) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	// На задание создаётся дочерний контекст, чтобы отмена прерывала именно это
	// задание, не задевая процесс. Признак запроса отмены хранится локально: он
	// отличает отмену задания от остановки процесса, при которой контекст тоже
	// отменяется, но это не отмена задания.
	jobCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var cancelRequested atomic.Bool
	markCancelled := func() {
		cancelRequested.Store(true)
		cancel()
	}
	if r.Signals != nil {
		r.Signals.Register(job.ID, markCancelled)
		defer r.Signals.Unregister(job.ID)
	}
	// Наблюдатель распознаёт отмену по данным хранилища, а не только по сигналу.
	// Это закрывает гонку «взято → сигнал зарегистрирован» и раздельные процессы.
	stopWatch := r.watchCancellation(jobCtx, job.ID, markCancelled)
	defer stopWatch()

	handlerErr := r.Handler(jobCtx, *job)

	status, reason, code := StatusDone, "", ""
	switch {
	case cancelRequested.Load():
		status = StatusCancelled
	case handlerErr != nil:
		status = StatusFailed
		reason = handlerErr.Error()
		if ce, ok := handlerErr.(CodedError); ok {
			reason = ce.Message()
			code = ce.Code()
		}
	}
	if err := r.finish(ctx, job.ID, status, reason, code); err != nil {
		// Отклонённый переход — не сбой: задание уже терминально (например,
		// отменено в хранилище раньше, чем обработчик успел завершиться).
		if errors.Is(err, ErrTransitionRejected) {
			if r.Rejected != nil {
				r.Rejected(job.ID, status)
			}
			return 1, nil
		}
		return 0, err
	}
	return 1, nil
}

// watchCancellation следит за состоянием задания в хранилище, пока обработчик
// работает, и отменяет контекст задания, когда оно отменено (статус cancelled)
// или когда запрошена его отмена. Возвращает функцию остановки наблюдателя;
// повторный вызов безопасен.
func (r *Runner) watchCancellation(ctx context.Context, id string, onCancel func()) func() {
	interval := r.Interval
	if interval <= 0 {
		interval = time.Second
	}
	stopCh := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stopCh:
				return
			case <-ticker.C:
				job, err := r.Store.FindByID(context.WithoutCancel(ctx), id)
				if err != nil || job == nil {
					continue
				}
				if job.Status == StatusCancelled || job.CancelRequested {
					onCancel()
					return
				}
				if job.Status.Terminal() {
					return
				}
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(stopCh) }) }
}

// Reconcile согласует задания, оставшиеся в running без живого обработчика, —
// после перезапуска процесса или сбоя. Вызывается при старте воркера, до первого
// взятия задания. Политика (срок аренды, лимит попыток, код отказа) берётся из
// раннера; незаданные значения заменяются значениями по умолчанию. Возвращает
// счётчики исходов.
func (r *Runner) Reconcile(ctx context.Context) (ReconcileResult, error) {
	lease := r.LeaseTTL
	if lease <= 0 {
		lease = defaultLeaseTTL
	}
	maxAttempts := r.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultMaxAttempts
	}
	return r.Store.ReconcileRunning(ctx, time.Now().Add(-lease), maxAttempts, r.FailCode, r.FailReason)
}

// finish переводит задание из running в терминальное состояние. Переход
// охраняется исходным состоянием: уже терминальное задание не перезаписывается.
func (r *Runner) finish(ctx context.Context, id string, status Status, reason, code string) error {
	return r.Store.UpdateStatus(ctx, id, StatusRunning, status, reason, code)
}

// Run обрабатывает задания в цикле до отмены контекста. Ошибка опроса
// хранилища останавливает цикл; ошибка обработчика — нет.
func (r *Runner) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	interval := r.Interval
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if _, err := r.RunOnce(ctx); err != nil {
				return err
			}
		}
	}
}
