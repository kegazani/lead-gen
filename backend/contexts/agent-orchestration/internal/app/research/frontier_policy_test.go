package research

import (
	"context"
	"fmt"
	"testing"
	"time"

	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	rm "lead-gen/contracts/agent-orchestration/researchmission"
	rp "lead-gen/contracts/agent-orchestration/researchplan"
	ra "lead-gen/contracts/integration-platform/researchacquisition"
)

type frontierSource struct{}

func (frontierSource) Capabilities() ra.Capabilities {
	return ra.Capabilities{Provider: "test-only", Revision: "v1", Search: true, Fetch: true}
}
func (frontierSource) Search(context.Context, ra.SearchRequest) (ra.Receipt, error) {
	panic("frontier policy не должна выполнять сеть")
}
func (frontierSource) Fetch(context.Context, ra.FetchRequest) (ra.Receipt, error) {
	panic("frontier policy не должна выполнять сеть")
}

func frontierFixture(unit string) (*Service, *domain.ResearchMission) {
	s := &Service{config: Config{Sources: map[string]ra.Port{"web": frontierSource{}}}}
	m := &domain.ResearchMission{ID: "mission", State: "running", Request: rm.StartRequest{Plan: rp.Plan{Brief: rp.SearchBrief{Goal: rp.Goal{Unit: unit, Quantity: 1}}}, Limits: rm.TechnicalLimits{MaxActions: 20, MaxDepth: 2}}}
	for i := 0; i < 4; i++ {
		hits := []ra.Hit{}
		for j := 0; j < 10; j++ {
			hits = append(hits, ra.Hit{URL: fmt.Sprintf("https://company-%d-%d.example/", i, j)})
		}
		m.Actions = append(m.Actions, domain.ResearchAction{ID: fmt.Sprintf("search-%d", i), Kind: "search", Queue: "discovery", SourceID: "web", ProfileRevision: "v1", Country: "RU", Language: "ru", State: "done", Receipt: actionJSON(ra.Receipt{Hits: hits})})
	}
	return s, m
}

func attachContactReceipt(m *domain.ResearchMission, actionID string) AnalysisResult {
	a := m.Action(actionID)
	text := "[О компании](/about)\n[Контакты](/contacts)"
	receipt := ra.Receipt{Artifacts: []ra.Artifact{{URL: a.URL, SHA256: "source-revision", Representation: "markdown", Content: text}}}
	analysis := AnalysisResult{Followups: SourceFollowups(receipt)}
	a.State, a.Receipt, a.Analysis = "done", actionJSON(receipt), actionJSON(analysis)
	return analysis
}

func TestContactFrontierReservesSlotsForLateContactAndPrioritizesProvenLink(t *testing.T) {
	s, m := frontierFixture(rp.UnitCompanyWithContact)
	for i := 0; i < 4; i++ {
		s.expand(m, m.Actions[i], AnalysisResult{})
	}
	if len(m.Actions) != 14 {
		t.Fatalf("четыре search не должны занимать все20 слотов: %d", len(m.Actions))
	}
	if !hasFrontierDiagnostic(m, "frontier-discovery-deferred") {
		t.Fatal("отложенные hits потерялись без диагностики")
	}
	pageID := m.Actions[4].ID
	analysis := attachContactReceipt(m, pageID)
	s.expand(m, *m.Action(pageID), analysis)
	contactID := domain.ResearchID(m.ID, "fetch", "https://company-0-0.example/contacts", "RU", "ru")
	contact := m.Action(contactID)
	if contact == nil || contact.Depth != 2 || contact.ParentID != pageID || contact.SourceID != "web" || contact.ProfileRevision != "v1" {
		t.Fatalf("поздняя контактная ссылка не вошла с lineage: %+v", contact)
	}
	m.LastQueue = "discovery"
	index := nextPendingActionIndex(m, time.Now())
	if index < 0 || m.Actions[index].ID != contactID {
		t.Fatal("контакты не опередили оставшиеся широкие search hits и /about")
	}
	// После контактов discovery всё равно получает очередь.
	m.Actions[1].State = "pending"
	m.LastQueue = "enrichment"
	if index = nextPendingActionIndex(m, time.Now()); index != 1 {
		t.Fatal("контактная очередь вытеснила discovery")
	}
}

func TestDeferredSearchHitsResumeOneAtATimeWithoutIncreasingCap(t *testing.T) {
	s, m := frontierFixture(rp.UnitCompanyWithContact)
	for i := 0; i < 4; i++ {
		s.expand(m, m.Actions[i], AnalysisResult{})
	}
	s.refillDeferredFrontier(m)
	if len(m.Actions) != 14 {
		t.Fatal("резерв отпущен при активных страницах")
	}
	for i := range m.Actions {
		m.Actions[i].State = "done"
	}
	for want := 15; want <= 20; want++ {
		s.refillDeferredFrontier(m)
		if len(m.Actions) != want || m.Actions[want-1].State != "pending" {
			t.Fatalf("receipt не восстановил один отложенный hit: want%d got%d", want, len(m.Actions))
		}
		m.Actions[want-1].State = "done"
	}
	s.refillDeferredFrontier(m)
	if len(m.Actions) != 20 || !hasFrontierDiagnostic(m, "frontier-action-cap-reached") {
		t.Fatal("предел действий превысили или молча отбросили оставшиеся hits")
	}
}

func TestCompanyGoalKeepsDiscoveryAdmissionAndFIFO(t *testing.T) {
	s, m := frontierFixture(rp.UnitCompany)
	s.expand(m, m.Actions[0], AnalysisResult{})
	s.expand(m, m.Actions[1], AnalysisResult{})
	if len(m.Actions) != 20 {
		t.Fatalf("для обычной компании необоснованно зарезервированы контакты: %d", len(m.Actions))
	}
	m.LastQueue = "discovery"
	if nextPendingActionIndex(m, time.Now()) != 4 {
		t.Fatal("изменён FIFO для обычной company цели")
	}
}

func TestFrontierDoesNotInventContactURLsAndHonorsBackoff(t *testing.T) {
	s, m := frontierFixture(rp.UnitCompanyWithContact)
	s.expand(m, m.Actions[0], AnalysisResult{})
	pageID := m.Actions[4].ID
	analysis := attachContactReceipt(m, pageID)
	forged := analysis.Followups[1]
	forged.URL = "https://company-0-0.example/guessed-contact"
	analysis.Followups = append(analysis.Followups, forged)
	s.expand(m, *m.Action(pageID), analysis)
	for i := range m.Actions {
		if m.Actions[i].URL == forged.URL {
			t.Fatal("выдуманный URL попал в frontier")
		}
		if m.Actions[i].URL == "https://company-0-0.example/contacts" {
			next := time.Now().Add(time.Hour)
			m.Actions[i].NextAttemptAt = &next
		}
	}
	m.LastQueue = "discovery"
	index := nextPendingActionIndex(m, time.Now())
	if index < 0 || m.Actions[index].URL != "https://company-0-0.example/about" {
		t.Fatal("backoff контактов проигнорирован или доказанная company страница не приоритетна")
	}
}

func hasFrontierDiagnostic(m *domain.ResearchMission, code string) bool {
	for _, action := range m.Actions {
		for _, diagnostic := range action.AnalysisDiagnostics {
			if diagnostic == code {
				return true
			}
		}
	}
	return false
}
