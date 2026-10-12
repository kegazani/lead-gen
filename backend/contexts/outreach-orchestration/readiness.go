package outreachorchestration

import (
	"context"
	"time"

	"lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/readiness"
)

// RoleReadinessReporter — владельческий контракт readiness ролей outreach.
type RoleReadinessReporter interface {
	RoleReadiness(context.Context, composition.Role) readiness.Snapshot
}

// ReadinessReport — локальный evaluator для зависимостей outreach.
type ReadinessReport struct {
	evaluator *readiness.Evaluator
	profiles  map[composition.Role]readiness.Profile
}

// NewReadinessReport задаёт зависимости по исполняемой роли. Вызовов команд,
// публикации, проверки ссылок или отправки HTTP-запросов здесь нет.
func NewReadinessReport(p Persistence, publisher any) *ReadinessReport {
	ping := readiness.Probe(nil)
	if p.Ping != nil {
		ping = func(ctx context.Context) (readiness.ProbeResult, error) {
			if err := p.Ping(ctx); err != nil {
				return readiness.ProbeResult{}, err
			}
			return readiness.ProbeResult{Status: readiness.StatusUp, Reason: readiness.ReasonNone}, nil
		}
	}
	storage := readiness.CheckSpec{ID: "postgres", Key: "outreach-orchestration.postgres", Required: true, Probe: ping}
	outbox := readiness.CheckSpec{ID: "outbox", Key: "outreach-orchestration.outbox", Required: true, Probe: ping}
	messaging := readiness.Probe(nil)
	if checker, ok := publisher.(interface{ ReadinessCheck(context.Context) error }); ok {
		messaging = func(ctx context.Context) (readiness.ProbeResult, error) {
			if err := checker.ReadinessCheck(ctx); err != nil {
				return readiness.ProbeResult{}, err
			}
			return readiness.ProbeResult{Status: readiness.StatusUp, Reason: readiness.ReasonNone}, nil
		}
	}
	return &ReadinessReport{
		evaluator: readiness.New(readiness.Config{Timeout: time.Second, TTL: time.Second}),
		profiles: map[composition.Role]readiness.Profile{
			composition.RoleAPI: {Capability: "outreach-api", Checks: []readiness.CheckSpec{
				storage,
				outbox,
				{ID: "access-decision", Key: "outreach.access-decision", Required: true},
				{ID: "risk-facts", Key: "outreach.risk-facts"},
				{ID: "approved-action-link", Key: "outreach.approved-action-link"},
			}},
			composition.RoleWorker:   {Capability: "approved-action-publication", Checks: []readiness.CheckSpec{storage, outbox, {ID: "messaging-publisher", Key: "outreach.messaging-publisher", Required: true, Probe: messaging}}},
			composition.RoleChains:   {Capability: "outreach-sequences", Checks: []readiness.CheckSpec{storage, outbox}},
			composition.RoleSchedule: {Capability: "outreach-schedule", Checks: nil},
		},
	}
}

// RoleReadiness возвращает readiness только для объявленных ролей; schedule
// реализует каркас без цикла исполнения и поэтому остаётся unknown.
func (r *ReadinessReport) RoleReadiness(ctx context.Context, role composition.Role) readiness.Snapshot {
	profile, ok := r.profiles[role]
	if !ok || len(profile.Checks) == 0 {
		return readiness.Snapshot{Capability: string(role), Status: readiness.StatusUnknown, Ready: false}
	}
	return r.evaluator.Evaluate(ctx, profile)
}
