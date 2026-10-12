package agentorchestration

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/platform/readiness"
)

type readyRuntime bool

func (r readyRuntime) Ready() bool { return bool(r) }

type temporalCheckFunc func(context.Context) error

func (f temporalCheckFunc) CheckHealth(ctx context.Context) error { return f(ctx) }

func temporalUp(context.Context) error { return nil }

func TestReadinessProfileRequiresDatabaseTemporalAndWorker(t *testing.T) {
	got := readiness.New(readiness.Config{Timeout: 50 * time.Millisecond}).Evaluate(context.Background(), readinessProfile(nil, readyRuntime(false), temporalCheckFunc(func(context.Context) error { return errors.New("secret endpoint") }), nil))
	if got.Ready || got.Checks[0].Reason != readiness.ReasonProbeNotConfigured || got.Checks[1].Status != readiness.StatusDown || got.Checks[2].Reason != readiness.ReasonProbeNotConfigured {
		t.Fatalf("nil DB/worker and unavailable Temporal must not be ready: %#v", got)
	}
}

func TestReadinessProfileChecksTemporalHealth(t *testing.T) {
	runtime := readyRuntime(true)
	called := false
	temporal := temporalCheckFunc(func(context.Context) error {
		called = true
		return errors.New("temporal endpoint and credentials are private")
	})
	got := readiness.New(readiness.Config{Timeout: 100 * time.Millisecond}).Evaluate(context.Background(), readinessProfile(nil, runtime, temporal, nil))
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

	workerActive := &atomic.Bool{}
	workerActive.Store(true)
	got := readiness.New(readiness.Config{Timeout: 100 * time.Millisecond}).Evaluate(context.Background(), readinessProfile(pool, readyRuntime(true), temporalCheckFunc(temporalUp), workerActive))
	if got.Ready || got.Checks[0].Status != readiness.StatusDown || got.Checks[0].Reason != readiness.ReasonProbeFailed {
		t.Fatalf("недоступный PostgreSQL pool не отражён: %#v", got)
	}
}
