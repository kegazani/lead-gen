package contentproduction

import (
	"context"
	"time"

	"lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/readiness"
)

// RoleReadinessReporter — владельческий контракт readiness ролей контента.
type RoleReadinessReporter interface {
	RoleReadiness(context.Context, composition.Role) readiness.Snapshot
}

type readinessReporter struct {
	evaluator *readiness.Evaluator
	profiles  map[composition.Role]readiness.Profile
}

func newReadinessReporter(p Persistence, renderer any, relayConfigured bool) *readinessReporter {
	var dbProbe readiness.Probe
	if p.Ping != nil {
		dbProbe = func(ctx context.Context) (readiness.ProbeResult, error) {
			if err := p.Ping(ctx); err != nil {
				return readiness.ProbeResult{}, err
			}
			return readiness.ProbeResult{Status: readiness.StatusUp, Reason: readiness.ReasonNone}, nil
		}
	}
	var rendererProbe readiness.Probe
	if checker, ok := renderer.(interface{ ReadinessCheck(context.Context) error }); ok {
		rendererProbe = func(ctx context.Context) (readiness.ProbeResult, error) {
			if err := checker.ReadinessCheck(ctx); err != nil {
				return readiness.ProbeResult{}, err
			}
			return readiness.ProbeResult{Status: readiness.StatusUp, Reason: readiness.ReasonNone}, nil
		}
	}
	database := readiness.CheckSpec{ID: "postgres", Key: "content-production.postgres", Required: true, Probe: dbProbe}
	outbox := readiness.CheckSpec{ID: "outbox", Key: "content-production.outbox", Required: true, Probe: dbProbe}
	transport := readiness.CheckSpec{ID: "messaging-publisher", Key: "content-production.messaging", Required: false}
	if relayConfigured {
		// Релею нельзя выполнить безопасную проверку через Publish: до отдельного
		// пассивного контракта статус транспорта остаётся unknown.
		transport.Probe = nil
	}
	return &readinessReporter{
		evaluator: readiness.New(readiness.Config{Timeout: 2 * time.Second, TTL: time.Second}),
		profiles: map[composition.Role]readiness.Profile{
			composition.RoleAPI: {Capability: "content-api", Checks: []readiness.CheckSpec{
				database,
				outbox,
				{ID: "renderer", Key: "content-production.renderer", Probe: rendererProbe},
				{ID: "llm", Key: "content-production.llm"},
				{ID: "site-collector", Key: "content-production.site-collector"},
				{ID: "document-parser", Key: "content-production.document-parser"},
				{ID: "plagiarism-source", Key: "content-production.plagiarism-source"},
			}},
			composition.RoleWorker: {Capability: "content-render-worker", Checks: []readiness.CheckSpec{database, outbox, {ID: "renderer", Key: "content-production.renderer", Required: true, Probe: rendererProbe}, transport}},
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
