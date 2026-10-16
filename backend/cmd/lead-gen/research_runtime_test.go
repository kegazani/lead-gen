package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	agentorchestration "lead-gen/backend/contexts/agent-orchestration"
	"lead-gen/contracts/agent-orchestration/researchmission"
	"lead-gen/contracts/agent-orchestration/researchplan"
	"lead-gen/contracts/integration-platform/researchacquisition"
	"lead-gen/contracts/lead-management/researchmemory"
)

func TestResearchCountriesValidateAndExpand(t *testing.T) {
	all, err := researchCountries("*")
	if err != nil || len(all) != 249 {
		t.Fatalf("каталог ISO: count=%d err=%v", len(all), err)
	}
	selected, err := researchCountries("ru,GB,RU")
	if err != nil || len(selected) != 2 || selected[0] != "RU" || selected[1] != "GB" {
		t.Fatalf("выбор рынка: %v %v", selected, err)
	}
	if _, err := researchCountries("XX"); err == nil {
		t.Fatal("принят неизвестный рынок")
	}
}

// TestResearchLiveProviders проверяет реальные опубликованные адаптеры и БД.
// Запуск явный: расходуется ограниченный Firecrawl grant; ключи берутся из ENV.
func TestResearchLiveProviders(t *testing.T) {
	if os.Getenv("LEADGEN_RESEARCH_LIVE") != "1" {
		t.Skip("реальные провайдеры запускаются явно")
	}
	for _, name := range []string{"LEADGEN_POSTGRES_TEST_DSN", "FIRECRAWL_API_KEY", "LLM_API_KEY", "LLM_MODEL"} {
		if os.Getenv(name) == "" {
			t.Fatalf("не задан %s", name)
		}
	}
	t.Chdir("../../..")
	resources := newRuntimePoolRegistry()
	t.Cleanup(resources.closeAll)
	runtime, err := buildResearchRuntime(buildOptions{
		researchOnlyProvider: true,
		researchRedDSN:       os.Getenv("LEADGEN_POSTGRES_TEST_DSN"), researchAccountID: "live-validation-primary", researchAccountCredits: 120,
		researchCountries: "RU,GB", researchMaxActions: 20, researchTimeout: 10 * time.Minute, researchContactFreshness: 7 * 24 * time.Hour,
		firecrawlKey: os.Getenv("FIRECRAWL_API_KEY"), firecrawlURL: os.Getenv("FIRECRAWL_BASE_URL"), firecrawlMaxCredits: 40, firecrawlCrawlInterval: time.Second,
		llmBaseURL: os.Getenv("LLM_BASE_URL"), llmModel: os.Getenv("LLM_MODEL"), llmKey: os.Getenv("LLM_API_KEY"), runtimeDBResources: resources,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	id := fmt.Sprintf("live-%d", time.Now().UnixNano())
	unit := envOr("LEADGEN_RESEARCH_LIVE_UNIT", researchplan.UnitCompany)
	icp := envOr("LEADGEN_RESEARCH_LIVE_ICP", "Российские производители строительных материалов")
	quantity := envIntOr("LEADGEN_RESEARCH_LIVE_QUANTITY", 2)
	brief, err := researchplan.NewBrief(researchplan.SearchBrief{ID: id, Revision: 1, TenantID: id, Purpose: "lead-research-validation", RequestID: id, IntentVersion: 1, ActorID: "human-validation", Offer: "Сервис автоматизации обработки заявок покупателей", ICP: icp, Criteria: researchplan.CriteriaSnapshot{Version: 1}, Goal: researchplan.Goal{Quantity: quantity, Unit: unit}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := runtime.requests.Planner.Plan(ctx, brief)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("planner=%s cells=%d", plan.PlannerStatus, len(plan.Cells))
	limits := runtime.requests.Limits
	limits.Deadline = time.Now().Add(9 * time.Minute)
	scope := researchmission.Scope{TenantID: brief.TenantID, PurposeID: brief.Purpose}
	snapshot, err := runtime.missions.Start(ctx, researchmission.StartRequest{Scope: scope, IntentID: id, Actor: brief.ActorID, Generation: "1", CriteriaVersion: 1, Plan: plan, Limits: limits, FirecrawlMaxCredits: 40})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; ; i++ {
		previousRevision := snapshot.Revision
		snapshot, err = runtime.missions.Step(ctx, scope, snapshot.MissionID)
		if err != nil {
			t.Fatalf("шаг %d: %v", i, err)
		}
		if snapshot.Revision != previousRevision || snapshot.CompletedAt != nil {
			t.Logf("step=%d state=%s phase=%s saved=%d accepted=%d contacts=%d documents=%d stop=%s", i, snapshot.State, snapshot.Phase, snapshot.Counters.Saved, snapshot.Counters.Accepted, snapshot.Counters.ContactReady, snapshot.Counters.Documents, snapshot.StopReason)
		}
		if snapshot.CompletedAt != nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	if snapshot.CompletedAt == nil || snapshot.Counters.Saved < 1 || snapshot.Counters.Accepted < brief.Goal.Quantity {
		for _, a := range snapshot.Actions {
			t.Logf("action kind=%s state=%s reason=%s", a.Kind, a.State, a.Reason)
		}
		t.Fatalf("реальная цель не достигнута: state=%s outcome=%s saved=%d accepted=%d", snapshot.State, snapshot.Outcome, snapshot.Counters.Saved, snapshot.Counters.Accepted)
	}
	creditedNames := map[string]bool{}
	for _, member := range snapshot.Members {
		dossier, err := runtime.requests.Memory.Get(ctx, researchmemory.GetRequest{Scope: researchmemory.Scope{TenantID: brief.TenantID, Purpose: brief.Purpose}, EntityID: member.EntityID})
		if err != nil {
			t.Fatal(err)
		}
		if len(dossier.Sources) == 0 || dossier.Entity.Name == "" {
			t.Fatal("сохранённая компания без доказательств")
		}
		if member.Accepted && len(member.EvidenceRefs) == 0 {
			t.Fatal("зачёт без оснований критериев")
		}
		if member.Accepted {
			name := strings.ToLower(strings.Join(strings.Fields(dossier.Entity.Name), " "))
			if creditedNames[name] {
				t.Fatal("повторное имя зачтено без ручного решения о различии компаний")
			}
			creditedNames[name] = true
		}
	}
	t.Logf("mission=%s saved=%d accepted=%d contacts=%d credits=%d unknown=%d", snapshot.MissionID, snapshot.Counters.Saved, snapshot.Counters.Accepted, snapshot.Counters.ContactReady, snapshot.Firecrawl.Actual, snapshot.Firecrawl.Unknown)
}

func TestResearchAnalysisBridgeRejectsForgedScopeAndMissingMarket(t *testing.T) {
	brief, err := researchplan.NewBrief(researchplan.SearchBrief{ID: "brief", TenantID: "tenant", Purpose: "purpose", RequestID: "request", IntentVersion: 1, ActorID: "operator", Offer: "Автоматизация", ICP: "Производители", Goal: researchplan.Goal{Quantity: 1, Unit: researchplan.UnitCompany}})
	if err != nil {
		t.Fatal(err)
	}
	base := agentorchestration.ResearchAnalysisRequest{Scope: researchmission.Scope{TenantID: "tenant", PurposeID: "purpose"}, Brief: brief, Receipt: researchacquisition.Receipt{Scope: researchacquisition.Scope{TenantID: "tenant", PurposeID: "purpose"}, Geography: researchacquisition.Geography{TargetCountry: "RU", Language: "ru"}, Artifacts: []researchacquisition.Artifact{{URL: "https://example.com", Content: "Компания", Representation: "markdown", SHA256: "revision", ObservedAt: time.Now()}}}}
	for _, name := range []string{"scope", "receipt-scope", "country-missing", "country-forged", "language-forged"} {
		t.Run(name, func(t *testing.T) {
			in := base
			switch name {
			case "scope":
				in.Scope.TenantID = "other"
			case "receipt-scope":
				in.Receipt.Scope.PurposeID = "other"
			case "country-missing":
				in.Receipt.Geography.TargetCountry = ""
			case "country-forged":
				in.Receipt.Geography.TargetCountry = "GB"
			case "language-forged":
				in.Receipt.Geography.Language = "en"
			}
			model := &bridgeAnalysisRecorder{}
			if _, err := (researchAnalysisBridge{model}).Analyze(context.Background(), in); err == nil || model.calls != 0 {
				t.Fatalf("недоверенный receipt дошёл до анализа: calls=%d err=%v", model.calls, err)
			}
		})
	}
	model := &bridgeAnalysisRecorder{}
	if _, err := (researchAnalysisBridge{model}).Analyze(context.Background(), base); err != nil || model.calls != 1 {
		t.Fatalf("валидная область: calls=%d err=%v", model.calls, err)
	}
	if model.request.Country != "RU" || model.request.Source.Family != "" {
		t.Fatal("транспорт не должен подменять рынок и семейство источника")
	}
}

type bridgeAnalysisRecorder struct {
	calls   int
	request researchmemory.AnalysisRequest
	result  researchmemory.AnalysisResult
}

func (r *bridgeAnalysisRecorder) Analyze(_ context.Context, in researchmemory.AnalysisRequest) (researchmemory.AnalysisResult, error) {
	r.calls++
	r.request = in
	return r.result, nil
}

func TestResearchRuntimeRejectsUnaccountedProviderRoute(t *testing.T) {
	if _, err := buildResearchRuntime(buildOptions{}); err == nil || !strings.Contains(err.Error(), "research-only-provider") {
		t.Fatalf("независимый платный путь должен блокировать сборку до открытия БД: %v", err)
	}
}

func TestResearchAnalysisBridgeCombinesBoundedDiagnostics(t *testing.T) {
	brief, err := researchplan.NewBrief(researchplan.SearchBrief{ID: "brief", TenantID: "tenant", Purpose: "purpose", RequestID: "request", IntentVersion: 1, ActorID: "operator", Offer: "Автоматизация", ICP: "Производители", Goal: researchplan.Goal{Quantity: 1, Unit: researchplan.UnitCompany}})
	if err != nil {
		t.Fatal(err)
	}
	model := &bridgeAnalysisRecorder{result: researchmemory.AnalysisResult{Diagnostics: []string{"subject-limit-reached", "unknown-country"}}}
	in := agentorchestration.ResearchAnalysisRequest{Scope: researchmission.Scope{TenantID: "tenant", PurposeID: "purpose"}, Brief: brief, Receipt: researchacquisition.Receipt{Scope: researchacquisition.Scope{TenantID: "tenant", PurposeID: "purpose"}, Geography: researchacquisition.Geography{TargetCountry: "RU", Language: "ru"}}}
	for i := 0; i < 40; i++ {
		in.Receipt.Artifacts = append(in.Receipt.Artifacts, researchacquisition.Artifact{URL: fmt.Sprintf("https://example.com/%d", i), Content: "Компания", Representation: "markdown", SHA256: "revision", ObservedAt: time.Now()})
	}
	result, err := (researchAnalysisBridge{model}).Analyze(context.Background(), in)
	if err != nil || len(result.Diagnostics) != 2 || model.calls != 40 {
		t.Fatalf("повторяющиеся предупреждения страниц не должны отклонять законный результат: diagnostics=%v calls=%d err=%v", result.Diagnostics, model.calls, err)
	}
}
