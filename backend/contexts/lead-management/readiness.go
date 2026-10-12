package leadmanagement

import (
	"context"
	"time"

	"lead-gen/backend/platform/readiness"
)

// ReadinessReport — безопасное owner-local представление готовности роли.
// Ready отражает профиль основной роли (для API — lead-management-core), а не
// объединение независимых capability; готовность каждой capability находится
// в Profiles[].Ready.
type ReadinessReport struct {
	Role     string             `json:"role"`
	Profiles []ReadinessProfile `json:"profiles"`
	Status   readiness.Status   `json:"status"`
	Ready    bool               `json:"ready"`
}

type ReadinessProfile struct {
	Capability string           `json:"capability"`
	Status     readiness.Status `json:"status"`
	Ready      bool             `json:"ready"`
	Checks     []ReadinessCheck `json:"checks"`
}

type ReadinessCheck struct {
	ID         string               `json:"id"`
	Status     readiness.Status     `json:"status"`
	Reason     readiness.ReasonCode `json:"reason"`
	ObservedAt time.Time            `json:"observed_at"`
	Required   bool                 `json:"required"`
}

// Readiness вычисляет отчёт указанной роли по зависимостям, уже собранным для
// сервиса. Платный Firecrawl поиск вынесен в профиль paid-search и никогда не
// исполняется самой readiness-проверкой.
func (s *Service) Readiness(ctx context.Context, role string) ReadinessReport {
	return evaluateReadiness(ctx, role, s.readinessChecks, s.readinessEvaluator)
}

func evaluateReadiness(ctx context.Context, role string, checks readinessChecks, evaluator *readiness.Evaluator) ReadinessReport {
	check := func(id, key string, required bool, probe readiness.Probe) readiness.CheckSpec {
		return readiness.CheckSpec{ID: id, Key: key, Required: required, Probe: probe}
	}
	profiles := []readiness.Profile{{
		Capability: "lead-management-core",
		Checks: []readiness.CheckSpec{
			check("persistence", "lead-management.persistence", true, checks.persistence),
			check("contact-persistence", "lead-management.contact-persistence", true, checks.contacts),
			check("llm", "lead-management.llm", false, nil),
			check("mail", "lead-management.mail", false, nil),
			check("vector", "lead-management.vector", false, nil),
		},
	}}
	if role == "worker" {
		profiles[0].Capability = "search-job-processing"
		profiles[0].Checks = append(profiles[0].Checks,
			check("firecrawl-search", "lead-management.firecrawl-search", true, checks.firecrawlSearch),
			check("search-provider", "lead-management.search-provider", true, checks.searchProvider),
		)
	} else if role == "api" {
		profiles = append(profiles, readiness.Profile{
			Capability: "paid-search",
			Checks: []readiness.CheckSpec{
				check("firecrawl-search", "lead-management.firecrawl-search", true, checks.firecrawlSearch),
				check("search-provider", "lead-management.search-provider", true, checks.searchProvider),
			},
		})
	}
	result := ReadinessReport{Role: role, Status: readiness.StatusUp, Ready: true}
	if role != "api" && role != "worker" {
		return ReadinessReport{Role: role, Status: readiness.StatusUnknown}
	}
	for _, profile := range profiles {
		snapshot := evaluator.Evaluate(ctx, profile)
		result.Profiles = append(result.Profiles, readinessProfile(snapshot))
		if profile.Capability == "paid-search" {
			// Профиль доступен клиентам отдельно, но его состояние не меняет
			// готовность API core. Любая неготовность capability видна в
			// агрегированном статусе как деградация, а не как сбой core.
			if !snapshot.Ready && result.Ready {
				result.Status = readiness.StatusDegraded
			}
			continue
		}
		if !snapshot.Ready {
			result.Ready = false
			result.Status = snapshot.Status
		} else if snapshot.Status != readiness.StatusUp && result.Status == readiness.StatusUp {
			// Optional core dependencies do not block readiness, but their
			// unknown/down state must remain visible in the root report.
			result.Status = readiness.StatusDegraded
		}
	}
	return result
}

func readinessProfile(snapshot readiness.Snapshot) ReadinessProfile {
	profile := ReadinessProfile{Capability: snapshot.Capability, Status: snapshot.Status, Ready: snapshot.Ready}
	for _, check := range snapshot.Checks {
		profile.Checks = append(profile.Checks, ReadinessCheck{
			ID: check.ID, Status: check.Status, Reason: check.Reason,
			ObservedAt: check.ObservedAt, Required: check.Required,
		})
	}
	return profile
}

type readinessChecks struct {
	persistence     readiness.Probe
	contacts        readiness.Probe
	firecrawlSearch readiness.Probe
	searchProvider  readiness.Probe
}
