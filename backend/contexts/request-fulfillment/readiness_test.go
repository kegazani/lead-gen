package requestfulfillment

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/request-fulfillment/internal/adapters/persistence/postgres"
	"lead-gen/backend/platform/readiness"
	platformworkflow "lead-gen/backend/platform/workflow"
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
