package jobs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"lead-gen/backend/platform/observability/observ"
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
			job.StartedAt = time.Now()
			job.UpdatedAt = job.StartedAt
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
	if to == StatusDone {
		job.Progress = ProgressMax
	}
	job.UpdatedAt = time.Now()
	m.jobs[id] = job
	return nil
}

func (m *memoryStore) RenewLease(_ context.Context, id string, attempt int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[id]
	if !ok {
		return ErrNotFound
	}
	if job.Status != StatusRunning || job.Attempts != attempt {
		return ErrTransitionRejected
	}
	job.UpdatedAt = time.Now()
	m.jobs[id] = job
	return nil
}

func (m *memoryStore) UpdateOwnedStatus(_ context.Context, id string, attempt int, to Status, reason, code string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[id]
	if !ok {
		return ErrNotFound
	}
	if job.Status != StatusRunning || job.Attempts != attempt {
		return ErrTransitionRejected
	}
	job.Status = to
	job.LastError = reason
	job.ErrorCode = code
	job.UpdatedAt = time.Now()
	m.jobs[id] = job
	return nil
}

// UpdateProgress фиксирует прогресс выполняющегося задания (ProgressStore).
func (m *memoryStore) UpdateProgress(_ context.Context, id string, progress int, step string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[id]
	if !ok {
		return ErrNotFound
	}
	if job.Status != StatusRunning {
		return ErrTransitionRejected
	}
	job.Progress = ClampProgress(progress)
	job.Step = step
	job.UpdatedAt = time.Now()
	m.jobs[id] = job
	return nil
}

// ListJobs возвращает задания по отбору в стабильном порядке (JobLister).
func (m *memoryStore) ListJobs(_ context.Context, filter JobFilter) ([]Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Job, 0, len(m.jobs))
	for _, job := range m.jobs {
		if filter.Match(job) {
			out = append(out, job)
		}
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
// (JobRetrier).
func (m *memoryStore) RetryFailed(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[id]
	if !ok {
		return ErrNotFound
	}
	if job.Status != StatusFailed {
		return ErrTransitionRejected
	}
	job.Status = StatusQueued
	job.Attempts = 0
	job.LastError = ""
	job.ErrorCode = ""
	job.Progress = 0
	job.Step = ""
	job.StartedAt = time.Time{}
	job.CancelRequested = false
	job.UpdatedAt = time.Now()
	m.jobs[id] = job
	return nil
}

// storeOnly прячет расширения Store: тип реализует только базовый контракт, чтобы
// проверить честный ErrUnsupported для отсутствующих возможностей.
type storeOnly struct {
	Store
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

func TestRunnerHeartbeatProtectsLongHandlerFromReconcile(t *testing.T) {
	store := newMemoryStore()
	ctx := context.Background()
	if err := store.Create(ctx, Job{ID: "long"}); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	runner := &Runner{
		Store: store, LeaseTTL: 90 * time.Millisecond, Interval: time.Hour,
		Handler: func(ctx context.Context, _ Job) error {
			close(started)
			select {
			case <-time.After(180 * time.Millisecond):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
	done := make(chan error, 1)
	go func() {
		_, err := runner.RunOnce(ctx)
		done <- err
	}()
	<-started
	deadline := time.Now().Add(140 * time.Millisecond)
	for time.Now().Before(deadline) {
		if _, err := runner.Reconcile(ctx); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if job, _ := store.get("long"); job.Status != StatusRunning {
		t.Fatalf("живой долгий handler был признан orphan: %s", job.Status)
	}
	if err := <-done; err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if job, _ := store.get("long"); job.Status != StatusDone {
		t.Fatalf("исход живого handler: %s, ожидался done", job.Status)
	}
}

type controlledHeartbeatStore struct {
	*memoryStore
	renewStarted     chan struct{}
	renewResult      chan error
	shutdownErr      error
	renewStartedOnce sync.Once
	ownedUpdates     atomic.Int32
}

func (s *controlledHeartbeatStore) RenewLease(ctx context.Context, _ string, _ int) error {
	s.renewStartedOnce.Do(func() { close(s.renewStarted) })
	select {
	case err := <-s.renewResult:
		return err
	case <-ctx.Done():
		if s.shutdownErr != nil {
			return s.shutdownErr
		}
		return ctx.Err()
	}
}

func (s *controlledHeartbeatStore) UpdateOwnedStatus(ctx context.Context, id string, attempt int, to Status, reason, code string) error {
	s.ownedUpdates.Add(1)
	return s.memoryStore.UpdateOwnedStatus(ctx, id, attempt, to, reason, code)
}

func TestRunnerHeartbeatShutdownDoesNotLoseLease(t *testing.T) {
	for _, shutdownErr := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(shutdownErr.Error(), func(t *testing.T) {
			store := &controlledHeartbeatStore{
				memoryStore:  newMemoryStore(),
				renewStarted: make(chan struct{}),
				renewResult:  make(chan error),
				shutdownErr:  shutdownErr,
			}
			if err := store.Create(context.Background(), Job{ID: "stop-heartbeat"}); err != nil {
				t.Fatal(err)
			}
			runner := &Runner{
				Store: store, LeaseTTL: 3 * time.Millisecond, Interval: time.Hour,
				Handler: func(context.Context, Job) error {
					<-store.renewStarted
					return nil
				},
			}
			type runResult struct {
				processed int
				err       error
			}
			done := make(chan runResult, 1)
			go func() {
				processed, err := runner.RunOnce(context.Background())
				done <- runResult{processed: processed, err: err}
			}()

			result := <-done
			if result.err != nil || result.processed != 1 {
				t.Fatalf("RunOnce = (%d, %v), ожидалось штатное завершение", result.processed, result.err)
			}
			job, _ := store.get("stop-heartbeat")
			if job.Status != StatusDone {
				t.Fatalf("статус после остановки heartbeat %q, ожидался done", job.Status)
			}
			if got := store.ownedUpdates.Load(); got != 1 {
				t.Fatalf("вызовов finishOwned %d, ожидался 1", got)
			}
		})
	}

	t.Run("ошибка продления при активном heartbeat", func(t *testing.T) {
		store := &controlledHeartbeatStore{
			memoryStore:  newMemoryStore(),
			renewStarted: make(chan struct{}),
			renewResult:  make(chan error, 1),
		}
		if err := store.Create(context.Background(), Job{ID: "lost-heartbeat"}); err != nil {
			t.Fatal(err)
		}
		runner := &Runner{
			Store: store, LeaseTTL: 3 * time.Millisecond, Interval: time.Hour,
			Handler: func(ctx context.Context, _ Job) error {
				<-ctx.Done()
				return ctx.Err()
			},
		}
		type runResult struct {
			processed int
			err       error
		}
		done := make(chan runResult, 1)
		go func() {
			processed, err := runner.RunOnce(context.Background())
			done <- runResult{processed: processed, err: err}
		}()

		<-store.renewStarted
		store.renewResult <- errors.New("lease renewal rejected")
		result := <-done
		if result.err != nil || result.processed != 0 {
			t.Fatalf("RunOnce = (%d, %v), ожидалось оставить исход владельцу lease", result.processed, result.err)
		}
		job, _ := store.get("lost-heartbeat")
		if job.Status != StatusRunning {
			t.Fatalf("статус после потери lease %q, ожидался running без финализации", job.Status)
		}
		if got := store.ownedUpdates.Load(); got != 0 {
			t.Fatalf("потерявший lease владелец вызвал finishOwned %d раз", got)
		}
	})
}

func TestLeaseGenerationFencesRestartedWorker(t *testing.T) {
	store := newMemoryStore()
	ctx := context.Background()
	if err := store.Create(ctx, Job{ID: "restart"}); err != nil {
		t.Fatal(err)
	}
	old, err := store.TakeQueued(ctx)
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	stale := store.jobs["restart"]
	stale.UpdatedAt = time.Now().Add(-time.Hour)
	store.jobs["restart"] = stale
	store.mu.Unlock()
	if _, err := store.ReconcileRunning(ctx, time.Now().Add(-time.Minute), 4, "", ""); err != nil {
		t.Fatal(err)
	}
	current, err := store.TakeQueued(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if current.Attempts != old.Attempts+1 {
		t.Fatalf("поколение новой попытки = %d, старой = %d", current.Attempts, old.Attempts)
	}
	if err := store.RenewLease(ctx, "restart", old.Attempts); !errors.Is(err, ErrTransitionRejected) {
		t.Fatalf("старая попытка продлила новую аренду: %v", err)
	}
	if err := store.UpdateOwnedStatus(ctx, "restart", old.Attempts, StatusDone, "", ""); !errors.Is(err, ErrTransitionRejected) {
		t.Fatalf("старая попытка завершила новую: %v", err)
	}
	if err := store.UpdateOwnedStatus(ctx, "restart", current.Attempts, StatusDone, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.RenewLease(ctx, "restart", old.Attempts); !errors.Is(err, ErrTransitionRejected) {
		t.Fatalf("heartbeat изменил terminal job: %v", err)
	}
}

type reconcileCountingStore struct {
	*memoryStore
	count atomic.Int32
}

func (s *reconcileCountingStore) ReconcileRunning(ctx context.Context, staleBefore time.Time, maxAttempts int, failCode, failReason string) (ReconcileResult, error) {
	s.count.Add(1)
	return s.memoryStore.ReconcileRunning(ctx, staleBefore, maxAttempts, failCode, failReason)
}

func TestRunnerRunReconcilesRepeatedly(t *testing.T) {
	store := &reconcileCountingStore{memoryStore: newMemoryStore()}
	runner := &Runner{Store: store, Handler: func(context.Context, Job) error { return nil }, Interval: 5 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	deadline := time.Now().Add(time.Second)
	for store.count.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := store.count.Load(); got < 3 {
		t.Fatalf("Run выполнил reconciliation %d раз, ожидалось периодически", got)
	}
}

// TestRunnerLogsLifecycle проверяет, что раннер логирует жизненный цикл задания
// (взятие, успех, неудачу) с task_id задания (design.md, Решение 4).
func TestRunnerLogsLifecycle(t *testing.T) {
	// Успех: лог несёт «взято» и «выполнено» с task_id.
	store := newMemoryStore()
	if err := store.Create(context.Background(), Job{ID: "job-1"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	var successBuf bytes.Buffer
	successRunner := &Runner{
		Store:    store,
		Interval: time.Hour,
		Logger:   observ.NewLoggerWithWriter(&successBuf),
		Handler:  func(context.Context, Job) error { return nil },
	}
	if _, err := successRunner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if !strings.Contains(successBuf.String(), `"task_id":"job-1"`) {
		t.Fatalf("лог не несёт task_id: %s", successBuf.String())
	}
	if !strings.Contains(successBuf.String(), "задание взято") || !strings.Contains(successBuf.String(), "задание выполнено") {
		t.Fatalf("лог не несёт жизненный цикл: %s", successBuf.String())
	}

	// Неудача: лог несёт «не выполнено» с task_id.
	failStore := newMemoryStore()
	if err := failStore.Create(context.Background(), Job{ID: "job-2"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	var failBuf bytes.Buffer
	failRunner := &Runner{
		Store:    failStore,
		Interval: time.Hour,
		Logger:   observ.NewLoggerWithWriter(&failBuf),
		Handler:  func(context.Context, Job) error { return errors.New("обход упал") },
	}
	if _, err := failRunner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if !strings.Contains(failBuf.String(), `"task_id":"job-2"`) {
		t.Fatalf("лог не несёт task_id: %s", failBuf.String())
	}
	if !strings.Contains(failBuf.String(), "задание не выполнено") {
		t.Fatalf("лог не несёт неудачу: %s", failBuf.String())
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
	if job.LastError != "Ошибка задания" || job.ErrorCode != "job.failed" {
		t.Fatalf("безопасный fallback %q/%q, ожидался job.failed/Ошибка задания", job.ErrorCode, job.LastError)
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
		FailureNormalizer: NewCatalogFailureNormalizer(map[string]FailureRule{
			"crawl.failed": {Message: "Не удалось обойти сайт", Diagnostic: "crawl_provider_failure"},
		}, Failure{}),
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
	if first.StartedAt.IsZero() {
		t.Fatal("взятие задания должно фиксировать время начала")
	}
	if _, err := store.TakeQueued(context.Background()); !errors.Is(err, ErrNoJob) {
		t.Fatalf("ожидалась ErrNoJob, получено %v", err)
	}
}

// TestNormalizeFailure проверяет выделение кода и сообщения из ошибки
// обработчика: обычная ошибка даёт пустой код и свой текст; ошибка-носитель
// CodedError (в том числе обёрнутая) — стабильный код и бизнес-формулировку без
// технических деталей.
func TestNormalizeFailure(t *testing.T) {
	normalizer := NewCatalogFailureNormalizer(map[string]FailureRule{
		"crawl.failed": {Message: "Не удалось обойти сайт", Diagnostic: "crawl_provider_failure"},
	}, Failure{})
	coded := codedErr{code: "crawl.failed", message: "Не удалось обойти сайт", detail: "postgres: таймаут 10s"}
	if got := normalizer.Normalize(coded); got.Code != "crawl.failed" || got.Message != "Не удалось обойти сайт" || got.Diagnostic != "crawl_provider_failure" {
		t.Fatalf("кодированная ошибка: %+v", got)
	}
	wrapped := fmt.Errorf("шаг обхода: %w", coded)
	if got := normalizer.Normalize(wrapped); got.Code != "crawl.failed" || got.Message != "Не удалось обойти сайт" {
		t.Fatalf("обёрнутая кодированная ошибка: %+v", got)
	}
	if got := normalizer.Normalize(errors.New("sentinel-secret user@example.com")); got.Code != "job.failed" || got.Message != "Ошибка задания" {
		t.Fatalf("неизвестная ошибка не получила фиксированный fallback: %+v", got)
	}
}

// TestRunnerNormalizesWrappedCodedError проверяет сквозной путь: обработчик
// возвращает обёрнутую CodedError, а задание получает нормализованную причину без
// технических деталей.
func TestRunnerNormalizesWrappedCodedError(t *testing.T) {
	store := newMemoryStore()
	if err := store.Create(context.Background(), Job{ID: "job-1"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	runner := &Runner{
		Store:    store,
		Interval: time.Hour,
		FailureNormalizer: NewCatalogFailureNormalizer(map[string]FailureRule{
			"crawl.failed": {Message: "Не удалось обойти сайт", Diagnostic: "crawl_provider_failure"},
		}, Failure{}),
		Handler: func(context.Context, Job) error {
			return fmt.Errorf("обход: %w", codedErr{code: "crawl.failed", message: "Не удалось обойти сайт", detail: "postgres: таймаут 10s"})
		},
	}
	if _, err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	job, _ := store.get("job-1")
	if job.Status != StatusFailed || job.ErrorCode != "crawl.failed" {
		t.Fatalf("исход %q код %q, ожидалось failed/crawl.failed", job.Status, job.ErrorCode)
	}
	if job.LastError != "Не удалось обойти сайт" {
		t.Fatalf("причина %q, ожидалась бизнес-формулировка", job.LastError)
	}
	if strings.Contains(job.LastError, "postgres") {
		t.Fatalf("в причине задания техническая деталь: %q", job.LastError)
	}
}

func TestRunnerStoresAndLogsOnlySafeFailure(t *testing.T) {
	secret := "sentinel-secret alice@example.com 550e8400-e29b-41d4-a716-446655440000"
	cases := []struct {
		name string
		err  error
		code string
		msg  string
	}{
		{name: "обычная", err: errors.New(secret), code: "job.failed", msg: "Ошибка задания"},
		{name: "wrapped", err: fmt.Errorf("wrapped: %w", errors.New(secret)), code: "job.failed", msg: "Ошибка задания"},
		{name: "coded-empty-message", err: fmt.Errorf("wrapped: %w", codedErr{code: "crawl.failed", detail: secret}), code: "crawl.failed", msg: "Не удалось обойти сайт"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newMemoryStore()
			if err := store.Create(context.Background(), Job{ID: "job-safe"}); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			runner := &Runner{
				Store: store, Interval: time.Hour, Logger: observ.NewLoggerWithWriter(&output),
				FailureNormalizer: NewCatalogFailureNormalizer(map[string]FailureRule{
					"crawl.failed": {Message: "Не удалось обойти сайт", Diagnostic: "crawl_provider_failure"},
				}, Failure{}),
				Handler: func(context.Context, Job) error { return tc.err },
			}
			if _, err := runner.RunOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			job, _ := store.get("job-safe")
			if job.ErrorCode != tc.code || job.LastError != tc.msg {
				t.Fatalf("причина задания = %q/%q", job.ErrorCode, job.LastError)
			}
			logged := output.String()
			for _, unsafe := range []string{"sentinel-secret", "alice@example.com", "550e8400-e29b-41d4-a716-446655440000", "wrapped:"} {
				if strings.Contains(logged, unsafe) || strings.Contains(job.LastError, unsafe) {
					t.Fatalf("исходные данные просочились (%q): %s / %s", unsafe, job.LastError, logged)
				}
			}
			if tc.code == "crawl.failed" && !strings.Contains(logged, `"diagnostic":"crawl_provider_failure"`) {
				t.Fatalf("безопасная диагностика отсутствует в JSON-логе: %s", logged)
			}
		})
	}
}

// TestReportProgressClampsAndGuards проверяет обновление прогресса: значение
// приводится к [0,100], обновляется только running-задание.
func TestReportProgressClampsAndGuards(t *testing.T) {
	store := newMemoryStore()
	ctx := context.Background()
	if err := store.Create(ctx, Job{ID: "job-1"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Задание в очереди ещё не выполняется.
	if err := ReportProgress(ctx, store, "job-1", 10, "обход"); !errors.Is(err, ErrTransitionRejected) {
		t.Fatalf("прогресс queued-задания = %v, ожидалась ErrTransitionRejected", err)
	}
	if _, err := store.TakeQueued(ctx); err != nil {
		t.Fatalf("TakeQueued: %v", err)
	}
	if err := ReportProgress(ctx, store, "job-1", 150, "обход"); err != nil {
		t.Fatalf("ReportProgress: %v", err)
	}
	job, _ := store.get("job-1")
	if job.Progress != ProgressMax || job.Step != "обход" {
		t.Fatalf("прогресс %d шаг %q, ожидалось %d/обход", job.Progress, job.Step, ProgressMax)
	}
	if err := ReportProgress(ctx, store, "job-1", -5, "финал"); err != nil {
		t.Fatalf("ReportProgress: %v", err)
	}
	if job, _ := store.get("job-1"); job.Progress != 0 {
		t.Fatalf("прогресс %d, ожидалось 0 (нижняя граница)", job.Progress)
	}
	// Отсутствующее задание отличается от недопустимого состояния.
	if err := ReportProgress(ctx, store, "нет-такого", 10, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("прогресс отсутствующего задания = %v, ожидалась ErrNotFound", err)
	}
}

// TestListDLQReturnsExhaustedFailedJobsInStableOrder проверяет видимость DLQ:
// возвращаются только failed-задания с исчерпанными попытками, в стабильном
// порядке (свежие раньше, при равенстве — по возрастанию ID).
func TestListDLQReturnsExhaustedFailedJobsInStableOrder(t *testing.T) {
	store := newMemoryStore()
	ctx := context.Background()
	base := time.Now()
	seed := func(id string, status Status, attempts int, updated time.Time) {
		store.mu.Lock()
		store.jobs[id] = Job{
			ID:        id,
			Status:    status,
			Attempts:  attempts,
			LastError: "Не удалось обойти сайт",
			ErrorCode: "crawl.failed",
			CreatedAt: base,
			UpdatedAt: updated,
		}
		store.mu.Unlock()
	}
	// Исчерпавшие попытки failed-задания: три штуки с разным UpdatedAt.
	seed("old", StatusFailed, 3, base.Add(-time.Hour))
	seed("new", StatusFailed, 2, base)
	seed("mid", StatusFailed, 5, base.Add(-time.Minute))
	// failed, но попытки не исчерпаны — не DLQ.
	seed("retryable", StatusFailed, 1, base.Add(-2*time.Hour))
	// running с исчерпанными попытками — не failed, не DLQ.
	seed("running", StatusRunning, 5, base.Add(time.Hour))
	// queued — не DLQ.
	seed("queued", StatusQueued, 0, base.Add(2*time.Hour))

	got, err := ListDLQ(ctx, store, 2, 0)
	if err != nil {
		t.Fatalf("ListDLQ: %v", err)
	}
	want := []string{"new", "mid", "old"}
	if len(got) != len(want) {
		t.Fatalf("DLQ вернула %d заданий (%+v), ожидалось %d", len(got), got, len(want))
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("позиция %d = %q, ожидалось %q (порядок нарушен)", i, got[i].ID, id)
		}
	}
	if got[0].ErrorCode != "crawl.failed" || got[0].LastError != "Не удалось обойти сайт" {
		t.Fatalf("причина из DLQ: код %q сообщение %q", got[0].ErrorCode, got[0].LastError)
	}

	limited, err := ListDLQ(ctx, store, 2, 2)
	if err != nil {
		t.Fatalf("ListDLQ(limit): %v", err)
	}
	if len(limited) != 2 || limited[0].ID != "new" || limited[1].ID != "mid" {
		t.Fatalf("ListDLQ(limit) = %+v, ожидались new/mid", limited)
	}
}

// TestRetryFailedJobRequeues проверяет явный повтор: failed-задание возвращается в
// очередь с новым бюджетом попыток и очищенной причиной; не-failed и
// отсутствующее задания отклоняются.
func TestRetryFailedJobRequeues(t *testing.T) {
	store := newMemoryStore()
	ctx := context.Background()
	store.mu.Lock()
	store.jobs["dead"] = Job{
		ID: "dead", Status: StatusFailed, Attempts: 3,
		LastError: "Не удалось обойти сайт", ErrorCode: "crawl.failed",
		Progress: 100, Step: "обход",
		CreatedAt: time.Now().Add(-time.Hour), UpdatedAt: time.Now(),
	}
	store.mu.Unlock()

	if err := RetryJob(ctx, store, "dead"); err != nil {
		t.Fatalf("RetryJob: %v", err)
	}
	job, _ := store.get("dead")
	if job.Status != StatusQueued {
		t.Fatalf("статус %q, ожидалось queued", job.Status)
	}
	if job.Attempts != 0 {
		t.Fatalf("попытки %d, ожидалось 0 (новый бюджет)", job.Attempts)
	}
	if job.LastError != "" || job.ErrorCode != "" {
		t.Fatalf("причина не очищена: %q/%q", job.ErrorCode, job.LastError)
	}
	if job.Progress != 0 || job.Step != "" || job.CancelRequested || !job.StartedAt.IsZero() {
		t.Fatalf("состояние выполнения не сброшено: %+v", job)
	}

	// Повтор задания, которое уже не failed, отклоняется.
	if err := RetryJob(ctx, store, "dead"); !errors.Is(err, ErrTransitionRejected) {
		t.Fatalf("повтор queued-задания = %v, ожидалась ErrTransitionRejected", err)
	}
	if err := RetryJob(ctx, store, "нет-такого"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("повтор отсутствующего = %v, ожидалась ErrNotFound", err)
	}
}

// TestCapabilitiesUnsupported проверяет честный отказ, когда хранилище не
// реализует необязательные возможности.
func TestCapabilitiesUnsupported(t *testing.T) {
	shim := storeOnly{Store: newMemoryStore()}
	ctx := context.Background()
	if err := ReportProgress(ctx, shim, "job-1", 10, "шаг"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("ReportProgress = %v, ожидалась ErrUnsupported", err)
	}
	if _, err := ListDLQ(ctx, shim, 2, 0); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("ListDLQ = %v, ожидалась ErrUnsupported", err)
	}
	if err := RetryJob(ctx, shim, "job-1"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("RetryJob = %v, ожидалась ErrUnsupported", err)
	}
}

// TestJobsOperationsConcurrent проверяет потокобезопасность новых операций:
// одновременные отчёты о прогрессе, чтение DLQ и повторы не должны вызывать гонок
// (проверяется под -race).
func TestJobsOperationsConcurrent(t *testing.T) {
	store := newMemoryStore()
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("job-%d", i)
		if err := store.Create(ctx, Job{ID: id}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if _, err := store.TakeQueued(ctx); err != nil {
			t.Fatalf("TakeQueued: %v", err)
		}
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = ReportProgress(ctx, store, fmt.Sprintf("job-%d", n%4), n, "шаг")
			}
		}(i)
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if _, err := ListDLQ(ctx, store, 2, 10); err != nil {
					t.Errorf("ListDLQ: %v", err)
					return
				}
			}
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = RetryJob(ctx, store, fmt.Sprintf("job-%d", n))
			}
		}(i)
	}
	wg.Wait()
}
