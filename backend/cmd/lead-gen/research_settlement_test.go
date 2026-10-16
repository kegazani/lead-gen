package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	agents "lead-gen/backend/contexts/agent-orchestration"
	integration "lead-gen/backend/contexts/integration-platform"
	leads "lead-gen/backend/contexts/lead-management"
	"lead-gen/backend/platform/migrate"
	rm "lead-gen/contracts/agent-orchestration/researchmission"
	rp "lead-gen/contracts/agent-orchestration/researchplan"
	ra "lead-gen/contracts/integration-platform/researchacquisition"
	memory "lead-gen/contracts/lead-management/researchmemory"
)

type settlementAnalyzer struct{}

// Двойник только извлечения: проверяем реальную запись полезного результата в
// owner память и восстановление ledger, без платной внешней модели.
func (settlementAnalyzer) Analyze(_ context.Context, in agents.ResearchAnalysisRequest) (agents.ResearchAnalysisResult, error) {
	hit := in.Receipt.Hits[0]
	text := hit.Snippet
	locator := memory.Locator{Start: 0, End: len(text), Quote: text}
	observation := memory.SaveObservationRequest{Scope: memory.Scope{TenantID: in.Scope.TenantID, Purpose: in.Scope.PurposeID}, ObservationID: in.ActionID + "-company", MissionID: in.MissionID, ActionID: in.ActionID, QueryAttemptID: in.QueryAttemptID, Source: memory.Source{URL: hit.URL, Revision: "snippet-v1", Representation: "text", Content: text, Kind: "search-snippet", ObservedAt: in.Receipt.ObservedAt, Family: "search"}, Subject: memory.Subject{Key: "company", Name: "Компания Альфа", Kind: "company", Locator: locator}}
	return agents.ResearchAnalysisResult{Observations: []memory.SaveObservationRequest{observation}}, nil
}

func TestResearchSettlementPostgresFinishFailureKeepsUsefulDataAndRecoversAfterTerminalRestart(t *testing.T) {
	dsn := os.Getenv("LEADGEN_SETTLEMENT_TEST_DSN")
	if dsn == "" {
		t.Skip("нужна отдельная PostgreSQL БД research_settlement для fault injection")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close() })
	var database string
	if err = pool.QueryRow(ctx, "SELECT current_database()").Scan(&database); err != nil {
		t.Fatal(err)
	}
	if database != "research_settlement" {
		t.Fatal("fault injection разрешён только в отдельной research_settlement")
	}
	for _, owner := range []string{"integration-platform", "lead-management", "agent-orchestration"} {
		if err = migrate.Apply(ctx, pool, "../../contexts/"+owner+"/migrations/red", migrate.OwnSchema(owner)); err != nil {
			t.Fatal(err)
		}
	}
	// Вся owner transaction откатывается именно на финальном UPDATE attempt,
	// после HTTP, несмотря на успешный provider payload.
	_, err = pool.Exec(ctx, `CREATE OR REPLACE FUNCTION public.research_test_reject_finish() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF OLD.state='running' AND NEW.state<>'running' THEN RAISE EXCEPTION 'test finish commit rejected'; END IF; RETURN NEW; END $$;
DROP TRIGGER IF EXISTS research_test_reject_finish ON "integration-platform".research_acquisition_attempt;
CREATE TRIGGER research_test_reject_finish BEFORE UPDATE ON "integration-platform".research_acquisition_attempt FOR EACH ROW EXECUTE FUNCTION public.research_test_reject_finish();`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS research_test_reject_finish ON "integration-platform".research_acquisition_attempt; DROP FUNCTION IF EXISTS public.research_test_reject_finish()`)
	})
	var paidCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v2/team/credit-usage" {
			fmt.Fprint(w, `{"success":true,"data":{"remainingCredits":100}}`)
			return
		}
		paidCalls.Add(1)
		fmt.Fprint(w, `{"success":true,"data":[{"url":"https://alpha.example/","title":"Компания Альфа","description":"Компания Альфа выпускает строительные материалы"}]}`)
	}))
	t.Cleanup(server.Close)
	now := time.Now().UTC()
	tenant := fmt.Sprintf("settlement-%d", now.UnixNano())
	newService := func() (*agents.ResearchService, ra.Port, memory.Port) {
		t.Helper()
		source, ledger, err := integration.NewResearchAcquisition(integration.ResearchAcquisitionConfig{Pool: pool, Firecrawl: integration.FirecrawlConfig{APIKey: "test-only-key", BaseURL: server.URL}, Account: ra.AccountPolicy{AccountID: tenant, MaxCredits: 20}, HTTPClient: server.Client()})
		if err != nil {
			t.Fatal(err)
		}
		store := leads.NewPostgresResearchMemory(pool)
		svc, err := agents.NewResearchService(agents.ResearchConfig{RedPool: pool, Sources: map[string]ra.Port{"fc": source}, Ledger: ledger, FirecrawlAccountID: tenant, MaxFirecrawlCredits: 10, Memory: store, Analyzer: settlementAnalyzer{}, Now: func() time.Time { return now }})
		if err != nil {
			t.Fatal(err)
		}
		return svc, source, store
	}
	svc, source, _ := newService()
	brief, err := rp.NewBrief(rp.SearchBrief{ID: tenant, TenantID: tenant, Purpose: "research-settlement-test", RequestID: "human-request", IntentVersion: 1, ActorID: "human", Offer: "Автоматизация", ICP: "Строительные компании", Criteria: rp.CriteriaSnapshot{Version: 1}, Goal: rp.Goal{Quantity: 1, Unit: rp.UnitCompany}})
	if err != nil {
		t.Fatal(err)
	}
	family := rp.QueryFamily{ID: "family", Country: "RU", Language: "ru", SourceFamily: "web", Question: "Найти компании", Query: "Строительные компании", Origin: rp.OriginRecipe}
	plan := rp.Plan{ID: "plan", Revision: 1, Brief: brief, BriefID: brief.ID, BriefRevision: brief.Revision, TenantID: tenant, Purpose: brief.Purpose, SchemaVersion: rp.SchemaVersion, PromptVersion: rp.PromptVersion, PlannerStatus: "recipe", Families: []rp.QueryFamily{family}, Cells: []rp.StrategyCell{{ID: "cell", Country: "RU", Language: "ru", Segment: brief.ICP, SourceID: "fc", SourceRevision: source.Capabilities().Revision, SourceFamily: "web", Question: family.Question, QueryFamilyID: family.ID}}}
	startRequest := rm.StartRequest{Scope: rm.Scope{TenantID: tenant, PurposeID: brief.Purpose}, IntentID: brief.RequestID, Actor: brief.ActorID, Generation: "1", CriteriaVersion: 1, Plan: plan, Limits: rm.TechnicalLimits{MaxActions: 1, MaxAttempts: 2, MaxResultsPerSearch: 1, MaxDepth: 1, MaxBytesPerArtifact: 1 << 20, ActionTimeoutSeconds: 5, Deadline: now.Add(time.Hour)}}
	current, err := svc.Start(ctx, startRequest)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		current, err = svc.Step(ctx, current.Scope, current.MissionID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if current.State != "completed" || current.Counters.Saved != 1 || current.Actions[0].SettlementState != "pending" || current.Firecrawl.Reserved == 0 || current.Firecrawl.Unknown != 0 || current.Firecrawl.Actual != 0 {
		t.Fatalf("полезный результат потерян или расход скрыт: %+v", current)
	}
	before := current
	var runnable bool
	if err = pool.QueryRow(ctx, `SELECT runnable FROM "agent-orchestration".research_missions WHERE tenant_id=$1 AND mission_id=$2`, tenant, current.MissionID).Scan(&runnable); err != nil || !runnable {
		t.Fatalf("terminal потерял обязательство: %t %v", runnable, err)
	}
	if _, err = pool.Exec(ctx, `DROP TRIGGER research_test_reject_finish ON "integration-platform".research_acquisition_attempt; DROP FUNCTION public.research_test_reject_finish()`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE "integration-platform".research_acquisition_attempt SET lease_until=clock_timestamp()-interval '1 second' WHERE tenant_id=$1`, tenant); err != nil {
		t.Fatal(err)
	}
	// Новый pool и новые публичные constructors: восстановление только из БД.
	pool.Close()
	pool, err = pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(11 * time.Minute)
	restarted, _, store := newService()
	if err = restarted.RunReady(ctx, 100); err != nil {
		t.Fatal(err)
	}
	current, err = restarted.Get(ctx, current.Scope, current.MissionID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Actions[0].SettlementState != "settled-unknown" || current.Firecrawl.Unknown != before.Firecrawl.Reserved || current.Firecrawl.Reserved != before.Firecrawl.Reserved || paidCalls.Load() != 1 || !reflect.DeepEqual(current.Counters, before.Counters) || !reflect.DeepEqual(current.Members, before.Members) {
		t.Fatalf("recovery потерял данные, учёт или повторил POST: calls=%d before=%+v after=%+v", paidCalls.Load(), before, current)
	}
	if err = pool.QueryRow(ctx, `SELECT runnable FROM "agent-orchestration".research_missions WHERE tenant_id=$1 AND mission_id=$2`, tenant, current.MissionID).Scan(&runnable); err != nil || runnable {
		t.Fatalf("settled obligation не закрыт: %t %v", runnable, err)
	}
	dossier, err := store.Get(ctx, memory.GetRequest{Scope: memory.Scope{TenantID: tenant, Purpose: brief.Purpose}, EntityID: current.Members[0].EntityID})
	if err != nil || dossier.Entity.Name != "Компания Альфа" {
		t.Fatalf("полезная owner запись потеряна: %+v %v", dossier.Entity, err)
	}
	if err = restarted.RunReady(ctx, 100); err != nil || paidCalls.Load() != 1 {
		t.Fatalf("повтор settlement вызвал provider: %d %v", paidCalls.Load(), err)
	}
	// Пересмотр критериев того же human intent не создаёт второй grant и не
	// сбрасывает удержанные credits предыдущего поколения.
	next := startRequest
	next.Generation, next.CriteriaVersion = "2", 2
	next.Plan.Revision = 2
	next.Plan.Brief.Revision, next.Plan.BriefRevision = 2, 2
	next.Plan.Brief.Criteria.Version, next.Plan.Brief.IntentVersion = 2, 2
	newGeneration, err := restarted.Start(ctx, next)
	if err != nil || newGeneration.Firecrawl.GrantRef != current.Firecrawl.GrantRef || newGeneration.MissionID == current.MissionID {
		t.Fatalf("новое поколение потеряло общий scope: %+v %v", newGeneration, err)
	}
	var grants, held, uncertain int
	if err = pool.QueryRow(ctx, `SELECT count(*),COALESCE(sum(reserved_credits),0),COALESCE(sum(unknown_credits),0) FROM "integration-platform".research_fc_grant WHERE tenant_id=$1`, tenant).Scan(&grants, &held, &uncertain); err != nil || grants != 1 || held != current.Firecrawl.Reserved || uncertain != current.Firecrawl.Unknown {
		t.Fatalf("новая generation сбросила бюджет: grants=%d held=%d unknown=%d err=%v", grants, held, uncertain, err)
	}
	changed := next
	changed.Generation, changed.FirecrawlMaxCredits = "3", 5
	if _, err = restarted.Start(ctx, changed); !errors.Is(err, rm.ErrConflict) {
		t.Fatalf("Start изменил cap human intent: %v", err)
	}
	if _, err = restarted.Control(ctx, rm.ControlRequest{Scope: newGeneration.Scope, MissionID: newGeneration.MissionID, Actor: "human", Command: "cancel", ExpectedRevision: newGeneration.Revision}); err != nil {
		t.Fatal(err)
	}
}
