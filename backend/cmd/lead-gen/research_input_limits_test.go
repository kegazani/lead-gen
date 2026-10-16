package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	agent "lead-gen/backend/contexts/agent-orchestration"
	lead "lead-gen/backend/contexts/lead-management"
	"lead-gen/contracts/agent-orchestration/researchmission"
	"lead-gen/contracts/agent-orchestration/researchplan"
	"lead-gen/contracts/integration-platform/llm"
	"lead-gen/contracts/integration-platform/researchacquisition"
)

type inputLimitModel struct {
	t     *testing.T
	brief researchplan.SearchBrief
	calls int
}

func (m *inputLimitModel) ExtractResearch(_ context.Context, req llm.ResearchExtractRequest) (llm.ResearchExtractResult, llm.Usage, error) {
	m.calls++
	if err := req.Validate(); err != nil {
		m.t.Fatalf("валидный brief превратился в невалидный extraction request: %v", err)
	}
	if req.ICP != m.brief.ICP || len(req.Criteria) != researchplan.MaxEvaluatedCriteria+2 {
		m.t.Fatal("потеряны ICP или обязательные критерии при переходе контрактов")
	}
	byID := map[string]llm.ResearchCriterion{}
	for _, c := range req.Criteria {
		byID[c.ID] = c
	}
	for kind, criteria := range map[string][]researchplan.Criterion{"must": m.brief.Criteria.Must, "should": m.brief.Criteria.Should, "exclude": m.brief.Criteria.Exclude} {
		for _, c := range criteria {
			if byID[c.ID].Text != c.Text || byID[c.ID].Kind != kind {
				m.t.Fatalf("критерий %q изменён или удалён", c.ID)
			}
		}
	}
	if byID["__human_icp"].Text != m.brief.ICP {
		m.t.Fatal("максимальный ICP обрезан в служебном критерии")
	}
	// Только тестовый ответ модели: проверяем передачу границ, не качество LLM.
	return llm.ResearchExtractResult{Subjects: []llm.ResearchSubject{}}, llm.Usage{}, nil
}

func TestResearchMaximumRussianBriefPassesProductionAnalysisRequest(t *testing.T) {
	brief, err := researchplan.NewBrief(researchplan.SearchBrief{ID: "limits", TenantID: "tenant", Purpose: "research", RequestID: "human-request", ActorID: "operator", IntentVersion: 1, Offer: "Услуга", ICP: strings.Repeat("я", 8000), Goal: researchplan.Goal{Unit: researchplan.UnitCompany, Quantity: 1}})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < researchplan.MaxEvaluatedCriteria; i++ {
		c := researchplan.Criterion{ID: fmt.Sprintf("критерий-%d", i), Text: strings.Repeat("я", 1000), Origin: researchplan.OriginHuman}
		switch {
		case i < 32:
			brief.Criteria.Must = append(brief.Criteria.Must, c)
		case i < 37:
			brief.Criteria.Should = append(brief.Criteria.Should, c)
		default:
			brief.Criteria.Exclude = append(brief.Criteria.Exclude, c)
		}
	}
	brief.Criteria.Must[0].ID = strings.Repeat("я", 64)
	for i := 0; i < researchplan.MaxCriteriaPerGroup; i++ {
		brief.Criteria.Unknown = append(brief.Criteria.Unknown, researchplan.Criterion{ID: fmt.Sprintf("гипотеза-%d", i), Text: strings.Repeat("я", 1000), Origin: researchplan.OriginInferred})
	}
	if err = brief.Validate(); err != nil {
		t.Fatal(err)
	}
	model := &inputLimitModel{t: t, brief: brief}
	analyzer, err := lead.NewResearchAnalyzer(lead.ResearchAnalysisConfig{Model: model, ModelID: "test-only", ContactFreshness: time.Hour, MaxSubjects: 4})
	if err != nil {
		t.Fatal(err)
	}
	bridge := researchAnalysisBridge{analyzer: analyzer}
	_, err = bridge.Analyze(context.Background(), agent.ResearchAnalysisRequest{Scope: researchmission.Scope{TenantID: brief.TenantID, PurposeID: brief.Purpose}, MissionID: "mission", ActionID: "action", QueryAttemptID: "attempt", Brief: brief, Receipt: researchacquisition.Receipt{Scope: researchacquisition.Scope{TenantID: brief.TenantID, PurposeID: brief.Purpose}, Geography: researchacquisition.Geography{TargetCountry: "RU", Language: "ru"}, Artifacts: []researchacquisition.Artifact{{URL: "https://company.example/", SHA256: "snapshot-revision", Representation: "markdown", Content: "Компания", ObservedAt: time.Now().UTC()}}}})
	if err != nil || model.calls != 1 {
		t.Fatalf("производственный analysis request не дошёл до модели: calls=%d err=%v", model.calls, err)
	}
}
