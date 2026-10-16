package research

import (
	"encoding/json"
	"sort"
	"time"

	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	ra "lead-gen/contracts/integration-platform/researchacquisition"
)

func contactGoal(m *domain.ResearchMission) bool {
	unit := m.Request.Plan.Brief.Goal.Unit
	return unit == "company-with-contact" || unit == "email-ready"
}

// contactFrontierReserve оставляет технические слоты для переходов со страницы
// компании. Это не цель по лидам и не отдельный денежный бюджет.
func contactFrontierReserve(m *domain.ResearchMission) int {
	if !contactGoal(m) || m.Request.Limits.MaxDepth < 2 {
		return 0
	}
	reserve := min(8, max(1, m.Request.Limits.MaxActions/3))
	searches := 0
	for _, action := range m.Actions {
		if action.Kind == "search" {
			searches++
		}
	}
	// Хотя бы один слот остаётся для исходной страницы даже при малом cap.
	return min(reserve, max(0, m.Request.Limits.MaxActions-searches-1))
}

type frontierTarget struct {
	url      string
	priority int
	shallow  bool
}

// expandFrontierAction использует только receipt и проверенные followups. Hits,
// временно отложенные резервом, остаются в receipt и могут быть повторно допущены.
func (s *Service) expandFrontierAction(m *domain.ResearchMission, a domain.ResearchAction, analysis AnalysisResult, releaseReserve bool, maxAdded int) int {
	var receipt ra.Receipt
	if json.Unmarshal(a.Receipt, &receipt) != nil {
		return 0
	}
	targets := []frontierTarget{}
	already := map[string]bool{}
	for _, artifact := range receipt.Artifacts {
		if normalized, ok := canonicalURL(artifact.URL); ok {
			already[normalized] = true
		}
	}
	for _, hit := range receipt.Hits {
		if normalized, ok := canonicalURL(hit.URL); ok && !already[normalized] {
			targets = append(targets, frontierTarget{url: normalized, shallow: true})
			already[normalized] = true
		}
	}
	if a.Depth < m.Request.Limits.MaxDepth {
		for _, followup := range analysis.Followups {
			if normalized, ok := canonicalURL(followup.URL); ok && !already[normalized] && followupProven(receipt, followup) {
				priority := 1
				if followup.Reason == "source-contact-link" {
					priority = 2
				}
				targets = append(targets, frontierTarget{url: normalized, priority: priority})
				already[normalized] = true
			}
		}
	}
	if contactGoal(m) {
		sort.SliceStable(targets, func(i, j int) bool { return targets[i].priority > targets[j].priority })
	}
	// Выбор fetch-адаптера сохраняет явный runtime allowlist и текущий FC grant.
	fetchSourceID, profileRevision := a.SourceID, a.ProfileRevision
	if provider := s.config.Sources[fetchSourceID]; provider == nil || !provider.Capabilities().Fetch || (provider.Capabilities().Provider == "firecrawl" && m.Firecrawl.Exhausted) {
		fetchSourceID = ""
		for _, candidate := range s.config.FetchSourceIDs {
			provider := s.config.Sources[candidate]
			if provider != nil && provider.Capabilities().Fetch && !(provider.Capabilities().Provider == "firecrawl" && m.Firecrawl.Exhausted) {
				fetchSourceID = candidate
				profileRevision = provider.Capabilities().Revision
				break
			}
		}
	}
	reserve, added := contactFrontierReserve(m), 0
	for _, target := range targets {
		id := domain.ResearchID(m.ID, "fetch", target.url, a.Country, a.Language)
		if m.Action(id) != nil {
			continue
		}
		if len(m.Actions) >= m.Request.Limits.MaxActions {
			frontierDiagnostic(m, a.ID, "frontier-action-cap-reached")
			continue
		}
		if target.shallow && !releaseReserve && len(m.Actions) >= m.Request.Limits.MaxActions-reserve {
			frontierDiagnostic(m, a.ID, "frontier-discovery-deferred")
			continue
		}
		state, reason := "pending", ""
		if fetchSourceID == "" {
			state, reason = "blocked", "fetch-capability-unavailable"
		}
		m.Actions = append(m.Actions, domain.ResearchAction{ID: id, Kind: "fetch", Queue: "enrichment", SourceID: fetchSourceID, DiscoverySourceID: a.SourceID, StrategyCellID: a.StrategyCellID, QueryFamilyID: a.QueryFamilyID, Country: a.Country, Language: a.Language, ProfileRevision: profileRevision, URL: target.url, ParentID: a.ID, Depth: a.Depth + 1, State: state, Reason: reason, Attempt: 1})
		added++
		if maxAdded > 0 && added >= maxAdded {
			break
		}
	}
	return added
}

func frontierDiagnostic(m *domain.ResearchMission, actionID, code string) {
	a := m.Action(actionID)
	if a == nil {
		return
	}
	for _, existing := range a.AnalysisDiagnostics {
		if existing == code {
			return
		}
	}
	if len(a.AnalysisDiagnostics) < 64 {
		a.AnalysisDiagnostics = append(a.AnalysisDiagnostics, code)
	}
}

func (s *Service) refillDeferredFrontier(m *domain.ResearchMission) {
	if !contactGoal(m) {
		return
	}
	for _, action := range m.Actions {
		switch action.State {
		case "pending", "running", "analyzing", "ingesting", "received", "analyzed":
			return
		case "unknown":
			if action.Reason == "lease-expired-effect-unknown" && action.NextAttemptAt != nil {
				return
			}
		}
	}
	for _, action := range m.Actions {
		if action.State != "done" || action.Kind != "search" {
			continue
		}
		if s.expandFrontierAction(m, action, AnalysisResult{}, true, 1) > 0 {
			return
		}
	}
}

// Чередование discovery/enrichment сохраняется. Внутри enrichment контактной
// цели доказанная контактная ссылка опережает FIFO из широких search hits.
func nextPendingActionIndex(m *domain.ResearchMission, now time.Time) int {
	for pass := 0; pass < 2; pass++ {
		index, priority := -1, -1
		for i, action := range m.Actions {
			if action.State != "pending" || (action.NextAttemptAt != nil && now.Before(*action.NextAttemptAt)) || (pass == 0 && action.Queue == m.LastQueue) {
				continue
			}
			p := 0
			if contactGoal(m) {
				p = contactActionPriority(m, action)
			}
			if index < 0 || p > priority {
				index, priority = i, p
			}
		}
		if index >= 0 {
			return index
		}
	}
	return -1
}

func contactActionPriority(m *domain.ResearchMission, action domain.ResearchAction) int {
	parent := m.Action(action.ParentID)
	if parent == nil || action.Kind != "fetch" {
		return 0
	}
	var receipt ra.Receipt
	var analysis AnalysisResult
	if json.Unmarshal(parent.Receipt, &receipt) != nil || json.Unmarshal(parent.Analysis, &analysis) != nil {
		return 0
	}
	for _, followup := range analysis.Followups {
		normalized, ok := canonicalURL(followup.URL)
		if ok && normalized == action.URL && followupProven(receipt, followup) {
			if followup.Reason == "source-contact-link" {
				return 2
			}
			return 1
		}
	}
	return 0
}
