package research

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
	rm "lead-gen/contracts/agent-orchestration/researchmission"
)

type healthRepository struct {
	ports.ResearchRepository
	mu        sync.Mutex
	probeErr  error
	scanErr   error
	blockStep bool
	entered   chan struct{}
	once      sync.Once
}

func (r *healthRepository) CheckHealth(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.probeErr
}
func (r *healthRepository) Ready(context.Context, int) ([]ports.ResearchRef, error) {
	if r.scanErr != nil {
		return nil, r.scanErr
	}
	if r.blockStep {
		return []ports.ResearchRef{{Scope: rm.Scope{TenantID: "tenant", PurposeID: "purpose"}, ID: "mission"}}, nil
	}
	return nil, nil
}
func (r *healthRepository) Update(ctx context.Context, _ rm.Scope, _ string, _ func(*domain.ResearchMission) error) (*domain.ResearchMission, error) {
	r.once.Do(func() { close(r.entered) })
	<-ctx.Done()
	return nil, ctx.Err()
}
func (r *healthRepository) Get(ctx context.Context, _ rm.Scope, _ string) (*domain.ResearchMission, error) {
	return nil, ctx.Err()
}
func awaitWorkerHealth(t *testing.T, s *Service) {
	t.Helper()
	until := time.Now().Add(time.Second)
	for time.Now().Before(until) {
		if s.CheckHealth(context.Background()) == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("worker не стал готовым")
}
func TestResearchHealthRequiresActualWorkerAndCurrentStore(t *testing.T) {
	repo := &healthRepository{}
	s := &Service{config: Config{Repository: repo}}
	if err := s.CheckHealth(context.Background()); err == nil {
		t.Fatal("config presence объявлен здоровым worker")
	}
	if err := s.RunReady(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckHealth(context.Background()); err == nil {
		t.Fatal("ручной scan объявлен фоновым worker")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.RunWorker(ctx, 1, nil) }()
	awaitWorkerHealth(t, s)
	if err := s.RunWorker(context.Background(), 1, nil); !errors.Is(err, rm.ErrConflict) {
		t.Fatalf("второй worker принят: %v", err)
	}
	repo.mu.Lock()
	repo.probeErr = errors.New("PostgreSQL недоступен")
	repo.mu.Unlock()
	if err := s.CheckHealth(context.Background()); err == nil {
		t.Fatal("сбой DB скрыт")
	}
	repo.mu.Lock()
	repo.probeErr = nil
	repo.mu.Unlock()
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := s.CheckHealth(context.Background()); err == nil {
		t.Fatal("остановленный worker готов")
	}
}
func TestResearchHealthLongBoundedStepIsHealthyButExpiredWorkIsNot(t *testing.T) {
	repo := &healthRepository{blockStep: true, entered: make(chan struct{})}
	s := &Service{config: Config{Repository: repo}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.RunWorker(ctx, 1, nil) }()
	select {
	case <-repo.entered:
	case <-time.After(time.Second):
		t.Fatal("worker не начал реальный Step")
	}
	s.workerMu.Lock()
	remaining := time.Until(s.worker.deadline)
	// Имитируем 121sec работы без реального длительного ожидания.
	s.worker.deadline = s.worker.deadline.Add(-121 * time.Second)
	s.workerMu.Unlock()
	if remaining < 300*time.Second {
		t.Fatalf("длинный Step имеет слишком короткое окно здоровья: %v", remaining)
	}
	if err := s.CheckHealth(context.Background()); err != nil {
		t.Fatalf("120sec действие ошибочно stale: %v", err)
	}
	s.workerMu.Lock()
	s.worker.deadline = time.Now().Add(-time.Second)
	s.workerMu.Unlock()
	if err := s.CheckHealth(context.Background()); err == nil {
		t.Fatal("зависший сверх bound Step объявлен healthy")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
func TestResearchHealthFailedQueueScanCannotBecomeReady(t *testing.T) {
	repo := &healthRepository{scanErr: errors.New("очередь недоступна")}
	s := &Service{config: Config{Repository: repo}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reported := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- s.RunWorker(ctx, 1, func(error) {
			select {
			case reported <- struct{}{}:
			default:
			}
		})
	}()
	select {
	case <-reported:
	case <-time.After(time.Second):
		t.Fatal("scan не выполнен")
	}
	if err := s.CheckHealth(context.Background()); err == nil {
		t.Fatal("ошибка очереди скрыта при успешном probe")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
