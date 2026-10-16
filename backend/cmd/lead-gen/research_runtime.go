package main

import (
	"context"
	"fmt"
	"strings"

	agentorchestration "lead-gen/backend/contexts/agent-orchestration"
	integrationplatform "lead-gen/backend/contexts/integration-platform"
	leadmanagement "lead-gen/backend/contexts/lead-management"
	requestfulfillment "lead-gen/backend/contexts/request-fulfillment"
	"lead-gen/backend/platform/composition"
	"lead-gen/contracts/agent-orchestration/researchmission"
	"lead-gen/contracts/agent-orchestration/researchplan"
	"lead-gen/contracts/integration-platform/llm"
	"lead-gen/contracts/integration-platform/researchacquisition"
	"lead-gen/contracts/lead-management/researchmemory"
)

// researchRuntime соединяет опубликованные порты владельцев в одном клиентском
// процессе. Сырые документы и решения хранятся только в явно заданной red БД.
type researchRuntime struct {
	missions *agentorchestration.ResearchService
	requests *requestfulfillment.ResearchConfig
}

func assembleResearchRuntime(selected []selectedService, opts *buildOptions) error {
	if !opts.researchEnabled {
		return nil
	}
	if opts.dbPolicy == intakeDBPolicyRuntimeOnly {
		return fmt.Errorf("research: runtime-only intake не поддерживает совмещённый исследовательский процесс")
	}
	requestAPI, requestWorker, agentWorker := false, false, false
	for _, service := range selected {
		requestAPI = requestAPI || (service.ID == requestfulfillment.ServiceID && hasRole(service.Roles, composition.RoleAPI))
		requestWorker = requestWorker || (service.ID == requestfulfillment.ServiceID && hasRole(service.Roles, composition.RoleWorker))
		agentWorker = agentWorker || (service.ID == agentorchestration.ServiceID && hasRole(service.Roles, composition.RoleWorker))
	}
	if !requestAPI || !requestWorker || !agentWorker || opts.accessDecision == nil {
		return fmt.Errorf("research: требуются совместно request-fulfillment/api+worker, agent-orchestration/worker и access-decision")
	}
	runtime, err := buildResearchRuntime(*opts)
	if err != nil {
		return err
	}
	opts.research = runtime
	return nil
}

func buildResearchRuntime(opts buildOptions) (*researchRuntime, error) {
	if !opts.researchOnlyProvider {
		return nil, fmt.Errorf("research: требуется research-only-provider, чтобы все платные вызовы Firecrawl проходили общий учёт")
	}
	if strings.TrimSpace(opts.researchRedDSN) == "" || strings.TrimSpace(opts.researchAccountID) == "" || strings.TrimSpace(opts.firecrawlKey) == "" || strings.TrimSpace(opts.llmKey) == "" || strings.TrimSpace(opts.llmModel) == "" || opts.researchAccountCredits < 0 || opts.firecrawlMaxCredits < 0 {
		return nil, fmt.Errorf("research: обязательны red DSN, Firecrawl account ID, ключи Firecrawl/LLM, модель и допустимые лимиты")
	}
	countries, err := researchCountries(opts.researchCountries)
	if err != nil {
		return nil, err
	}
	pool, err := newServicePool(opts.researchRedDSN, "research")
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			pool.Close()
		}
	}()
	for _, owner := range []string{leadmanagement.ServiceID, integrationplatform.ServiceID, agentorchestration.ServiceID} {
		if err := applyMigrations(pool, "backend/contexts/"+owner+"/migrations/red", owner); err != nil {
			return nil, err
		}
	}
	source, ledger, err := integrationplatform.NewResearchAcquisition(integrationplatform.ResearchAcquisitionConfig{
		Pool: pool, Firecrawl: integrationplatform.FirecrawlConfig{APIKey: opts.firecrawlKey, BaseURL: opts.firecrawlURL, Logger: opts.logger},
		Account:       researchacquisition.AccountPolicy{AccountID: opts.researchAccountID, MaxCredits: opts.researchAccountCredits},
		MaxConcurrent: 2, MinInterval: opts.firecrawlCrawlInterval,
	})
	if err != nil {
		return nil, err
	}
	model := integrationplatform.NewOpenAI(integrationplatform.OpenAIConfig{BaseURL: opts.llmBaseURL, Model: opts.llmModel, APIKey: opts.llmKey})
	memory := leadmanagement.NewPostgresResearchMemory(pool)
	analyzer, err := leadmanagement.NewResearchAnalyzer(leadmanagement.ResearchAnalysisConfig{Model: model, ModelID: opts.llmModel, ContactFreshness: opts.researchContactFreshness, MaxSubjects: 4})
	if err != nil {
		return nil, err
	}
	caps := source.Capabilities()
	const sourceID = "firecrawl-web"
	sources := map[string]researchacquisition.Port{sourceID: source}
	var manifests []researchplan.SourceCapability
	if strings.TrimSpace(opts.researchSearXNGURL) != "" {
		searxng, err := integrationplatform.NewSearXNGResearch(integrationplatform.SearXNGResearchConfig{Pool: pool, BaseURL: opts.researchSearXNGURL, Engines: splitList(opts.researchSearXNGEngines)})
		if err != nil {
			return nil, err
		}
		const searxngID = "searxng-web"
		sources[searxngID] = searxng
		manifest := searxng.Capabilities()
		manifests = append(manifests, researchplan.SourceCapability{ID: searxngID, Revision: manifest.Revision, SourceFamily: "web", Countries: countries, Languages: []string{"*"}, Query: manifest.Search, Available: true})
	}
	manifests = append(manifests, researchplan.SourceCapability{ID: sourceID, Revision: caps.Revision, SourceFamily: "web", Countries: countries, Languages: []string{"*"}, Query: caps.Search, Available: true})
	planner, err := agentorchestration.NewResearchPlanner(agentorchestration.ResearchPlannerConfig{Model: model, ModelID: opts.llmModel,
		Sources: manifests,
	})
	if err != nil {
		return nil, err
	}
	missions, err := agentorchestration.NewResearchService(agentorchestration.ResearchConfig{RedPool: pool, Sources: sources, FetchSourceIDs: []string{sourceID}, Ledger: ledger, FirecrawlAccountID: opts.researchAccountID, MaxFirecrawlCredits: opts.firecrawlMaxCredits, Memory: memory, Analyzer: researchAnalysisBridge{analyzer}})
	if err != nil {
		return nil, err
	}
	requests := &requestfulfillment.ResearchConfig{Missions: missions, Memory: memory, Planner: planner, Access: opts.accessDecision, Countries: countries, RunTimeout: opts.researchTimeout,
		OnlyResearch: opts.researchOnlyProvider, Identity: leadmanagement.NewPostgresResearchIdentity(pool),
		Limits: researchmission.TechnicalLimits{MaxActions: opts.researchMaxActions, MaxAttempts: 2, MaxResultsPerSearch: 10, MaxDepth: 2, MaxBytesPerArtifact: 256 << 10, ActionTimeoutSeconds: 120},
	}
	if opts.runtimeDBResources == nil {
		return nil, fmt.Errorf("research: отсутствует владелец закрытия пулов")
	}
	if err := opts.runtimeDBResources.addServicePool(pool); err != nil {
		return nil, err
	}
	keep = true
	return &researchRuntime{missions: missions, requests: requests}, nil
}

func researchCountries(value string) ([]string, error) {
	if strings.TrimSpace(value) == "*" {
		var result []string
		for _, country := range researchplan.Countries() {
			result = append(result, country.Code)
		}
		return result, nil
	}
	result, seen := []string{}, map[string]bool{}
	for _, raw := range strings.Split(value, ",") {
		country := strings.ToUpper(strings.TrimSpace(raw))
		if _, ok := researchplan.LookupCountry(country); !ok {
			return nil, fmt.Errorf("research: неизвестная страна %q", country)
		}
		if !seen[country] {
			result = append(result, country)
			seen[country] = true
		}
	}
	return result, nil
}

// researchAnalysisBridge переводит контракты, не импортируя чужие internal-модели.
type researchAnalysisBridge struct{ analyzer researchmemory.Analyzer }

func (b researchAnalysisBridge) Analyze(ctx context.Context, in agentorchestration.ResearchAnalysisRequest) (agentorchestration.ResearchAnalysisResult, error) {
	var result agentorchestration.ResearchAnalysisResult
	if in.Scope.TenantID != in.Brief.TenantID || in.Scope.PurposeID != in.Brief.Purpose || in.Receipt.Scope.TenantID != in.Scope.TenantID || in.Receipt.Scope.PurposeID != in.Scope.PurposeID {
		return result, fmt.Errorf("research: область анализа не совпадает с поручением")
	}
	marketAllowed := false
	for _, market := range in.Brief.Markets {
		if market.Country != in.Receipt.Geography.TargetCountry {
			continue
		}
		for _, language := range market.Languages {
			if language == in.Receipt.Geography.Language {
				marketAllowed = true
			}
		}
	}
	if !marketAllowed {
		return result, fmt.Errorf("research: рынок документа не входит в поручение")
	}
	result.Followups = agentorchestration.ResearchFollowups(in.Receipt)
	result.SchemaVersion = llm.ResearchExtractionSchemaVersion
	var criteria []researchmemory.AnalysisCriterion
	for _, group := range []struct {
		kind   string
		values []researchplan.Criterion
	}{{"must", in.Brief.Criteria.Must}, {"should", in.Brief.Criteria.Should}, {"exclude", in.Brief.Criteria.Exclude}} {
		for _, c := range group.values {
			criteria = append(criteria, researchmemory.AnalysisCriterion{ID: c.ID, Text: c.Text, Kind: group.kind})
		}
	}
	for _, artifact := range in.Receipt.Artifacts {
		if strings.TrimSpace(artifact.Content) == "" {
			continue
		}
		analyzed, err := b.analyzer.Analyze(ctx, researchmemory.AnalysisRequest{Scope: researchmemory.Scope{TenantID: in.Scope.TenantID, Purpose: in.Scope.PurposeID}, MissionID: in.MissionID, ActionID: in.ActionID, QueryAttemptID: in.QueryAttemptID, CriteriaVersion: in.Brief.Criteria.Version, ICP: in.Brief.ICP, Country: in.Receipt.Geography.TargetCountry, Criteria: criteria,
			Source: researchmemory.Source{URL: artifact.URL, Revision: artifact.SHA256, Representation: artifact.Representation, Content: artifact.Content, ObservedAt: artifact.ObservedAt, Kind: "page"},
		})
		if err != nil {
			return result, err
		}
		result.ModelID, result.MethodVersion = analyzed.ModelID, analyzed.MethodVersion
		for _, diagnostic := range analyzed.Diagnostics {
			seen := false
			for _, existing := range result.Diagnostics {
				seen = seen || existing == diagnostic
			}
			if !seen && len(result.Diagnostics) < 64 {
				result.Diagnostics = append(result.Diagnostics, diagnostic)
			}
		}
		for _, observed := range analyzed.Observations {
			result.Observations = append(result.Observations, observed.Observation)
			result.Assessments = append(result.Assessments, agentorchestration.ResearchAssessment{ObservationID: observed.Observation.ObservationID, CriteriaVersion: in.Brief.Criteria.Version, Accepted: observed.Accepted, Reason: observed.Reason, EvidenceRefs: observed.EvidenceRefs})
		}
	}
	return result, nil
}

var _ agentorchestration.ResearchAnalyzer = researchAnalysisBridge{}
