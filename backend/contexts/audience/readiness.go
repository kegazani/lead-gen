package audience

import (
	"context"
	"time"

	httpapi "lead-gen/backend/contexts/audience/internal/adapters/http"
	"lead-gen/backend/platform/readiness"
)

// Readiness вычисляет readiness-профили только для ролей владельца audience.
// Отсутствие реальной проверки БД остаётся unknown.
func (s *Service) Readiness(ctx context.Context, role string) ReadinessReport {
	return evaluateReadiness(ctx, role, s.persistence, s.readinessEvaluator)
}

func evaluateReadiness(ctx context.Context, role string, persistence Persistence, evaluator *readiness.Evaluator) ReadinessReport {
	if role != "api" && role != "worker" && role != "schedule" {
		return ReadinessReport{Role: role, Status: readiness.StatusUnknown}
	}
	profiles := []readiness.Profile{{
		Capability: "lead-segmentation",
		Checks: []readiness.CheckSpec{{
			ID: "persistence", Key: "audience.persistence", Required: true, Probe: persistence.DatabaseProbe,
		}},
	}}
	if role == "worker" {
		profiles = append(profiles, readiness.Profile{
			Capability: "event-publication",
			Checks:     []readiness.CheckSpec{{ID: "publisher", Key: "audience.publisher", Required: false}},
		})
	}
	if role == "schedule" {
		profiles = []readiness.Profile{{
			Capability: "audience-refresh",
			Checks: []readiness.CheckSpec{{
				ID: "persistence", Key: "audience.persistence", Required: true, Probe: persistence.DatabaseProbe,
			}},
		}}
	}
	result := ReadinessReport{Role: role, Status: readiness.StatusUp, Ready: true}
	for _, profile := range profiles {
		snapshot := evaluator.Evaluate(ctx, profile)
		result.Profiles = append(result.Profiles, httpReadinessProfile(snapshot))
		if snapshot.Status == readiness.StatusDegraded && result.Status == readiness.StatusUp {
			result.Status = readiness.StatusDegraded
		}
		if !snapshot.Ready {
			result.Ready = false
			if snapshot.Status == readiness.StatusDown || result.Status != readiness.StatusDown {
				result.Status = snapshot.Status
			}
		}
	}
	return result
}

// ReadinessReport — owner-local snapshot readiness роли audience.
type ReadinessReport struct {
	Role     string                     `json:"role"`
	Profiles []httpapi.ReadinessProfile `json:"profiles"`
	Status   readiness.Status           `json:"status"`
	Ready    bool                       `json:"ready"`
}

func httpReadinessProfile(snapshot readiness.Snapshot) httpapi.ReadinessProfile {
	profile := httpapi.ReadinessProfile{Capability: snapshot.Capability, Status: snapshot.Status, Ready: snapshot.Ready}
	for _, check := range snapshot.Checks {
		profile.Checks = append(profile.Checks, httpapi.ReadinessCheck{
			ID: check.ID, Status: check.Status, Reason: check.Reason,
			ObservedAt: check.ObservedAt, Required: check.Required,
		})
	}
	return profile
}

func newReadinessEvaluator() *readiness.Evaluator {
	return readiness.New(readiness.Config{Timeout: time.Second, TTL: 2 * time.Second})
}
