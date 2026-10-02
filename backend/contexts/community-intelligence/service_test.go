package communityintelligence

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	persistence "lead-gen/backend/contexts/community-intelligence/internal/adapters/persistence/inmemory"
	queueinmemory "lead-gen/backend/contexts/community-intelligence/internal/adapters/queue/inmemory"
	"lead-gen/backend/contexts/community-intelligence/internal/adapters/telegram"
	"lead-gen/backend/contexts/community-intelligence/internal/app/command"
	"lead-gen/backend/contexts/community-intelligence/internal/domain"
	"lead-gen/backend/contexts/community-intelligence/internal/ports"
	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/observability/observ"
)

type testClock struct{ now time.Time }

func (c testClock) Now() time.Time { return c.now }

func testObservability() (observ.Logger, observ.Metrics, observ.Tracer) {
	return observ.NewLoggerWithWriter(io.Discard), observ.NewMetrics(), observ.NewTracer(ServiceID)
}

func testSource(t *testing.T, id domain.CommunityID) domain.CommunitySource {
	t.Helper()
	source, err := domain.NewCommunitySource(domain.NewCommunitySourceParams{
		ID:         id,
		Title:      "Сообщество " + id.String(),
		Kind:       domain.SourceUserAccount,
		Authorized: true,
	})
	if err != nil {
		t.Fatalf("NewCommunitySource: %v", err)
	}
	return source
}

func newService(t *testing.T, cfg Config) *Service {
	t.Helper()
	logger, metrics, tracer := testObservability()
	cfg.Logger, cfg.Metrics, cfg.Tracer = logger, metrics, tracer
	service, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return service
}

// runRole запускает роль сервиса в отдельной горутине.
func runRole(t *testing.T, service *Service, role composition.Role, ctx context.Context) {
	t.Helper()
	runner, ok := service.RoleRunner(role)
	if !ok {
		t.Fatalf("роль %q не объявлена", role)
	}
	go func() { _ = runner.Run(ctx) }()
}

func TestServiceDeclaresCatalogRoles(t *testing.T) {
	service := newService(t, Config{})
	if service.ID() != ServiceID {
		t.Fatalf("ID = %q", service.ID())
	}
	for _, role := range []composition.Role{composition.RoleWorker, composition.RoleSchedule} {
		if _, ok := service.RoleRunner(role); !ok {
			t.Fatalf("роль %q не объявлена", role)
		}
	}
	for _, role := range []composition.Role{composition.RoleAPI, composition.RoleChains, composition.RoleIngest, composition.RoleSender} {
		if _, ok := service.RoleRunner(role); ok {
			t.Fatalf("роль %q не должна быть объявлена каталогом", role)
		}
	}
}

func TestDefaultReaderIsUnavailable(t *testing.T) {
	registry := persistence.NewRegistry()
	service := newService(t, Config{Registry: registry})
	if err := registry.Register("t-1", testSource(t, "c-1"), 0); err != nil {
		t.Fatalf("Register: %v", err)
	}
	from := time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)
	_, err := service.Slice().ReadCommunity.Handle(context.Background(), command.ReadCommunity{
		CommunityID: "c-1",
		TenantID:    "t-1",
		From:        from,
		To:          from.Add(time.Hour),
	})
	if !errors.Is(err, ports.ErrSessionRequired) {
		t.Fatalf("ожидался ErrSessionRequired, получено %v", err)
	}
}

func TestScheduleEnqueuesTrackedCommunities(t *testing.T) {
	registry := persistence.NewRegistry()
	if err := registry.Register("t-1", testSource(t, "c-1"), 0); err != nil {
		t.Fatalf("Register: %v", err)
	}
	queue := queueinmemory.NewBuffered(4)
	now := time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)
	service := newService(t, Config{
		Registry: registry,
		Queue:    queue,
		Clock:    testClock{now: now},
		Interval: 20 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runRole(t, service, composition.RoleSchedule, ctx)

	dequeueCtx, dequeueCancel := context.WithTimeout(ctx, time.Second)
	defer dequeueCancel()
	cmd, ok := queue.Dequeue(dequeueCtx)
	if !ok {
		t.Fatal("роль schedule не поставила команду чтения")
	}
	if cmd.CommunityID != "c-1" || cmd.TenantID != "t-1" {
		t.Fatalf("команда сбора: %+v", cmd)
	}
	if !cmd.To.Equal(now) {
		t.Fatalf("верхняя граница окна = %s, ожидалось %s", cmd.To, now)
	}
}

func TestWorkerBuildsDigest(t *testing.T) {
	now := time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)
	registry := persistence.NewRegistry()
	if err := registry.Register("t-1", testSource(t, "c-1"), 0); err != nil {
		t.Fatalf("Register: %v", err)
	}
	reader := telegram.NewDeterministic()
	reader.SetCommunity("c-1", 100, true, []telegram.Message{
		{ID: "m-1", Author: "author-1", Text: "нужна цена", PostedAt: now.Add(-time.Hour)},
		{ID: "m-2", Author: "author-2", Text: "нужна цена", PostedAt: now.Add(-time.Minute)},
	})
	digests := persistence.New()
	queue := queueinmemory.NewBuffered(2)
	service := newService(t, Config{
		Registry: registry,
		Reader:   reader,
		Digests:  digests,
		Queue:    queue,
		Clock:    testClock{now: now},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runRole(t, service, composition.RoleWorker, ctx)

	if err := queue.Enqueue(ctx, ports.ReadCommand{
		CommunityID: "c-1",
		TenantID:    "t-1",
		From:        now.Add(-24 * time.Hour),
		To:          now,
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	waitForDigests(t, digests, "c-1", func(stored []*domain.CommunityDigest) bool {
		return len(stored) == 1 && len(stored[0].Topics()) == 1 && stored[0].Topics()[0].Mentions() == 2
	})
}

func TestScheduleAndWorkerTogether(t *testing.T) {
	now := time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)
	registry := persistence.NewRegistry()
	if err := registry.Register("t-1", testSource(t, "c-1"), 0); err != nil {
		t.Fatalf("Register: %v", err)
	}
	reader := telegram.NewDeterministic()
	reader.SetCommunity("c-1", 100, true, []telegram.Message{
		{ID: "m-1", Author: "author-1", Text: "нужна цена", PostedAt: now.Add(-time.Hour)},
	})
	digests := persistence.New()
	queue := queueinmemory.NewBuffered(4)
	service := newService(t, Config{
		Registry: registry,
		Reader:   reader,
		Digests:  digests,
		Queue:    queue,
		Clock:    testClock{now: now},
		Interval: 20 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runRole(t, service, composition.RoleSchedule, ctx)
	runRole(t, service, composition.RoleWorker, ctx)

	waitForDigests(t, digests, "c-1", func(stored []*domain.CommunityDigest) bool {
		return len(stored) > 0
	})
}

// waitForDigests ждёт, пока хранилище удовлетворит условию, либо падает по
// таймауту.
func waitForDigests(t *testing.T, digests *persistence.Repository, community domain.CommunityID, ready func([]*domain.CommunityDigest) bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		stored, err := digests.ListByCommunity(context.Background(), community)
		if err == nil && ready(stored) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("выжимка не построена в отведённое время")
}
