package requestfulfillment

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/request-fulfillment/internal/adapters/persistence/postgres"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/researchbridge"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/backend/platform/readiness"
	platformworkflow "lead-gen/backend/platform/workflow"
	"lead-gen/contracts/agent-orchestration/researchmission"
)

type temporalCheckFunc func(context.Context) error

func (f temporalCheckFunc) CheckHealth(ctx context.Context) error { return f(ctx) }

func temporalUp(context.Context) error { return nil }

func TestReadinessProfileFailsClosedForUnavailableDependencies(t *testing.T) {
	ctx := context.Background()
	runtime := platformworkflow.NewAvailability()
	profile := readinessProfile(nil, runtime, temporalCheckFunc(func(context.Context) error { return errors.New("secret endpoint") }), nil, ExecutionProtocolV1, false)
	got := readiness.New(readiness.Config{Timeout: 50 * time.Millisecond}).Evaluate(ctx, profile)
	if got.Ready || got.Checks[0].Reason != readiness.ReasonProbeNotConfigured || got.Checks[1].Status != readiness.StatusDown || got.Checks[2].Reason != readiness.ReasonProbeNotConfigured {
		t.Fatalf("nil DB/worker and unavailable Temporal must not be ready: %#v", got)
	}
}

func TestReadinessProfileChecksTemporalWithoutWorkflowOperations(t *testing.T) {
	runtime := platformworkflow.NewAvailability()
	runtime.MarkReady()
	called := false
	temporal := temporalCheckFunc(func(context.Context) error {
		called = true
		return errors.New("temporal endpoint and credentials are private")
	})
	got := readiness.New(readiness.Config{Timeout: 100 * time.Millisecond}).Evaluate(context.Background(), readinessProfile(nil, runtime, temporal, nil, ExecutionProtocolV1, false))
	if !called || got.Checks[1].Status != readiness.StatusDown || got.Checks[1].Reason != readiness.ReasonProbeFailed {
		t.Fatalf("Temporal health status was not checked safely: called=%v snapshot=%#v", called, got)
	}
}

func TestReadinessProfileChecksActualPostgresPool(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), "postgres://invalid:invalid@127.0.0.1:1/readiness?connect_timeout=1")
	if err != nil {
		t.Fatalf("создание пула: %v", err)
	}
	defer pool.Close()

	runtime := platformworkflow.NewAvailability()
	runtime.MarkReady()
	repository := postgres.NewRepository(pool)
	workerActive := &atomic.Bool{}
	workerActive.Store(true)
	got := readiness.New(readiness.Config{Timeout: 100 * time.Millisecond}).Evaluate(context.Background(), readinessProfile(repository, runtime, temporalCheckFunc(temporalUp), workerActive, ExecutionProtocolV1, false))
	if got.Ready || got.Checks[0].Status != readiness.StatusDown || got.Checks[0].Reason != readiness.ReasonProbeFailed {
		t.Fatalf("недоступный PostgreSQL pool не отражён: %#v", got)
	}
}

func TestReadinessOptionalCallDependencyIsVisibleWithoutBlocking(t *testing.T) {
	runtime := platformworkflow.NewAvailability()
	runtime.MarkReady()
	workerActive := &atomic.Bool{}
	workerActive.Store(true)
	profile := readinessProfile(nil, runtime, temporalCheckFunc(temporalUp), workerActive, ExecutionProtocolV1, true)
	profile.Checks[0].Probe = func(context.Context) (readiness.ProbeResult, error) {
		return readiness.ProbeResult{Status: readiness.StatusUp, Reason: readiness.ReasonNone}, nil
	}
	got := readiness.New(readiness.Config{}).Evaluate(context.Background(), profile)
	if !got.Ready || got.Status != readiness.StatusDegraded {
		t.Fatalf("optional dependency должна быть degraded без блокировки готовности: %#v", got)
	}
	check := got.Checks[3]
	if check.Required || check.Status != readiness.StatusUnknown || check.Reason != readiness.ReasonProbeNotConfigured {
		t.Fatalf("optional call probe должна остаться явно unknown: %#v", check)
	}
}

func TestV2ReadinessRemainsUnknownWithoutDispatcher(t *testing.T) {
	runtime := platformworkflow.NewAvailability()
	runtime.MarkReady()
	workerActive := &atomic.Bool{}
	workerActive.Store(true)
	got := readiness.New(readiness.Config{}).Evaluate(context.Background(), readinessProfile(nil, runtime, temporalCheckFunc(temporalUp), workerActive, ExecutionProtocolV2, false))
	last := got.Checks[len(got.Checks)-1]
	if got.Ready || last.ID != "execution-v2-dispatcher" || last.Status != readiness.StatusUnknown {
		t.Fatalf("v2 without running dispatcher was reported ready: %#v", got)
	}
}

type researchHealthMission struct {
	researchmission.Port
	unavailable atomic.Bool
}

func (m *researchHealthMission) CheckHealth(context.Context) error {
	if m.unavailable.Load() {
		return errors.New("worker исследования не работает")
	}
	return nil
}

type researchHealthRepository struct {
	ports.RequestRepository
	unavailable atomic.Bool
}

func (r *researchHealthRepository) Ping(context.Context) error {
	if r.unavailable.Load() {
		return errors.New("БД недоступна")
	}
	return nil
}

func (r *researchHealthRepository) ListByStatus(ctx context.Context, _ domain.RequestStatus) ([]*domain.Request, error) {
	return nil, r.Ping(ctx)
}

func TestResearchReadinessUsesSelectedExecutionMode(t *testing.T) {
	repository := &researchHealthRepository{}
	mission := &researchHealthMission{}
	worker := &researchWorkerHealth{}
	worker.active.Store(true)
	worker.observe(nil)
	cfg := Config{Research: &ResearchConfig{OnlyResearch: true, Missions: mission}, readinessResearchWorker: worker}
	evaluator := readiness.New(readiness.Config{})
	profile := requestReadinessProfile(repository, cfg, ExecutionProtocolV2)
	got := evaluator.Evaluate(context.Background(), profile)
	if !got.Ready || len(got.Checks) != 3 {
		t.Fatalf("OnlyResearch должен проверять БД и оба действующих worker без Temporal: %#v", got)
	}
	for _, check := range got.Checks {
		if !check.Required || check.Status != readiness.StatusUp || check.ID == "temporal" || check.ID == "execution-v2-dispatcher" {
			t.Fatalf("неверная зависимость выбранного режима: %#v", check)
		}
	}
	mission.unavailable.Store(true)
	if got = evaluator.Evaluate(context.Background(), profile); got.Ready || got.Checks[2].Status != readiness.StatusDown {
		t.Fatalf("отключённый mission worker должен блокировать готовность: %#v", got)
	}
	mission.unavailable.Store(false)
	repository.unavailable.Store(true)
	if got = evaluator.Evaluate(context.Background(), profile); got.Ready || got.Checks[0].Status != readiness.StatusDown {
		t.Fatalf("ошибка БД должна блокировать готовность исследования: %#v", got)
	}
	repository.unavailable.Store(false)
	cfg.Research.OnlyResearch = false
	got = evaluator.Evaluate(context.Background(), requestReadinessProfile(repository, cfg, ExecutionProtocolV1))
	if got.Ready || len(got.Checks) != 6 || got.Checks[1].ID != "temporal" || got.Checks[2].ID != "temporal-worker" {
		t.Fatalf("смешанный режим должен сохранить обязательные проверки Temporal: %#v", got)
	}
}

func TestResearchReadinessCannotUseConfigurationAsWorkerProof(t *testing.T) {
	cfg := Config{Research: &ResearchConfig{OnlyResearch: true}}
	got := readiness.New(readiness.Config{}).Evaluate(context.Background(), requestReadinessProfile(&researchHealthRepository{}, cfg, ExecutionProtocolV1))
	if got.Ready || got.Checks[1].Reason != readiness.ReasonProbeNotConfigured || got.Checks[2].Reason != readiness.ReasonProbeNotConfigured {
		t.Fatalf("наличие ResearchConfig не подтверждает работу worker: %#v", got)
	}
	health := &researchWorkerHealth{}
	health.active.Store(true)
	health.observe(nil)
	health.lastScan.Store(time.Now().Add(-2 * researchScanFreshness).UnixNano())
	result, err := researchWorkerProbe(health)(context.Background())
	if err != nil || result.Status != readiness.StatusDown {
		t.Fatalf("зависший проход не должен оставаться готовым: %#v %v", result, err)
	}
}

func TestResearchRequestWorkerReadinessTracksRealLoop(t *testing.T) {
	repository := &researchHealthRepository{}
	health := &researchWorkerHealth{}
	service := &Service{
		slice:  &Slice{researchReconciler: researchbridge.NewReconciler(repository, nil, nil, nil, nil, nil)},
		logger: observ.NewLogger(), readinessResearchWorker: health,
	}
	probe := researchWorkerProbe(health)
	waitFor := func(want readiness.Status) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			result, err := probe(context.Background())
			if err == nil && result.Status == want {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("worker не перешёл в готовность %s", want)
	}
	waitFor(readiness.StatusDown)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- service.runResearchReconciler(ctx) }()
	waitFor(readiness.StatusUp)
	repository.unavailable.Store(true)
	waitFor(readiness.StatusDown)
	repository.unavailable.Store(false)
	waitFor(readiness.StatusUp)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker не остановился")
	}
	waitFor(readiness.StatusDown)
}
