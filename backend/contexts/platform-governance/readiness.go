package platformgovernance

import (
	"context"
	"encoding/json"
	"net/http"

	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/readiness"
)

// Readiness возвращает владельческий профиль роли, не раскрывая адреса или ошибки зависимостей.
func (s *Service) Readiness(ctx context.Context, role composition.Role) readiness.Snapshot {
	db := readiness.Probe(nil)
	if s.pool != nil {
		db = func(ctx context.Context) (readiness.ProbeResult, error) {
			if err := s.pool.Ping(ctx); err != nil {
				return readiness.ProbeResult{Status: readiness.StatusDown, Reason: readiness.ReasonProbeFailed}, nil
			}
			return readiness.ProbeResult{Status: readiness.StatusUp, Reason: readiness.ReasonNone}, nil
		}
	}
	switch role {
	case composition.RoleAPI:
		return s.readiness.Evaluate(ctx, readiness.Profile{Capability: "platform-governance-api", Checks: []readiness.CheckSpec{
			{ID: "tenant_repository", Key: "platform-governance.database", Required: true, Probe: db},
			{ID: "portal_sessions", Key: "platform-governance.database", Required: true, Probe: db},
			{ID: "analytics_dashboard", Key: "platform-governance.analytics", Required: false},
		}})
	case composition.RoleWorker:
		publisher := readiness.Probe(nil)
		if s.publisher != nil {
			// Publisher подтверждён только наличием безопасного health-порта. Publish
			// не вызывается: это создало бы побочный эффект в брокере.
			if probe, ok := s.publisher.(interface {
				Readiness(context.Context) error
			}); ok {
				publisher = func(ctx context.Context) (readiness.ProbeResult, error) {
					if err := probe.Readiness(ctx); err != nil {
						return readiness.ProbeResult{Status: readiness.StatusDown, Reason: readiness.ReasonProbeFailed}, nil
					}
					return readiness.ProbeResult{Status: readiness.StatusUp, Reason: readiness.ReasonNone}, nil
				}
			}
		}
		return s.readiness.Evaluate(ctx, readiness.Profile{Capability: "platform-governance-outbox-worker", Checks: []readiness.CheckSpec{
			{ID: "outbox_repository", Key: "platform-governance.database", Required: true, Probe: db},
			{ID: "rabbit_publisher", Key: "platform-governance.rabbitmq", Required: true, Probe: publisher},
			s.relayLoop.check(),
		}})
	default:
		return readiness.Snapshot{Capability: "platform-governance-role", Status: readiness.StatusUnknown}
	}
}

func writeReadiness(w http.ResponseWriter, snapshot readiness.Snapshot) {
	status := http.StatusOK
	if !snapshot.Ready {
		status = http.StatusServiceUnavailable
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(snapshot)
}
