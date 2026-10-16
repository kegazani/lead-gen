package requestfulfillment

import (
	"context"
	"sync/atomic"
	"time"

	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/backend/platform/readiness"
	platformworkflow "lead-gen/backend/platform/workflow"
	"lead-gen/contracts/agent-orchestration/researchmission"
)

type readinessPinger interface {
	Ping(context.Context) error
}

type temporalHealthChecker interface {
	CheckHealth(context.Context) error
}

// requestReadinessProfile проверяет только контуры, выбранные составом сервиса.
// В смешанном режиме обязательны оба пути; OnlyResearch не зависит от Temporal.
func requestReadinessProfile(repository ports.RequestRepository, cfg Config, protocol ExecutionProtocol) readiness.Profile {
	profile := readinessProfile(repository, cfg.Runtime, cfg.WorkflowClient, cfg.readinessWorkerActive, protocol, cfg.CallContract != nil)
	if cfg.Research == nil {
		return profile
	}
	if cfg.Research.OnlyResearch {
		profile.Checks = []readiness.CheckSpec{{ID: "postgres", Key: "request-fulfillment/postgres", Required: true, Probe: postgresProbe(repository)}}
	}
	missionHealth, _ := cfg.Research.Missions.(researchmission.HealthChecker)
	profile.Checks = append(profile.Checks,
		readiness.CheckSpec{ID: "research-request-worker", Key: "request-fulfillment/research-request-worker", Required: true, Probe: researchWorkerProbe(cfg.readinessResearchWorker)},
		readiness.CheckSpec{ID: "research-mission-worker", Key: "request-fulfillment/research-mission-worker", Required: true, Probe: researchMissionProbe(missionHealth)},
	)
	return profile
}

// researchWorkerHealth отражает наблюдённый проход очереди, а не наличие config.
// Зависший проход тоже снимает готовность, даже если goroutine ещё существует.
type researchWorkerHealth struct {
	active   atomic.Bool
	healthy  atomic.Bool
	lastScan atomic.Int64
}

const researchScanFreshness = 30 * time.Second

func (h *researchWorkerHealth) observe(err error) {
	h.healthy.Store(err == nil)
	h.lastScan.Store(time.Now().UnixNano())
}

func researchWorkerProbe(health *researchWorkerHealth) readiness.Probe {
	if health == nil {
		return nil
	}
	return func(context.Context) (readiness.ProbeResult, error) {
		lastScan := time.Unix(0, health.lastScan.Load())
		if !health.active.Load() || !health.healthy.Load() || time.Since(lastScan) > researchScanFreshness {
			return readiness.ProbeResult{Status: readiness.StatusDown, Reason: readiness.ReasonProbeFailed}, nil
		}
		return readiness.ProbeResult{Status: readiness.StatusUp, Reason: readiness.ReasonNone}, nil
	}
}

func researchMissionProbe(health researchmission.HealthChecker) readiness.Probe {
	if health == nil {
		return nil
	}
	return func(ctx context.Context) (readiness.ProbeResult, error) {
		if err := health.CheckHealth(ctx); err != nil {
			return readiness.ProbeResult{}, err
		}
		return readiness.ProbeResult{Status: readiness.StatusUp, Reason: readiness.ReasonNone}, nil
	}
}

func readinessProfile(repository ports.RequestRepository, runtime *platformworkflow.Availability, temporal temporalHealthChecker, workerActive *atomic.Bool, protocol ExecutionProtocol, callConfigured bool) readiness.Profile {
	profile := readiness.Profile{Capability: "request-fulfillment-api", Checks: []readiness.CheckSpec{
		{ID: "postgres", Key: "request-fulfillment/postgres", Required: true, Probe: postgresProbe(repository)},
		{ID: "temporal", Key: "request-fulfillment/temporal", Required: true, Probe: temporalProbe(runtime, temporal)},
		{ID: "temporal-worker", Key: "request-fulfillment/temporal-worker", Required: true, Probe: workerProbe(workerActive)},
		{ID: "call-provider", Key: "request-fulfillment/call-provider", Probe: optionalProbe(callConfigured)},
	}}
	if protocol == ExecutionProtocolV2 {
		// Durable intent dispatcher is not currently running in this context.
		profile.Checks = append(profile.Checks, readiness.CheckSpec{ID: "execution-v2-dispatcher", Key: "request-fulfillment/execution-v2-dispatcher", Required: true})
	}
	return profile
}

func postgresProbe(repository ports.RequestRepository) readiness.Probe {
	pinger, ok := repository.(readinessPinger)
	if !ok || pinger == nil {
		return nil
	}
	return func(ctx context.Context) (readiness.ProbeResult, error) {
		if err := pinger.Ping(ctx); err != nil {
			return readiness.ProbeResult{}, err
		}
		return readiness.ProbeResult{Status: readiness.StatusUp, Reason: readiness.ReasonNone}, nil
	}
}

func temporalProbe(runtime *platformworkflow.Availability, temporal temporalHealthChecker) readiness.Probe {
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

func optionalProbe(configured bool) readiness.Probe {
	if !configured {
		return nil
	}
	// У контракта звонков нет подтверждённой безопасной health-пробы.
	return nil
}
