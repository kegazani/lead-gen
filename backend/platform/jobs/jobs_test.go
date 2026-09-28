package jobs

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// memoryStore — in-memory реализация Store для тестов примитива. Она не является
// частью публичной поверхности пакета и нужна только чтобы `go test` прогонял
// переходы статусов и раннер без внешней инфраструктуры.
type memoryStore struct {
	mu   sync.Mutex
	jobs map[string]Job
}

func newMemoryStore() *memoryStore {
	return &memoryStore{jobs: make(map[string]Job)}
}

func (m *memoryStore) Create(_ context.Context, job Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.jobs[job.ID]; exists {
		return errors.New("задание уже существует")
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now()
	}
	job.Status = StatusQueued
	job.UpdatedAt = job.CreatedAt
	m.jobs[job.ID] = job
	return nil
}

func (m *memoryStore) TakeQueued(_ context.Context) (*Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, job := range m.jobs {
		if job.Status == StatusQueued {
			job.Status = StatusRunning
			job.Attempts++
			job.UpdatedAt = time.Now()
			m.jobs[id] = job
			return &job, nil
		}
	}
	return nil, ErrNoJob
}

func (m *memoryStore) FindByID(_ context.Context, id string) (*Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[id]
	if !ok {
		return nil, ErrNotFound
	}
	return &job, nil
}

func (m *memoryStore) UpdateStatus(_ context.Context, id string, from, to Status, reason string, code string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[id]
	if !ok {
		return ErrNotFound
	}
	if job.Status != from {
		return ErrTransitionRejected
	}
	job.Status = to
	job.LastError = reason
	job.ErrorCode = code
	job.UpdatedAt = time.Now()
	m.jobs[id] = job
	return nil
}

func (m *memoryStore) RequestCancel(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[id]
	if !ok {
		return ErrNotFound
	}
	switch job.Status {
	case StatusQueued:
		job.Status = StatusCancelled
	case StatusRunning:
		job.Status = StatusCancelled
		job.CancelRequested = true
	default:
		// Терминальное состояние — no-op.
		return nil
	}
	job.UpdatedAt = time.Now()
	m.jobs[id] = job
	return nil
}

func (m *memoryStore) ReconcileRunning(_ context.Context, staleBefore time.Time, maxAttempts int, failCode, failReason string) (ReconcileResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result ReconcileResult
	for id, job := range m.jobs {
		if job.Status != StatusRunning {
			continue
		}
		// Свежая активность означает живого обработчика: не трогаем.
		if job.UpdatedAt.After(staleBefore) {
			continue
		}
		switch {
		case job.CancelRequested:
			job.Status = StatusCancelled
			result.Cancelled++
		case job.Attempts >= maxAttempts:
			job.Status = StatusFailed
			job.LastError = failReason
			job.ErrorCode = failCode
			result.Failed++
		default:
			job.Status = StatusQueued
			result.Requeued++
		}
		job.UpdatedAt = time.Now()
		m.jobs[id] = job
	}
	return result, nil
}

func (m *memoryStore) get(id string) (Job, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[id]
	return job, ok
}

func TestStatusValid(t *testing.T) {
	for _, s := range []Status{StatusQueued, StatusRunning, StatusDone, StatusFailed, StatusCancelled} {
		if !s.Valid() {
			t.Fatalf("состояние %q должно быть валидным", s)
		}
	}
	if (Status("unknown")).Valid() {
		t.Fatal("неизвестное состояние не должно быть валидным")
	}
}

func TestStatusTerminal(t *testing.T) {
	for _, s := range []Status{StatusDone, StatusFailed, StatusCancelled} {
		if !s.Terminal() {
			t.Fatalf("состояние %q должно быть терминальным", s)
		}
	}
	for _, s := range []Status{StatusQueued, StatusRunning} {
		if s.Terminal() {
			t.Fatalf("состояние %q не должно быть терминальным", s)
		}
	}
}

func TestCancelRegistryLifecycle(t *testing.T) {
	registry := NewCancelRegistry()
	if registry.Cancel("missing") {
		t.Fatal("Cancel незарегистрированного задания должен вернуть false")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	registry.Register("job-1", cancel)
	go func() { <-ctx.Done(); close(done) }()

	if !registry.Cancel("job-1") {
		t.Fatal("Cancel зарегистрированного задания должен вернуть true")
	}
	<-done

	registry.Unregister("job-1")
	if registry.Cancel("job-1") {
		t.Fatal("после снятия регистрации Cancel должен вернуть false")
	}
	// Повторное снятие регистрации идемпотентно и не должно паниковать.
	registry.Unregister("job-1")
}

func TestRequestCancelTransitions(t *testing.T) {
	ctx := context.Background()

	queued := newMemoryStore()
	if err := queued.Create(ctx, Job{ID: "q"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := queued.RequestCancel(ctx, "q"); err != nil {
		t.Fatalf("RequestCancel(queued): %v", err)
	}
	if job, _ := queued.get("q"); job.Status != StatusCancelled {
		t.Fatalf("queued-задание должно перейти в cancelled, получено %q", job.Status)
	}

	running := newMemoryStore()
	if err := running.Create(ctx, Job{ID: "r"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := running.TakeQueued(ctx); err != nil {
		t.Fatalf("TakeQueued: %v", err)
	}
	if err := running.RequestCancel(ctx, "r"); err != nil {
		t.Fatalf("RequestCancel(running): %v", err)
	}
	job, _ := running.get("r")
	if job.Status != StatusCancelled || !job.CancelRequested {
		t.Fatalf("running-задание должно перейти в cancelled с признаком отмены, получено %q cancel=%v", job.Status, job.CancelRequested)
	}

	// Терминальное задание — no-op, повторная отмена без ошибки.
	if err := running.RequestCancel(ctx, "r"); err != nil {
		t.Fatalf("RequestCancel(terminal): %v", err)
	}
	if job, _ := running.get("r"); job.Status != StatusCancelled {
		t.Fatalf("терминальное задание не должно меняться, получено %q", job.Status)
	}
	// Позднее завершение не переворачивает уже принятую отмену.
	if err := running.UpdateStatus(ctx, "r", StatusRunning, StatusDone, "", ""); !errors.Is(err, ErrTransitionRejected) {
		t.Fatalf("поздний переход должно быть отклонён, получено %v", err)
	}
	if job, _ := running.get("r"); job.Status != StatusCancelled {
		t.Fatalf("состояние после отклонённого перехода %q, ожидалось cancelled", job.Status)
	}
}

// TestUpdateStatusGuardsTransitions проверяет, что переход состояния возможен
// только из допустимого исходного, а недопустимый отклоняется без изменения задания.
func TestUpdateStatusGuardsTransitions(t *testing.T) {
	store := newMemoryStore()
	if err := store.Create(context.Background(), Job{ID: "job-1"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// queued -> done напрямую недопустим: задание сначала берётся в обработку.
	if err := store.UpdateStatus(context.Background(), "job-1", StatusRunning, StatusDone, "", ""); !errors.Is(err, ErrTransitionRejected) {
		t.Fatalf("переход queued -> done должно быть отклонён, получено %v", err)
	}
	if job, _ := store.get("job-1"); job.Status != StatusQueued {
		t.Fatalf("состояние после отклонённого перехода %q, ожидалось queued", job.Status)
	}

	if _, err := store.TakeQueued(context.Background()); err != nil {
		t.Fatalf("TakeQueued: %v", err)
	}
	if err := store.UpdateStatus(context.Background(), "job-1", StatusRunning, StatusDone, "", ""); err != nil {
		t.Fatalf("переход running -> done: %v", err)
	}
	if job, _ := store.get("job-1"); job.Status != StatusDone {
		t.Fatalf("состояние %q, ожидалось done", job.Status)
	}
}

// TestRunnerCancelsWhenCancelRecordedInStore проверяет распознавание отмены по
// данным хранилища: отмена, не сопровождаемая сигналом, всё равно останавливает
// обработчик и завершает задание cancelled.
func TestRunnerCancelsWhenCancelRecordedInStore(t *testing.T) {
	store := newMemoryStore()
	if err := store.Create(context.Background(), Job{ID: "job-1"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	started := make(chan struct{})
	runner := &Runner{
		Store:    store,
		Interval: 5 * time.Millisecond,
		Handler: func(ctx context.Context, _ Job) error {
			close(started)
			<-ctx.Done()
			return ErrCancelled
		},
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := runner.RunOnce(context.Background()); err != nil {
			t.Errorf("RunOnce: %v", err)
		}
	}()

	<-started
	if err := store.RequestCancel(context.Background(), "job-1"); err != nil {
		t.Fatalf("RequestCancel: %v", err)
	}
	<-done

	job, _ := store.get("job-1")
	if job.Status != StatusCancelled {
		t.Fatalf("состояние %q, ожидалось cancelled", job.Status)
	}
}

// TestReconcileRunning проверяет согласование осиротевших running-заданий:
// без признака отмены и с запасом попыток — обратно в очередь; с признаком
// отмены — cancelled; с исчерпанными попытками — failed с кодом; queued и
// терминальные задания согласование не трогает.
func TestReconcileRunning(t *testing.T) {
	store := newMemoryStore()
	ctx := context.Background()

	// Осиротевшее задание без признака отмены.
	if err := store.Create(ctx, Job{ID: "orphan"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.TakeQueued(ctx); err != nil {
		t.Fatalf("TakeQueued: %v", err)
	}

	// Осиротевшее задание с признаком отмены. Ветка недостижима через публичный
	// API (решительная отмена сразу даёт cancelled), поэтому состояние выставляется
	// напрямую: так проверяется защита для строк, оставленных прежней версией.
	if err := store.Create(ctx, Job{ID: "cancel-requested"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.TakeQueued(ctx); err != nil {
		t.Fatalf("TakeQueued: %v", err)
	}
	store.mu.Lock()
	requested := store.jobs["cancel-requested"]
	requested.CancelRequested = true
	store.jobs["cancel-requested"] = requested
	store.mu.Unlock()

	// Осиротевшее задание с исчерпанными попытками.
	if err := store.Create(ctx, Job{ID: "exhausted"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.TakeQueued(ctx); err != nil {
		t.Fatalf("TakeQueued: %v", err)
	}
	store.mu.Lock()
	exhausted := store.jobs["exhausted"]
	exhausted.Attempts = 3
	store.jobs["exhausted"] = exhausted
	store.mu.Unlock()

	// Терминальное задание.
	if err := store.Create(ctx, Job{ID: "finished"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.TakeQueued(ctx); err != nil {
		t.Fatalf("TakeQueued: %v", err)
	}
	if err := store.UpdateStatus(ctx, "finished", StatusRunning, StatusDone, "", ""); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	// Задание в очереди.
	if err := store.Create(ctx, Job{ID: "queued-left"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Срок аренды в будущем: все running-задания считаются осиротевшими.
	stale := time.Now().Add(time.Hour)
	result, err := store.ReconcileRunning(ctx, stale, 3, "lifecycle.interrupted", "Задание прервано перезапуском")
	if err != nil {
		t.Fatalf("ReconcileRunning: %v", err)
	}
	if result.Total() != 3 {
		t.Fatalf("согласовано %d заданий, ожидалось 3 (%+v)", result.Total(), result)
	}
	if job, _ := store.get("orphan"); job.Status != StatusQueued {
		t.Fatalf("осиротевшее задание %q, ожидалось queued", job.Status)
	}
	if job, _ := store.get("cancel-requested"); job.Status != StatusCancelled {
		t.Fatalf("задание с отменой %q, ожидалось cancelled", job.Status)
	}
	if job, _ := store.get("exhausted"); job.Status != StatusFailed || job.ErrorCode != "lifecycle.interrupted" {
		t.Fatalf("задание с исчерпанными попытками %q код %q, ожидалось failed/lifecycle.interrupted", job.Status, job.ErrorCode)
	}
	if job, _ := store.get("finished"); job.Status != StatusDone {
		t.Fatalf("терминальное задание изменилось на %q", job.Status)
	}
	if job, _ := store.get("queued-left"); job.Status != StatusQueued {
		t.Fatalf("queued-задание изменилось на %q", job.Status)
	}
}

// TestReconcileRunningHonoursLease проверяет, что задание с недавней активностью
// (возможно, живой обработчик другой копии) не согласуется.
func TestReconcileRunningHonoursLease(t *testing.T) {
	store := newMemoryStore()
	ctx := context.Background()
	if err := store.Create(ctx, Job{ID: "live"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.TakeQueued(ctx); err != nil {
		t.Fatalf("TakeQueued: %v", err)
	}

	result, err := store.ReconcileRunning(ctx, time.Now().Add(-time.Minute), 5, "c", "r")
	if err != nil {
		t.Fatalf("ReconcileRunning: %v", err)
	}
	if result.Total() != 0 {
		t.Fatalf("свежее задание не должно согласовываться, получено %+v", result)
	}
	if job, _ := store.get("live"); job.Status != StatusRunning {
		t.Fatalf("состояние %q, ожидалось running", job.Status)
	}
}

// TestRunnerReconcilePassesPolicy проверяет, что раннер передаёт в хранилище
// политику (лимит попыток, код и формулировку отказа).
func TestRunnerReconcilePassesPolicy(t *testing.T) {
	store := newMemoryStore()
	ctx := context.Background()
	if err := store.Create(ctx, Job{ID: "orphan"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.TakeQueued(ctx); err != nil {
		t.Fatalf("TakeQueued: %v", err)
	}
	// Активность состариваем, чтобы задание попало под аренду.
	store.mu.Lock()
	job := store.jobs["orphan"]
	job.UpdatedAt = time.Now().Add(-time.Hour)
	store.jobs["orphan"] = job
	store.mu.Unlock()

	runner := &Runner{
		Store:       store,
		LeaseTTL:    time.Minute,
		MaxAttempts: 1,
		FailCode:    "lifecycle.interrupted",
		FailReason:  "Задание прервано перезапуском",
	}
	result, err := runner.Reconcile(ctx)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if result.Failed != 1 {
		t.Fatalf("ожидался один отказ, получено %+v", result)
	}
	job, _ = store.get("orphan")
	if job.Status != StatusFailed || job.ErrorCode != "lifecycle.interrupted" || job.LastError != "Задание прервано перезапуском" {
		t.Fatalf("исход %q код %q сообщение %q", job.Status, job.ErrorCode, job.LastError)
	}
}

func TestRunnerMarksDone(t *testing.T) {
	store := newMemoryStore()
	if err := store.Create(context.Background(), Job{ID: "job-1", Payload: []byte("p")}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	runner := &Runner{
		Store:    store,
		Interval: time.Hour,
		Handler: func(_ context.Context, job Job) error {
			if job.ID != "job-1" {
				t.Fatalf("обработчик получил задание %q, ожидалось job-1", job.ID)
			}
			return nil
		},
	}

	processed, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if processed != 1 {
		t.Fatalf("обработано %d заданий, ожидалось 1", processed)
	}
	job, ok := store.get("job-1")
	if !ok {
		t.Fatal("задание не найдено")
	}
	if job.Status != StatusDone {
		t.Fatalf("состояние %q, ожидалось done", job.Status)
	}
	if job.Attempts != 1 {
		t.Fatalf("число попыток %d, ожидалось 1", job.Attempts)
	}
}

func TestRunnerMarksFailed(t *testing.T) {
	store := newMemoryStore()
	if err := store.Create(context.Background(), Job{ID: "job-1"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	runner := &Runner{
		Store:    store,
		Interval: time.Hour,
		Handler:  func(context.Context, Job) error { return errors.New("обход упал") },
	}

	processed, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if processed != 1 {
		t.Fatalf("обработано %d заданий, ожидалось 1", processed)
	}
	job, _ := store.get("job-1")
	if job.Status != StatusFailed {
		t.Fatalf("состояние %q, ожидалось failed", job.Status)
	}
	if job.LastError != "обход упал" {
		t.Fatalf("причина отказа %q, ожидалась «обход упал»", job.LastError)
	}
	if job.ErrorCode != "" {
		t.Fatalf("код отказа %q, ожидался пустой (обычная ошибка без кода)", job.ErrorCode)
	}
}

// codedErr — тестовая ошибка, реализующая CodedError.
type codedErr struct {
	code    string
	message string
	detail  string
}

func (e codedErr) Error() string   { return e.detail }
func (e codedErr) Code() string    { return e.code }
func (e codedErr) Message() string { return e.message }

// TestRunnerMarksFailedWithCode проверяет, что ошибка-носитель кода даёт failed с
// кодом и бизнес-сообщением, а техническая деталь не попадает в сообщение.
func TestRunnerMarksFailedWithCode(t *testing.T) {
	store := newMemoryStore()
	if err := store.Create(context.Background(), Job{ID: "job-1"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	runner := &Runner{
		Store:    store,
		Interval: time.Hour,
		Handler: func(context.Context, Job) error {
			return codedErr{code: "crawl.failed", message: "Не удалось обойти сайт", detail: "postgres: таймаут 10s"}
		},
	}

	if _, err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	job, _ := store.get("job-1")
	if job.Status != StatusFailed {
		t.Fatalf("состояние %q, ожидалось failed", job.Status)
	}
	if job.ErrorCode != "crawl.failed" {
		t.Fatalf("код отказа %q, ожидался crawl.failed", job.ErrorCode)
	}
	if job.LastError != "Не удалось обойти сайт" {
		t.Fatalf("сообщение %q, ожидалась бизнес-формулировка", job.LastError)
	}
}

// TestRunnerMarksCancelled проверяет, что отмена задания сигналом переводит его в
// cancelled без текста ошибки, а обработчик получает отменённый контекст.
func TestRunnerMarksCancelled(t *testing.T) {
	store := newMemoryStore()
	if err := store.Create(context.Background(), Job{ID: "job-1"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	registry := NewCancelRegistry()
	started := make(chan struct{})
	runner := &Runner{
		Store:    store,
		Signals:  registry,
		Interval: time.Hour,
		Handler: func(ctx context.Context, _ Job) error {
			close(started)
			<-ctx.Done()
			return ErrCancelled
		},
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := runner.RunOnce(context.Background()); err != nil {
			t.Errorf("RunOnce: %v", err)
		}
	}()

	<-started
	if !registry.Cancel("job-1") {
		t.Fatal("выполняющееся задание должно быть зарегистрировано в реестре отмены")
	}
	<-done

	job, _ := store.get("job-1")
	if job.Status != StatusCancelled {
		t.Fatalf("состояние %q, ожидалось cancelled", job.Status)
	}
	if job.LastError != "" {
		t.Fatalf("отменённое задание не должно нести ошибку, получено %q", job.LastError)
	}
}

// TestRunnerProcessStopIsNotCancel проверяет, что остановка процесса (отмена
// родительского контекста) не считается отменой задания и даёт failed.
func TestRunnerProcessStopIsNotCancel(t *testing.T) {
	store := newMemoryStore()
	if err := store.Create(context.Background(), Job{ID: "job-1"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	runner := &Runner{
		Store:    store,
		Signals:  NewCancelRegistry(),
		Interval: time.Hour,
		Handler: func(ctx context.Context, _ Job) error {
			<-ctx.Done()
			return ErrCancelled
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := runner.RunOnce(ctx); err != nil {
			t.Errorf("RunOnce: %v", err)
		}
	}()
	cancel()
	<-done

	job, _ := store.get("job-1")
	if job.Status != StatusFailed {
		t.Fatalf("состояние %q, ожидалось failed (остановка процесса — не отмена задания)", job.Status)
	}
}

func TestRunOnceEmptyQueue(t *testing.T) {
	runner := &Runner{Store: newMemoryStore(), Handler: func(context.Context, Job) error { return nil }}
	processed, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if processed != 0 {
		t.Fatalf("обработано %d заданий, ожидалось 0", processed)
	}
}

func TestTakeQueuedAtomicTransition(t *testing.T) {
	store := newMemoryStore()
	if err := store.Create(context.Background(), Job{ID: "job-1"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	first, err := store.TakeQueued(context.Background())
	if err != nil {
		t.Fatalf("TakeQueued: %v", err)
	}
	if first.Status != StatusRunning {
		t.Fatalf("состояние после взятия %q, ожидалось running", first.Status)
	}
	if _, err := store.TakeQueued(context.Background()); !errors.Is(err, ErrNoJob) {
		t.Fatalf("ожидалась ErrNoJob, получено %v", err)
	}
}
