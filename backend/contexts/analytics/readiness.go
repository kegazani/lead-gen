package analytics

import (
	"context"
	"encoding/json"
	"net/http"

	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/readiness"
)

// Readiness проверяет PostgreSQL адаптеры только для ролей, чьи основные операции
// от них зависят. Проверки не читают проекцию и не изменяют задания.
func (s *Service) Readiness(ctx context.Context, role composition.Role) readiness.Snapshot {
	projection := storeProbe(s.slice.ProjectionStore)
	exports := storeProbe(s.slice.ExportStore)
	switch role {
	case composition.RoleAPI:
		return s.readiness.Evaluate(ctx, readiness.Profile{Capability: "analytics-api", Checks: []readiness.CheckSpec{
			{ID: "api_core", Key: "analytics.api-core", Required: true, Probe: func(context.Context) (readiness.ProbeResult, error) {
				return readiness.ProbeResult{Status: readiness.StatusUp, Reason: readiness.ReasonNone}, nil
			}},
			{ID: "dashboard_projection", Key: "analytics.projection", Required: false, Probe: projection},
			{ID: "report_exports", Key: "analytics.exports", Required: false, Probe: exports},
		}})
	case composition.RoleWorker:
		checks := []readiness.CheckSpec{
			{ID: "dashboard_projection", Key: "analytics.projection", Required: true, Probe: projection},
			{ID: "report_exports", Key: "analytics.exports", Required: true, Probe: exports},
			s.analyticsLoop.check("analytics_event_subscriber", true),
			s.deliveryLoop.check("delivery_event_subscriber", true),
			s.exportLoop.check("report_export_loop", true),
		}
		return s.readiness.Evaluate(ctx, readiness.Profile{Capability: "analytics-worker", Checks: checks})
	default:
		return readiness.Snapshot{Capability: "analytics-role", Status: readiness.StatusUnknown}
	}
}

func storeProbe(store interface{}) readiness.Probe {
	check, ok := store.(interface{ Readiness(context.Context) error })
	if !ok {
		return nil
	}
	return func(ctx context.Context) (readiness.ProbeResult, error) {
		if err := check.Readiness(ctx); err != nil {
			return readiness.ProbeResult{Status: readiness.StatusDown, Reason: readiness.ReasonProbeFailed}, nil
		}
		return readiness.ProbeResult{Status: readiness.StatusUp, Reason: readiness.ReasonNone}, nil
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
