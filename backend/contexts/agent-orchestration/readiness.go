package agentorchestration

import (
	"context"
	"sync/atomic"

	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/platform/readiness"
)

type runtimeAvailability interface {
	Ready() bool
}

type temporalHealthChecker interface {
	CheckHealth(context.Context) error
}

func readinessProfile(pool *pgxpool.Pool, runtime runtimeAvailability, temporal temporalHealthChecker, workerActive *atomic.Bool) readiness.Profile {
	return readiness.Profile{Capability: "agent-orchestration-api", Checks: []readiness.CheckSpec{
		{ID: "postgres", Key: "agent-orchestration/postgres", Required: true, Probe: postgresProbe(pool)},
		{ID: "temporal", Key: "agent-orchestration/temporal", Required: true, Probe: temporalProbe(runtime, temporal)},
		{ID: "temporal-worker", Key: "agent-orchestration/temporal-worker", Required: true, Probe: workerProbe(workerActive)},
	}}
}

func postgresProbe(pool *pgxpool.Pool) readiness.Probe {
	if pool == nil {
		return nil
	}
	return func(ctx context.Context) (readiness.ProbeResult, error) {
		if err := pool.Ping(ctx); err != nil {
			return readiness.ProbeResult{}, err
		}
		return readiness.ProbeResult{Status: readiness.StatusUp, Reason: readiness.ReasonNone}, nil
	}
}

func temporalProbe(runtime runtimeAvailability, temporal temporalHealthChecker) readiness.Probe {
	if runtime == nil || temporal == nil {
		return nil
	}
	return func(ctx context.Context) (readiness.ProbeResult, error) {
		if !runtime.Ready() {
			return readiness.ProbeResult{Status: readiness.StatusDown, Reason: readiness.ReasonProbeFailed}, nil
		}
		if err := temporal.CheckHealth(ctx); err != nil {
			return readiness.ProbeResult{}, err
		}
		return readiness.ProbeResult{Status: readiness.StatusUp, Reason: readiness.ReasonNone}, nil
	}
}

func workerProbe(active *atomic.Bool) readiness.Probe {
	if active == nil {
		return nil
	}
	return func(context.Context) (readiness.ProbeResult, error) {
		if !active.Load() {
			return readiness.ProbeResult{Status: readiness.StatusDown, Reason: readiness.ReasonProbeFailed}, nil
		}
		return readiness.ProbeResult{Status: readiness.StatusUp, Reason: readiness.ReasonNone}, nil
	}
}
