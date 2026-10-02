package salesworkflow

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"lead-gen/backend/contexts/sales-workflow/internal/app/command"
	composition "lead-gen/backend/platform/composition"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
	"lead-gen/backend/platform/observability/observ"
)

// fakePublisher считает доставленные сообщения и сохраняет ключи маршрутизации.
type fakePublisher struct {
	mu   sync.Mutex
	keys []string
}

func (p *fakePublisher) Publish(_ context.Context, _, routingKey string, _ []byte, _ map[string]string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.keys = append(p.keys, routingKey)
	return nil
}

func (p *fakePublisher) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.keys)
}

func TestNewRequiresObservability(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("без логгера сервис собран")
	}
	if _, err := New(Config{Logger: observ.NewLoggerWithWriter(io.Discard)}); err == nil {
		t.Fatal("без метрик сервис собран")
	}
	if _, err := New(Config{
		Logger:  observ.NewLoggerWithWriter(io.Discard),
		Metrics: observ.NewMetrics(),
	}); err == nil {
		t.Fatal("без трейсера сервис собран")
	}
}

func TestRoleRunnerMapping(t *testing.T) {
	service := newService(t, Config{})
	if service.ID() != ServiceID {
		t.Fatalf("ID %q", service.ID())
	}
	for _, role := range []composition.Role{composition.RoleAPI, composition.RoleIngest, composition.RoleWorker} {
		if _, ok := service.RoleRunner(role); !ok {
			t.Fatalf("роль %q не реализована", role)
		}
	}
	for _, role := range []composition.Role{composition.RoleSender, composition.RoleChains, composition.RoleSchedule} {
		if _, ok := service.RoleRunner(role); ok {
			t.Fatalf("роль %q не должна быть реализована", role)
		}
	}
}

func TestWorkerPublishesEvents(t *testing.T) {
	publisher := &fakePublisher{}
	service := newService(t, Config{
		Publisher: publisher,
		Interval:  time.Millisecond,
	})

	if _, err := service.slice.CreateDeal.Handle(context.Background(), command.CreateDeal{
		DealID:           "deal-1",
		TenantID:         "tenant-1",
		CompanyRef:       "company-1",
		ExternalRef:      "crm-deal-1",
		ExternalRevision: 1,
		Actor:            "operator-1",
	}); err != nil {
		t.Fatalf("CreateDeal: %v", err)
	}

	runner, ok := service.RoleRunner(composition.RoleWorker)
	if !ok {
		t.Fatal("роль worker не реализована")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()

	deadline := time.After(2 * time.Second)
	for publisher.count() == 0 {
		select {
		case <-deadline:
			cancel()
			<-done
			t.Fatal("relay не опубликовал событие")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("worker завершился с ошибкой: %v", err)
	}
	if publisher.keys[0] != "deal-state.v1" {
		t.Fatalf("ключ маршрутизации %q", publisher.keys[0])
	}
}

func TestWorkerWithoutPublisherFails(t *testing.T) {
	service := newService(t, Config{})
	runner, _ := service.RoleRunner(composition.RoleWorker)
	if err := runner.Run(context.Background()); err == nil {
		t.Fatal("worker без Publisher не сообщил об ошибке")
	}
}

// newService собирает сервис с обязательной наблюдаемостью.
func newService(t *testing.T, cfg Config) *Service {
	t.Helper()
	if cfg.Journal == nil {
		cfg.Journal = platformoutbox.NewMemoryJournal()
	}
	if cfg.Clock == nil {
		cfg.Clock = &fixedClock{now: testNow}
	}
	if cfg.IDs == nil {
		cfg.IDs = &seqIDs{}
	}
	cfg.Logger = observ.NewLoggerWithWriter(io.Discard)
	cfg.Metrics = observ.NewMetrics()
	cfg.Tracer = observ.NewTracer(ServiceID)
	service, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return service
}
