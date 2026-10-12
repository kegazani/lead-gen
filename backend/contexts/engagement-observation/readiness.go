package engagementobservation

import (
	"context"
	"time"

	"lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/readiness"
	"lead-gen/backend/platform/transport/amqp"
)

// RoleReadinessReporter — владельческий контракт readiness ingest и worker.
type RoleReadinessReporter interface {
	RoleReadiness(context.Context, composition.Role) readiness.Snapshot
}

type readinessReporter struct {
	evaluator *readiness.Evaluator
	profiles  map[composition.Role]readiness.Profile
}

type passiveReadiness interface {
	ReadinessCheck(context.Context) error
}

func newReadinessReporter(p Persistence, consumer amqp.Consumer, publisher amqp.Publisher, deduper any) *readinessReporter {
	var dbProbe readiness.Probe
	if p.Ping != nil {
		dbProbe = func(ctx context.Context) (readiness.ProbeResult, error) {
			if err := p.Ping(ctx); err != nil {
				return readiness.ProbeResult{}, err
			}
			return readiness.ProbeResult{Status: readiness.StatusUp, Reason: readiness.ReasonNone}, nil
		}
	}
	probe := func(dependency any) readiness.Probe {
		checker, ok := dependency.(passiveReadiness)
		if !ok {
			return nil
		}
		return func(ctx context.Context) (readiness.ProbeResult, error) {
			if err := checker.ReadinessCheck(ctx); err != nil {
				return readiness.ProbeResult{}, err
			}
			return readiness.ProbeResult{Status: readiness.StatusUp, Reason: readiness.ReasonNone}, nil
		}
	}
	database := readiness.CheckSpec{ID: "postgres", Key: "engagement-observation.postgres", Required: true, Probe: dbProbe}
	outbox := readiness.CheckSpec{ID: "outbox", Key: "engagement-observation.outbox", Required: true, Probe: dbProbe}
	return &readinessReporter{
		evaluator: readiness.New(readiness.Config{Timeout: time.Second, TTL: time.Second}),
		profiles: map[composition.Role]readiness.Profile{
			composition.RoleIngest: {Capability: "engagement-ingest", Checks: []readiness.CheckSpec{database, outbox}},
			composition.RoleWorker: {Capability: "engagement-subscriber", Checks: []readiness.CheckSpec{
				database,
				outbox,
				{ID: "message-consumer", Key: "engagement-observation.consumer", Required: true, Probe: probe(consumer)},
				{ID: "inbox-deduper", Key: "engagement-observation.inbox", Required: true, Probe: probe(deduper)},
				{ID: "outbox-publisher", Key: "engagement-observation.publisher", Probe: probe(publisher)},
			}},
		},
	}
}

func (r *readinessReporter) RoleReadiness(ctx context.Context, role composition.Role) readiness.Snapshot {
	profile, ok := r.profiles[role]
	if !ok {
		return readiness.Snapshot{Capability: string(role), Status: readiness.StatusUnknown, Ready: false}
	}
	return r.evaluator.Evaluate(ctx, profile)
}
