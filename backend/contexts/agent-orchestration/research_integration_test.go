package agentorchestration_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"lead-gen/backend/contexts/agent-orchestration/internal/adapters/persistence/postgres"
	"lead-gen/backend/contexts/agent-orchestration/internal/app/research"
	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
	"lead-gen/backend/platform/migrate"
	rm "lead-gen/contracts/agent-orchestration/researchmission"
	rp "lead-gen/contracts/agent-orchestration/researchplan"
	ra "lead-gen/contracts/integration-platform/researchacquisition"
	memory "lead-gen/contracts/lead-management/researchmemory"
)

type analyzerFunc func(context.Context, research.AnalysisRequest) (research.AnalysisResult, error)

func (f analyzerFunc) Analyze(c context.Context, r research.AnalysisRequest) (research.AnalysisResult, error) {
	return f(c, r)
}

type sourceDouble struct {
	mu          sync.Mutex
	provider    string
	calls       int
	lookups     int
	receipts    map[string]ra.Receipt
	before      func(ra.Action)
	fail        error
	geoOverride *ra.Geography
	transform   func(*ra.Receipt)
	crash       bool
}

func (f *sourceDouble) Capabilities() ra.Capabilities {
	return ra.Capabilities{Provider: f.provider, Revision: "v1", Search: true, Fetch: true}
}
func (f *sourceDouble) Search(ctx context.Context, r ra.SearchRequest) (ra.Receipt, error) {
	return f.execute(r.Action)
}
func (f *sourceDouble) Fetch(ctx context.Context, r ra.FetchRequest) (ra.Receipt, error) {
	return f.execute(r.Action)
}
func (f *sourceDouble) execute(a ra.Action) (ra.Receipt, error) {
	f.mu.Lock()
	f.calls++
	before := f.before
	fail := f.fail
	f.mu.Unlock()
	if before != nil {
		before(a)
	}
	if fail != nil {
		return ra.Receipt{}, fail
	}
	receipt := ra.Receipt{Scope: a.Scope, ActionID: a.ActionID, Attempt: a.Attempt, OperationID: "operation-" + a.ActionID, Provider: f.provider, ProfileRevision: a.ProfileRevision, Status: "success", Geography: ra.Geography{TargetCountry: "RU", Language: "ru"}, ObservedAt: time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)}
	for i := 0; i < 3; i++ {
		url := fmt.Sprintf("https://company%d.example/", i)
		receipt.Hits = append(receipt.Hits, ra.Hit{URL: url, Title: fmt.Sprintf("Компания %d", i), Snippet: "строительство", Rank: i + 1})
		receipt.Artifacts = append(receipt.Artifacts, ra.Artifact{URL: url, Content: fmt.Sprintf("Компания %d строит здания", i), Representation: "markdown", SHA256: fmt.Sprint(i), ObservedAt: receipt.ObservedAt})
	}
	if f.geoOverride != nil {
		receipt.Geography = *f.geoOverride
	}
	if f.transform != nil {
		f.transform(&receipt)
	}
	f.mu.Lock()
	if f.receipts == nil {
		f.receipts = map[string]ra.Receipt{}
	}
	f.receipts[a.ActionID] = receipt
	crash := f.crash
	f.crash = false
	f.mu.Unlock()
	if crash {
		panic("имитация остановки после provider commit")
	}
	return receipt, nil
}
func (f *sourceDouble) Lookup(ctx context.Context, scope ra.Scope, id string, attempt int) (ra.Receipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups++
	r, ok := f.receipts[id]
	if !ok {
		return ra.Receipt{}, ra.ErrUnknown
	}
	return r, nil
}

type memoryDouble struct {
	memory.Port
	mu           sync.Mutex
	observations map[string]memory.SaveObservationRequest
	revoked      map[string]bool
	calls        int
	after        func()
	fail         error
}

func (f *memoryDouble) Get(_ context.Context, r memory.GetRequest) (memory.Dossier, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := memory.Dossier{Entity: memory.Entity{ID: r.EntityID, Revision: 1}}
	found := false
	for _, observation := range f.observations {
		if observation.Scope != r.Scope || observation.Subject.Key != r.EntityID {
			continue
		}
		found = true
		out.Entity.Name = observation.Subject.Name
		source := observation.Source
		source.ID = "source-" + observation.ObservationID
		out.Sources = append(out.Sources, source)
		access := "available"
		if f.revoked[observation.ObservationID] {
			access = "revoked"
		}
		for _, claim := range observation.Claims {
			out.Assertions = append(out.Assertions, memory.Assertion{ID: observation.ObservationID + claim.Predicate, EntityID: r.EntityID, SourceID: source.ID, ObservationID: observation.ObservationID, Predicate: claim.Predicate, Value: claim.Value, Locator: claim.Locator, Knowledge: "observed", Freshness: "current", Access: access, SupportState: "satisfied"})
		}
	}
	if !found {
		return memory.Dossier{}, memory.ErrNotFound
	}
	return out, nil
}
func (f *memoryDouble) SaveObservation(ctx context.Context, r memory.SaveObservationRequest) (memory.SaveObservationResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.fail != nil {
		return memory.SaveObservationResult{}, f.fail
	}
	if f.observations == nil {
		f.observations = map[string]memory.SaveObservationRequest{}
	}
	_, duplicate := f.observations[r.ObservationID]
	f.observations[r.ObservationID] = r
	if f.after != nil {
		f.after()
	}
	return memory.SaveObservationResult{ObservationID: r.ObservationID, EntityID: r.Subject.Key, Revision: 1, Created: !duplicate, Duplicate: duplicate, HasContact: r.Subject.Key == "entity-0", HasEmail: r.Subject.Key == "entity-0"}, nil
}

type ledgerDouble struct {
	ra.Ledger
	mu     sync.Mutex
	grants []ra.Grant
}

func (f *ledgerDouble) EnsureGrant(_ context.Context, g ra.Grant) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.grants = append(f.grants, g)
	return nil
}
func (f *ledgerDouble) Snapshot(_ context.Context, s ra.Scope, id string) (ra.GrantSnapshot, error) {
	return ra.GrantSnapshot{Grant: ra.Grant{Scope: s, ID: id}, Allocation: ra.Balance{MaxCredits: 4}}, nil
}

func analyze(_ context.Context, r research.AnalysisRequest) (research.AnalysisResult, error) {
	result := research.AnalysisResult{}
	for i, artifact := range r.Receipt.Artifacts {
		id := r.ActionID + fmt.Sprintf("/observation/%d", i)
		result.Observations = append(result.Observations, memory.SaveObservationRequest{Scope: memory.Scope{TenantID: r.Scope.TenantID, Purpose: r.Scope.PurposeID}, ObservationID: id, MissionID: r.MissionID, ActionID: r.ActionID, QueryAttemptID: r.QueryAttemptID, Source: memory.Source{URL: artifact.URL, Representation: artifact.Representation, Content: artifact.Content, Revision: artifact.SHA256, ObservedAt: artifact.ObservedAt}, Subject: memory.Subject{Key: fmt.Sprintf("entity-%d", i), Name: fmt.Sprintf("Компания %d", i), Kind: "company"}, Claims: []memory.Claim{{Predicate: fmt.Sprintf("criteria-evidence:%d:industry:match", r.Brief.Criteria.Version), Value: artifact.Content, Locator: memory.Locator{Start: 0, End: len(artifact.Content), Quote: artifact.Content}}}})
		result.Assessments = append(result.Assessments, research.Assessment{ObservationID: id, CriteriaVersion: r.Brief.Criteria.Version, Accepted: true, Reason: "сохранённое свидетельство соответствует критерию", EvidenceRefs: []string{fmt.Sprintf("%s:%d:industry:match:0:%d", id, r.Brief.Criteria.Version, len(artifact.Content))}})
	}
	return result, nil
}
func request(t *testing.T, now time.Time, providers ...string) rm.StartRequest {
	t.Helper()
	tenant := t.Name()
	brief, err := rp.NewBrief(rp.SearchBrief{ID: "brief", TenantID: tenant, Purpose: "lead-research", RequestID: "human-request", IntentVersion: 1, ActorID: "human", Offer: "Внедряем CRM", ICP: "Строительные компании", Criteria: rp.CriteriaSnapshot{Version: 1, Must: []rp.Criterion{{ID: "industry", Text: "строительство", Origin: rp.OriginHuman}}}, Goal: rp.Goal{Quantity: 2, Unit: rp.UnitCompany}})
	if err != nil {
		t.Fatal(err)
	}
	plan := rp.Plan{ID: "plan", Revision: 1, Brief: brief, BriefID: brief.ID, BriefRevision: brief.Revision, TenantID: tenant, Purpose: brief.Purpose, SchemaVersion: rp.SchemaVersion, PromptVersion: rp.PromptVersion, PlannerStatus: "recipe"}
	for i, provider := range providers {
		family := rp.QueryFamily{ID: fmt.Sprintf("family-%d", i), Country: "RU", Language: "ru", SourceFamily: "web", Question: "найти строительные компании", Query: fmt.Sprintf("строительные компании %d", i), Origin: rp.OriginRecipe, CriteriaRefs: []string{"industry"}}
		plan.Families = append(plan.Families, family)
		plan.Cells = append(plan.Cells, rp.StrategyCell{ID: fmt.Sprintf("cell-%d", i), Country: "RU", Language: "ru", Segment: brief.ICP, SourceID: provider, SourceRevision: "v1", SourceFamily: "web", Question: family.Question, QueryFamilyID: family.ID})
	}
	return rm.StartRequest{Scope: rm.Scope{TenantID: tenant, PurposeID: brief.Purpose}, IntentID: brief.RequestID, Actor: brief.ActorID, Generation: "1", CriteriaVersion: 1, Plan: plan, Limits: rm.TechnicalLimits{MaxActions: 20, MaxAttempts: 2, MaxResultsPerSearch: 10, MaxDepth: 2, MaxBytesPerArtifact: 1 << 20, ActionTimeoutSeconds: 30, Deadline: now.Add(time.Hour)}}
}
func database(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("LEADGEN_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("LEADGEN_POSTGRES_TEST_DSN не задан: требуется реальный PostgreSQL")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	migrations, err := migrate.Load(filepath.Join("migrations", "red"), migrate.OwnSchema("agent-orchestration"))
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range migrations {
		if _, err = pool.Exec(context.Background(), migration.SQL); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = pool.Exec(context.Background(), `DELETE FROM "agent-orchestration".research_missions WHERE tenant_id=$1`, t.Name()); err != nil {
		t.Fatal(err)
	}
	return pool
}
func service(t *testing.T, pool *pgxpool.Pool, now *time.Time, mem *memoryDouble, sources map[string]ra.Port, repo ports.ResearchRepository) *research.Service {
	t.Helper()
	if repo == nil {
		repo = postgres.NewResearchRepository(pool)
	}
	svc, err := research.New(research.Config{Repository: repo, Sources: sources, Memory: mem, Analyzer: analyzerFunc(analyze), Now: func() time.Time { return *now }, Ledger: &ledgerDouble{}, FirecrawlAccountID: "account"})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}
func advance(t *testing.T, svc *research.Service, snapshot rm.Snapshot, limit int) rm.Snapshot {
	t.Helper()
	for i := 0; i < limit; i++ {
		var err error
		snapshot, err = svc.Step(context.Background(), snapshot.Scope, snapshot.MissionID)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.State == "completed" {
			return snapshot
		}
	}
	t.Fatalf("миссия не завершилась: %+v", snapshot)
	return snapshot
}

func TestResearchPGPluralResultsCreditsAndIsolation(t *testing.T) {
	pool := database(t)
	now := time.Now().UTC()
	source := &sourceDouble{provider: "proxy"}
	mem := &memoryDouble{}
	svc := service(t, pool, &now, mem, map[string]ra.Port{"proxy": source}, nil)
	req := request(t, now, "proxy")
	start, err := svc.Start(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	again, err := svc.Start(context.Background(), req)
	if err != nil || again.MissionID != start.MissionID {
		t.Fatalf("идемпотентный Start: %v", err)
	}
	modified := req
	modified.Plan.Brief.ICP = "Другая аудитория"
	if _, err = svc.Start(context.Background(), modified); !errors.Is(err, rm.ErrConflict) {
		t.Fatalf("повтор изменил intent: %v", err)
	}
	if _, err = svc.Get(context.Background(), rm.Scope{TenantID: "чужой", PurposeID: req.Scope.PurposeID}, start.MissionID); !errors.Is(err, rm.ErrNotFound) {
		t.Fatalf("изоляция tenant: %v", err)
	}
	source.before = func(_ ra.Action) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if _, err := pool.Exec(ctx, `UPDATE "agent-orchestration".research_missions SET updated_at=updated_at WHERE tenant_id=$1 AND mission_id=$2`, req.Scope.TenantID, start.MissionID); err != nil {
			t.Errorf("сеть вызвана внутри mission транзакции: %v", err)
		}
	}
	first, err := svc.Step(context.Background(), start.Scope, start.MissionID)
	if err != nil {
		t.Fatal(err)
	}
	if first.Counters.Discovered != 3 || first.Counters.Saved != 0 {
		t.Fatalf("extracted != saved: %+v", first.Counters)
	}
	finish := advance(t, svc, first, 5)
	if finish.Goal.Quantity != 2 || finish.Counters.Saved != 3 || finish.Counters.Accepted != 2 || finish.Outcome != "goal-reached" {
		t.Fatalf("неверный итог: %+v", finish)
	}
	if source.calls != 1 {
		t.Fatalf("Search повторён: %d", source.calls)
	}
	if _, err = svc.Control(context.Background(), rm.ControlRequest{Scope: finish.Scope, MissionID: finish.MissionID, Actor: "human", Command: "resume", ExpectedRevision: finish.Revision}); !errors.Is(err, rm.ErrInvalid) {
		t.Fatalf("terminal resume: %v", err)
	}
}

func TestResearchPGRestartRecoversProviderReceiptWithoutDispatch(t *testing.T) {
	pool := database(t)
	now := time.Now().UTC()
	source := &sourceDouble{provider: "proxy", crash: true}
	mem := &memoryDouble{}
	sources := map[string]ra.Port{"proxy": source}
	svc := service(t, pool, &now, mem, sources, nil)
	start, err := svc.Start(context.Background(), request(t, now, "proxy"))
	if err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("ожидалось прерывание worker")
			}
		}()
		_, _ = svc.Step(context.Background(), start.Scope, start.MissionID)
	}()
	now = now.Add(7 * time.Minute)
	restarted := service(t, pool, &now, mem, sources, nil)
	finish := advance(t, restarted, start, 6)
	if finish.Counters.Accepted != 2 || source.calls != 1 || source.lookups != 1 {
		t.Fatalf("восстановление: accepted=%d calls=%d lookup=%d", finish.Counters.Accepted, source.calls, source.lookups)
	}
}

type failingRepository struct {
	ports.ResearchRepository
	fail atomic.Bool
}

func (r *failingRepository) Update(ctx context.Context, s rm.Scope, id string, fn func(*domain.ResearchMission) error) (*domain.ResearchMission, error) {
	if r.fail.Swap(false) {
		return nil, errors.New("имитация потери mission commit")
	}
	return r.ResearchRepository.Update(ctx, s, id, fn)
}
func TestResearchPGRestartAfterOwnerCommitDoesNotDuplicateMembership(t *testing.T) {
	pool := database(t)
	now := time.Now().UTC()
	source := &sourceDouble{provider: "proxy"}
	mem := &memoryDouble{}
	repo := &failingRepository{ResearchRepository: postgres.NewResearchRepository(pool)}
	svc := service(t, pool, &now, mem, map[string]ra.Port{"proxy": source}, repo)
	start, err := svc.Start(context.Background(), request(t, now, "proxy"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err = svc.Step(context.Background(), start.Scope, start.MissionID); err != nil {
			t.Fatal(err)
		}
	}
	mem.after = func() { repo.fail.Store(true); mem.after = nil }
	if _, err = svc.Step(context.Background(), start.Scope, start.MissionID); err == nil {
		t.Fatal("ожидалась потеря commit")
	}
	partial, err := svc.Get(context.Background(), start.Scope, start.MissionID)
	if err != nil {
		t.Fatal(err)
	}
	if partial.Counters.Saved != 0 {
		t.Fatal("owner receipt ещё не вошёл в membership")
	}
	now = now.Add(7 * time.Minute)
	finish := advance(t, svc, start, 5)
	if finish.Counters.Saved != 3 || finish.Counters.Accepted != 2 || len(mem.observations) != 3 || mem.calls != 4 {
		t.Fatalf("дедуп: %+v, observations=%d calls=%d", finish.Counters, len(mem.observations), mem.calls)
	}
}

func TestResearchPGConcurrentWorkersDispatchOnce(t *testing.T) {
	pool := database(t)
	now := time.Now().UTC()
	started := make(chan struct{})
	release := make(chan struct{})
	source := &sourceDouble{provider: "proxy", before: func(ra.Action) { close(started); <-release }}
	mem := &memoryDouble{}
	svc := service(t, pool, &now, mem, map[string]ra.Port{"proxy": source}, nil)
	start, err := svc.Start(context.Background(), request(t, now, "proxy"))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, e := svc.Step(context.Background(), start.Scope, start.MissionID); done <- e }()
	<-started
	waiting, err := svc.Step(context.Background(), start.Scope, start.MissionID)
	if err != nil {
		t.Fatal(err)
	}
	if waiting.WaitingReason != "source-backoff-or-worker" {
		t.Errorf("ожидание не объяснено: %s", waiting.WaitingReason)
	}
	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if source.calls != 1 {
		t.Fatalf("двойной dispatch: %d", source.calls)
	}
}

func TestResearchPGFirecrawlExhaustionContinuesProxy(t *testing.T) {
	pool := database(t)
	now := time.Now().UTC()
	fc := &sourceDouble{provider: "firecrawl", fail: ra.ErrBudgetExhausted}
	proxy := &sourceDouble{provider: "proxy"}
	mem := &memoryDouble{}
	svc := service(t, pool, &now, mem, map[string]ra.Port{"fc": fc, "proxy": proxy}, nil)
	start, err := svc.Start(context.Background(), request(t, now, "fc", "proxy"))
	if err != nil {
		t.Fatal(err)
	}
	first, err := svc.Step(context.Background(), start.Scope, start.MissionID)
	if err != nil {
		t.Fatal(err)
	}
	if first.State == "completed" || !first.Firecrawl.Exhausted {
		t.Fatalf("Firecrawl остановил остальные источники: %+v", first)
	}
	finish := advance(t, svc, first, 8)
	if finish.Counters.Accepted != 2 || fc.calls != 1 || proxy.calls != 1 {
		t.Fatalf("proxy не продолжил: %+v", finish)
	}
}

func TestResearchPGCancelledLateReceiptFreezesComposition(t *testing.T) {
	pool := database(t)
	now := time.Now().UTC()
	started := make(chan struct{})
	release := make(chan struct{})
	source := &sourceDouble{provider: "proxy", before: func(ra.Action) { close(started); <-release }}
	mem := &memoryDouble{}
	svc := service(t, pool, &now, mem, map[string]ra.Port{"proxy": source}, nil)
	start, err := svc.Start(context.Background(), request(t, now, "proxy"))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, e := svc.Step(context.Background(), start.Scope, start.MissionID); done <- e }()
	<-started
	active, err := svc.Get(context.Background(), start.Scope, start.MissionID)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := svc.Control(context.Background(), rm.ControlRequest{Scope: active.Scope, MissionID: active.MissionID, Actor: "human", Command: "cancel", ExpectedRevision: active.Revision})
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		cancelled, err = svc.Step(context.Background(), start.Scope, start.MissionID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if cancelled.State != "cancelled" || cancelled.Counters.Accepted != 0 || cancelled.Counters.Saved != 0 || cancelled.Counters.LateUpdates != 3 || len(mem.observations) != 3 {
		t.Fatalf("поздний результат переписал snapshot: %+v", cancelled)
	}
}

func TestResearchPGOwnerFailureRemainsVisibleWithoutCredit(t *testing.T) {
	pool := database(t)
	now := time.Now().UTC()
	source := &sourceDouble{provider: "proxy"}
	mem := &memoryDouble{fail: errors.New("хранилище владельца недоступно")}
	svc := service(t, pool, &now, mem, map[string]ra.Port{"proxy": source}, nil)
	start, err := svc.Start(context.Background(), request(t, now, "proxy"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err = svc.Step(context.Background(), start.Scope, start.MissionID); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := svc.Step(context.Background(), start.Scope, start.MissionID)
	if err == nil {
		t.Fatal("ожидалась ошибка owner commit")
	}
	if snapshot.Counters.Discovered != 3 || snapshot.Counters.Saved != 0 || snapshot.Counters.Accepted != 0 || snapshot.WaitingReason != "owner-persistence-failed" {
		t.Fatalf("ошибка скрыта: %+v", snapshot)
	}
	mem.fail = nil
	now = now.Add(11 * time.Second)
	finish := advance(t, svc, start, 4)
	if finish.Counters.Accepted != 2 || source.calls != 1 {
		t.Fatalf("recovery повторил acquisition: %+v", finish)
	}
}
func TestResearchPGPauseResumeDoesNotChangeLimits(t *testing.T) {
	pool := database(t)
	now := time.Now().UTC()
	source := &sourceDouble{provider: "proxy"}
	svc := service(t, pool, &now, &memoryDouble{}, map[string]ra.Port{"proxy": source}, nil)
	req := request(t, now, "proxy")
	start, err := svc.Start(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	paused, err := svc.Control(context.Background(), rm.ControlRequest{Scope: start.Scope, MissionID: start.MissionID, Actor: "human", Command: "pause", Reason: "Проверка оператором", ExpectedRevision: start.Revision})
	if err != nil {
		t.Fatal(err)
	}
	paused, err = svc.Step(context.Background(), paused.Scope, paused.MissionID)
	if err != nil {
		t.Fatal(err)
	}
	if source.calls != 0 || paused.State != "paused" || paused.WaitingReason != "Проверка оператором" {
		t.Fatalf("пауза нарушена: %+v", paused)
	}
	if _, err = svc.Control(context.Background(), rm.ControlRequest{Scope: start.Scope, MissionID: start.MissionID, Actor: "human", Command: "resume", ExpectedRevision: start.Revision}); !errors.Is(err, rm.ErrConflict) {
		t.Fatalf("stale resume принят: %v", err)
	}
	resumed, err := svc.Control(context.Background(), rm.ControlRequest{Scope: paused.Scope, MissionID: paused.MissionID, Actor: "human", Command: "resume", ExpectedRevision: paused.Revision})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := postgres.NewResearchRepository(pool).Get(context.Background(), start.Scope, start.MissionID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Request.Limits != req.Limits {
		t.Fatal("resume расширил технические пределы")
	}
	_ = advance(t, svc, resumed, 5)
}
func TestResearchPGEmailPresenceIsNotEmailReadiness(t *testing.T) {
	pool := database(t)
	now := time.Now().UTC()
	source := &sourceDouble{provider: "proxy"}
	svc := service(t, pool, &now, &memoryDouble{}, map[string]ra.Port{"proxy": source}, nil)
	req := request(t, now, "proxy")
	req.Plan.Brief.Goal.Unit = rp.UnitEmailReady
	start, err := svc.Start(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	finish := advance(t, svc, start, 5)
	if finish.Counters.Saved != 3 || finish.Counters.Accepted != 0 || finish.Counters.EmailReady != 0 || !finish.Members[0].HasEmail || finish.Members[0].Reason != "email-policy-readiness-not-confirmed" {
		t.Fatalf("email автоматически объявлен адресатом: %+v", finish)
	}
}
func TestResearchPGDeadlineAndDuplicateStart(t *testing.T) {
	pool := database(t)
	now := time.Now().UTC()
	source := &sourceDouble{provider: "proxy"}
	svc := service(t, pool, &now, &memoryDouble{}, map[string]ra.Port{"proxy": source}, nil)
	req := request(t, now, "proxy")
	start, err := svc.Start(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Hour)
	finish, err := svc.Step(context.Background(), start.Scope, start.MissionID)
	if err != nil {
		t.Fatal(err)
	}
	if finish.StopReason != "technical-deadline" || source.calls != 0 {
		t.Fatalf("deadline не закрыл admission: %+v", finish)
	}
	repeated, err := svc.Start(context.Background(), req)
	if err != nil || repeated.MissionID != start.MissionID || repeated.State != "completed" {
		t.Fatalf("повтор Start после deadline: %+v %v", repeated, err)
	}
}
func TestResearchPGAnalyzerCannotIngestOtherScope(t *testing.T) {
	pool := database(t)
	now := time.Now().UTC()
	source := &sourceDouble{provider: "proxy"}
	mem := &memoryDouble{}
	svc, err := research.New(research.Config{Repository: postgres.NewResearchRepository(pool), Sources: map[string]ra.Port{"proxy": source}, Memory: mem, Now: func() time.Time { return now }, Analyzer: analyzerFunc(func(ctx context.Context, in research.AnalysisRequest) (research.AnalysisResult, error) {
		out, err := analyze(ctx, in)
		out.Observations[0].Scope.TenantID = "чужой"
		return out, err
	})})
	if err != nil {
		t.Fatal(err)
	}
	req := request(t, now, "proxy")
	req.Limits.MaxAttempts = 1
	start, err := svc.Start(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	finish := advance(t, svc, start, 5)
	if mem.calls != 0 || finish.Counters.Accepted != 0 || finish.Actions[0].Reason != "analysis-failed" {
		t.Fatalf("непроверенный scope дошёл до памяти: %+v", finish)
	}
}

type searchOnlyDouble struct{ sourceDouble }

func (f *searchOnlyDouble) Capabilities() ra.Capabilities {
	return ra.Capabilities{Provider: "searxng", Revision: "v1", Search: true}
}
func (f *searchOnlyDouble) Search(ctx context.Context, r ra.SearchRequest) (ra.Receipt, error) {
	out, err := f.sourceDouble.Search(ctx, r)
	out.Artifacts = nil
	return out, err
}
func (f *searchOnlyDouble) Fetch(context.Context, ra.FetchRequest) (ra.Receipt, error) {
	return ra.Receipt{}, errors.New("Search-only источник не должен получать Fetch")
}
func TestResearchPGSearchOnlyRoutesFetchThroughExplicitCapability(t *testing.T) {
	pool := database(t)
	now := time.Now().UTC()
	search := &searchOnlyDouble{sourceDouble: sourceDouble{provider: "searxng"}}
	fetch := &sourceDouble{provider: "firecrawl"}
	ledger := &ledgerDouble{}
	svc, err := research.New(research.Config{Repository: postgres.NewResearchRepository(pool), Sources: map[string]ra.Port{"searx": search, "fc": fetch}, FetchSourceIDs: []string{"fc"}, Ledger: ledger, FirecrawlAccountID: "account", Memory: &memoryDouble{}, Analyzer: analyzerFunc(analyze), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	start, err := svc.Start(context.Background(), request(t, now, "searx"))
	if err != nil {
		t.Fatal(err)
	}
	if start.Firecrawl.GrantRef == "" {
		t.Fatal("будущий Fetch не связан с grant человеческого intent")
	}
	finish := advance(t, svc, start, 10)
	if finish.Counters.Accepted != 2 || search.calls != 1 || fetch.calls != 1 {
		t.Fatalf("маршрут Search→Fetch: %+v", finish)
	}
	stored, err := postgres.NewResearchRepository(pool).Get(context.Background(), start.Scope, start.MissionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range stored.Actions {
		if action.Kind == "fetch" && (action.SourceID != "fc" || action.DiscoverySourceID != "searx") {
			t.Fatalf("происхождение поиска потеряно: %+v", action)
		}
	}
}

type identityMemoryDouble struct {
	*memoryDouble
	revision    int64
	canonical   map[string]string
	unavailable map[string]bool
}

func (m *identityMemoryDouble) ResolveIdentityBatch(_ context.Context, r memory.IdentityBatchRequest) ([]memory.IdentityEntityState, error) {
	states := []memory.IdentityEntityState{}
	for _, id := range r.EntityIDs {
		canonical := id
		if m.canonical[id] != "" {
			canonical = m.canonical[id]
		}
		states = append(states, memory.IdentityEntityState{EntityID: id, EntityRevision: 1, CanonicalEntityID: canonical, IdentityRevision: m.revision, Available: !m.unavailable[id], HasContact: id == "entity-0", HasEmail: id == "entity-0"})
	}
	return states, nil
}
func TestResearchPGActiveIdentityProjectionMergesAndSplitsWithoutNewCalls(t *testing.T) {
	pool := database(t)
	now := time.Now().UTC()
	source := &sourceDouble{provider: "proxy"}
	mem := &identityMemoryDouble{memoryDouble: &memoryDouble{}, revision: 1, canonical: map[string]string{}, unavailable: map[string]bool{}}
	svc, err := research.New(research.Config{Repository: postgres.NewResearchRepository(pool), Sources: map[string]ra.Port{"proxy": source}, Memory: mem, Analyzer: analyzerFunc(analyze), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	req := request(t, now, "proxy")
	req.Plan.Brief.Goal.Quantity = 6
	start, err := svc.Start(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	var current rm.Snapshot
	for i := 0; i < 3; i++ {
		current, err = svc.Step(context.Background(), start.Scope, start.MissionID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if current.Counters.Accepted != 3 {
		t.Fatalf("начальный зачёт: %+v", current)
	}
	current, err = svc.Control(context.Background(), rm.ControlRequest{Scope: current.Scope, MissionID: current.MissionID, Actor: "human", Command: "pause", Reason: "Сверка тождества", ExpectedRevision: current.Revision})
	if err != nil {
		t.Fatal(err)
	}
	mem.canonical["entity-1"] = "entity-0"
	mem.revision = 2
	current, err = svc.Step(context.Background(), start.Scope, start.MissionID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Counters.Accepted != 2 || current.Counters.Saved != 2 || len(current.Members[0].OriginalEntityIDs) != 2 {
		t.Fatalf("merge projection: %+v", current)
	}
	delete(mem.canonical, "entity-1")
	mem.revision = 3
	current, err = svc.Step(context.Background(), start.Scope, start.MissionID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Counters.Accepted != 3 || current.Counters.Saved != 3 {
		t.Fatalf("split projection: %+v", current)
	}
	mem.unavailable["entity-1"] = true
	mem.revision = 4
	current, err = svc.Step(context.Background(), start.Scope, start.MissionID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Counters.Accepted != 2 || source.calls != 1 {
		t.Fatalf("withdrawal projection повторил acquisition или сохранил credit: %+v", current)
	}
}

func TestResearchPGPausedDeadlineRunsThroughDurableQueue(t *testing.T) {
	pool := database(t)
	now := time.Now().UTC()
	source := &sourceDouble{provider: "proxy"}
	svc := service(t, pool, &now, &memoryDouble{}, map[string]ra.Port{"proxy": source}, nil)
	start, err := svc.Start(context.Background(), request(t, now, "proxy"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Control(context.Background(), rm.ControlRequest{Scope: start.Scope, MissionID: start.MissionID, Actor: "human", Command: "pause", Reason: "Проверка", ExpectedRevision: start.Revision})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Hour)
	if err = svc.RunReady(context.Background(), 1000); err != nil {
		t.Fatal(err)
	}
	finish, err := svc.Get(context.Background(), start.Scope, start.MissionID)
	if err != nil {
		t.Fatal(err)
	}
	if finish.State != "completed" || finish.StopReason != "technical-deadline" || source.calls != 0 {
		t.Fatalf("paused deadline отсутствует в durable queue: %+v", finish)
	}
}

func TestResearchPGReceiptGeographyMustMatchAction(t *testing.T) {
	for name, geo := range map[string]ra.Geography{
		"missing":          {},
		"missing-language": {TargetCountry: "RU"},
		"other-country":    {TargetCountry: "GB", Language: "ru"},
		"other-language":   {TargetCountry: "RU", Language: "en"},
	} {
		t.Run(name, func(t *testing.T) {
			pool := database(t)
			now := time.Now().UTC()
			source := &sourceDouble{provider: "proxy", geoOverride: &geo}
			mem := &memoryDouble{}
			svc := service(t, pool, &now, mem, map[string]ra.Port{"proxy": source}, nil)
			start, err := svc.Start(context.Background(), request(t, now, "proxy"))
			if err != nil {
				t.Fatal(err)
			}
			finish := advance(t, svc, start, 3)
			if finish.Actions[0].State != "quarantined" || finish.Actions[0].Reason != "receipt-geography-mismatch" || finish.Counters.Saved != 0 || mem.calls != 0 {
				t.Fatalf("география не изолирована до анализа/памяти: %+v", finish)
			}
		})
	}
}

func TestResearchPGAnalysisDiagnosticsSurviveRestartWithProvenance(t *testing.T) {
	pool := database(t)
	now := time.Now().UTC()
	source := &sourceDouble{provider: "proxy"}
	mem := &memoryDouble{}
	calls := 0
	newService := func() *research.Service {
		svc, err := research.New(research.Config{Repository: postgres.NewResearchRepository(pool), Sources: map[string]ra.Port{"proxy": source}, Memory: mem, Now: func() time.Time { return now }, Analyzer: analyzerFunc(func(ctx context.Context, in research.AnalysisRequest) (research.AnalysisResult, error) {
			calls++
			out, err := analyze(ctx, in)
			out.Diagnostics = []string{"subject-limit-reached", "market-evidence-unknown"}
			return out, err
		})})
		if err != nil {
			t.Fatal(err)
		}
		return svc
	}
	svc := newService()
	start, err := svc.Start(context.Background(), request(t, now, "proxy"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err = svc.Step(context.Background(), start.Scope, start.MissionID); err != nil {
			t.Fatal(err)
		}
	}
	restarted := newService()
	saved, err := restarted.Get(context.Background(), start.Scope, start.MissionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Warnings) != 2 || len(saved.Actions[0].Diagnostics) != 2 {
		t.Fatalf("диагностика потеряна: %+v", saved)
	}
	for _, warning := range saved.Warnings {
		if warning.ActionID != saved.Actions[0].ID || warning.SourceID != "proxy" || warning.Country != "RU" || warning.Language != "ru" {
			t.Fatalf("нет происхождения: %+v", warning)
		}
	}
	finish := advance(t, restarted, saved, 3)
	if calls != 1 || source.calls != 1 || len(finish.Warnings) != 2 {
		t.Fatalf("анализ повторён или предупреждения потеряны: calls=%d snapshot=%+v", calls, finish)
	}
}

func TestResearchPGAnalysisDiagnosticsRejectUnboundedOrRawText(t *testing.T) {
	for name, diagnostics := range map[string][]string{
		"too-many": strings.Fields(strings.Repeat("bounded-code ", 65)),
		"too-long": {strings.Repeat("a", 129)},
		"raw-text": {"Provider returned https://example.test?token=private"},
	} {
		t.Run(name, func(t *testing.T) {
			pool := database(t)
			now := time.Now().UTC()
			mem := &memoryDouble{}
			svc, err := research.New(research.Config{Repository: postgres.NewResearchRepository(pool), Sources: map[string]ra.Port{"proxy": &sourceDouble{provider: "proxy"}}, Memory: mem, Now: func() time.Time { return now }, Analyzer: analyzerFunc(func(ctx context.Context, in research.AnalysisRequest) (research.AnalysisResult, error) {
				out, err := analyze(ctx, in)
				out.Diagnostics = diagnostics
				return out, err
			})})
			if err != nil {
				t.Fatal(err)
			}
			req := request(t, now, "proxy")
			req.Limits.MaxAttempts = 1
			start, err := svc.Start(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			finish := advance(t, svc, start, 4)
			if finish.Actions[0].Reason != "analysis-failed" || len(finish.Warnings) != 0 || mem.calls != 0 {
				t.Fatalf("неограниченная диагностика принята: %+v", finish)
			}
		})
	}
}

func TestResearchPGSameNameCandidatesRemainSavedWithoutDuplicateCredit(t *testing.T) {
	pool := database(t)
	now := time.Now().UTC()
	source := &sourceDouble{provider: "proxy", transform: func(receipt *ra.Receipt) {
		receipt.Artifacts = receipt.Artifacts[:2]
		receipt.Hits = receipt.Hits[:2]
		for i := range receipt.Artifacts {
			receipt.Artifacts[i].Content = "Поревит строит здания"
		}
	}}
	mem := &memoryDouble{}
	svc, err := research.New(research.Config{Repository: postgres.NewResearchRepository(pool), Sources: map[string]ra.Port{"proxy": source}, Memory: mem, Now: func() time.Time { return now }, Analyzer: analyzerFunc(func(ctx context.Context, in research.AnalysisRequest) (research.AnalysisResult, error) {
		out, err := analyze(ctx, in)
		for i := range out.Observations {
			out.Observations[i].Subject.Name = "Поревит"
		}
		return out, err
	})})
	if err != nil {
		t.Fatal(err)
	}
	start, err := svc.Start(context.Background(), request(t, now, "proxy"))
	if err != nil {
		t.Fatal(err)
	}
	finish := advance(t, svc, start, 5)
	if finish.Counters.Saved != 2 || finish.Counters.Accepted != 1 || finish.Outcome != "partial" || finish.Members[1].Reason != "identity-review-required" || len(finish.Warnings) != 1 {
		t.Fatalf("две страницы одной компании засчитаны как две компании: %+v", finish)
	}
	if finish.Members[0].EntityID == finish.Members[1].EntityID || finish.Members[1].HasContact {
		t.Fatal("слабое имя объединило тождество или перенесло контакты")
	}
}

type lateContactMemory struct{ *memoryDouble }

func (m *lateContactMemory) SaveObservation(ctx context.Context, r memory.SaveObservationRequest) (memory.SaveObservationResult, error) {
	out, err := m.memoryDouble.SaveObservation(ctx, r)
	if err != nil {
		return out, err
	}
	out.HasContact, out.HasEmail = false, false
	for _, claim := range r.Claims {
		if claim.Predicate == "email" {
			out.HasContact, out.HasEmail = true, true
		}
	}
	return out, nil
}
func TestResearchPGLateContactPreservesOwnCriteriaUntilSourceRevocation(t *testing.T) {
	pool := database(t)
	now := time.Now().UTC()
	source := &sourceDouble{provider: "proxy"}
	mem := &lateContactMemory{&memoryDouble{revoked: map[string]bool{}}}
	calls := 0
	var profileObservation string
	newService := func() *research.Service {
		svc, err := research.New(research.Config{Repository: postgres.NewResearchRepository(pool), Sources: map[string]ra.Port{"profile": source, "contacts": source, "reserve": source}, Memory: mem, Now: func() time.Time { return now }, Analyzer: analyzerFunc(func(ctx context.Context, in research.AnalysisRequest) (research.AnalysisResult, error) {
			calls++
			out, err := analyze(ctx, in)
			out.Observations, out.Assessments = out.Observations[:1], out.Assessments[:1]
			if calls == 1 {
				profileObservation = out.Observations[0].ObservationID
			} else {
				out.Observations[0].Claims = []memory.Claim{{Predicate: "email", Value: "hello@company0.example"}}
				out.Assessments[0].Accepted = false
				out.Assessments[0].Reason = "criteria-evidence-unknown"
				out.Assessments[0].EvidenceRefs = nil
			}
			return out, err
		})})
		if err != nil {
			t.Fatal(err)
		}
		return svc
	}
	svc := newService()
	req := request(t, now, "profile", "contacts", "reserve")
	req.Plan.Brief.Goal.Unit = rp.UnitCompanyWithContact
	current, err := svc.Start(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		current, err = svc.Step(context.Background(), current.Scope, current.MissionID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(current.Members) != 1 || !current.Members[0].CriteriaAccepted || current.Members[0].HasContact || current.Counters.Accepted != 0 {
		t.Fatalf("профиль без контакта: %+v", current)
	}
	for i := 0; i < 3; i++ {
		current, err = svc.Step(context.Background(), current.Scope, current.MissionID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if current.Counters.Accepted != 1 || !current.Members[0].HasContact || len(current.Members[0].AssessmentSets) != 2 {
		t.Fatalf("поздний контакт стёр критерии: %+v", current)
	}
	current, err = svc.Control(context.Background(), rm.ControlRequest{Scope: current.Scope, MissionID: current.MissionID, Actor: "human", Command: "pause", Reason: "Сверка источника", ExpectedRevision: current.Revision})
	if err != nil {
		t.Fatal(err)
	}
	restarted := newService()
	current, err = restarted.Step(context.Background(), current.Scope, current.MissionID)
	if err != nil || current.Counters.Accepted != 1 {
		t.Fatalf("restart потерял полный набор: %+v %v", current, err)
	}
	mem.revoked[profileObservation] = true
	current, err = restarted.Step(context.Background(), current.Scope, current.MissionID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Counters.Accepted != 0 || current.Members[0].CriteriaAccepted || !current.Members[0].HasContact || len(current.Members[0].AssessmentSets) != 2 || calls != 2 || source.calls != 2 {
		t.Fatalf("отзыв источника не снял критерий или повторил сеть: %+v calls=%d source=%d", current, calls, source.calls)
	}
	lastProgress := current.LastProgressAt
	now = now.Add(time.Minute)
	current, err = restarted.Step(context.Background(), current.Scope, current.MissionID)
	if err != nil || !current.LastProgressAt.Equal(lastProgress) {
		t.Fatalf("повтор той же проекции выдан за новый прогресс: %+v %v", current, err)
	}
}

func TestResearchPGAdmissionContentionDoesNotSpendProviderAttempts(t *testing.T) {
	pool := database(t)
	now := time.Now().UTC()
	source := &sourceDouble{provider: "proxy", fail: ra.ErrThrottled}
	mem := &memoryDouble{}
	svc := service(t, pool, &now, mem, map[string]ra.Port{"proxy": source}, nil)
	req := request(t, now, "proxy")
	req.Limits.MaxAttempts = 1
	current, err := svc.Start(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		current, err = svc.Step(context.Background(), current.Scope, current.MissionID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Actions[0].Attempt != 1 || current.Actions[0].State != "pending" || current.Actions[0].NextAttemptAt == nil {
			t.Fatalf("admission потратил provider attempt: %+v", current)
		}
		now = now.Add(6 * time.Second)
	}
	source.fail = nil
	finish := advance(t, svc, current, 4)
	if finish.Counters.Accepted != 2 || finish.Actions[0].Attempt != 1 {
		t.Fatalf("освободившийся слот не продолжил action: %+v", finish)
	}
}

func TestResearchPGProviderAttemptLimitIsKnownFailure(t *testing.T) {
	pool := database(t)
	now := time.Now().UTC()
	svc := service(t, pool, &now, &memoryDouble{}, map[string]ra.Port{"proxy": &sourceDouble{provider: "proxy", fail: ra.ErrAttemptLimit}}, nil)
	start, err := svc.Start(context.Background(), request(t, now, "proxy"))
	if err != nil {
		t.Fatal(err)
	}
	finish := advance(t, svc, start, 3)
	if finish.Actions[0].State != "failed" || finish.Actions[0].Reason != "source-attempt-limit" || finish.StopReason == "provider-effect-unknown" {
		t.Fatalf("известный технический отказ стал unknown: %+v", finish)
	}
}

type settlementSourceDouble struct {
	sourceDouble
	persistedPartial bool
	lookupMode       string
	responseLost     bool
	noOperation      bool
	unavailable      bool
}

func (s *settlementSourceDouble) Search(ctx context.Context, request ra.SearchRequest) (ra.Receipt, error) {
	if s.noOperation {
		return ra.Receipt{}, ra.ErrUnknown
	}
	out, err := s.sourceDouble.Search(ctx, request)
	if err != nil {
		return out, err
	}
	out.Persistence = "unknown"
	if s.responseLost {
		return ra.Receipt{}, ra.ErrUnknown
	}
	if s.unavailable {
		out.Status, out.Hits, out.Artifacts = "unavailable", nil, nil
	}
	if s.persistedPartial {
		out.Persistence, out.Status, out.CostState = "persisted", "partial", "unknown"
	}
	return out, ra.ErrUnknown
}
func (s *settlementSourceDouble) Lookup(ctx context.Context, scope ra.Scope, id string, attempt int) (ra.Receipt, error) {
	if s.noOperation {
		return ra.Receipt{}, ra.ErrNotFound
	}
	out, err := s.sourceDouble.Lookup(ctx, scope, id, attempt)
	if err != nil {
		return out, err
	}
	switch s.lookupMode {
	case "in-flight":
		return ra.Receipt{}, ra.ErrInFlight
	case "crash":
		s.lookupMode = ""
		panic("имитация остановки во время settlement Lookup")
	case "conflict":
		out.OperationID = "other-operation"
	}
	out.Persistence = "persisted"
	return out, nil
}

func TestResearchPGSettlementLeaseRecoversAfterRestartWithoutRepeatingEffects(t *testing.T) {
	pool := database(t)
	now := time.Now().UTC()
	source := &settlementSourceDouble{sourceDouble: sourceDouble{provider: "proxy"}, lookupMode: "in-flight"}
	mem := &memoryDouble{}
	sources := map[string]ra.Port{"proxy": source}
	svc := service(t, pool, &now, mem, sources, nil)
	current, err := svc.Start(context.Background(), request(t, now, "proxy"))
	if err != nil {
		t.Fatal(err)
	}
	current = advance(t, svc, current, 4)
	frozen := current.Counters
	if current.Actions[0].SettlementState != "pending" {
		t.Fatalf("потеряно обязательство: %+v", current)
	}
	now = now.Add(6 * time.Second)
	current, err = svc.Step(context.Background(), current.Scope, current.MissionID)
	if err != nil || current.Actions[0].SettlementState != "pending" || current.Actions[0].SettlementReason != "receipt-settlement-in-flight" {
		t.Fatalf("незавершённый owner receipt: %+v %v", current, err)
	}
	source.lookupMode = "crash"
	now = now.Add(time.Minute)
	func() {
		defer func() {
			if recover() == nil {
				t.Error("ожидалась остановка settlement worker")
			}
		}()
		_, _ = svc.Step(context.Background(), current.Scope, current.MissionID)
	}()
	now = now.Add(12 * time.Minute)
	restarted := service(t, pool, &now, mem, sources, nil)
	current, err = restarted.Step(context.Background(), current.Scope, current.MissionID)
	if err != nil || current.Actions[0].SettlementState != "settled" || current.Counters != frozen || source.calls != 1 || source.lookups != 3 || mem.calls != 3 {
		t.Fatalf("settlement повторил эффекты или не восстановился: %+v err=%v source=%d lookups=%d memory=%d", current, err, source.calls, source.lookups, mem.calls)
	}
}

func TestResearchPGPersistedPartialUnknownIsNotUnpersistedReceipt(t *testing.T) {
	pool := database(t)
	now := time.Now().UTC()
	source := &settlementSourceDouble{sourceDouble: sourceDouble{provider: "proxy"}, persistedPartial: true}
	svc := service(t, pool, &now, &memoryDouble{}, map[string]ra.Port{"proxy": source}, nil)
	start, err := svc.Start(context.Background(), request(t, now, "proxy"))
	if err != nil {
		t.Fatal(err)
	}
	finish := advance(t, svc, start, 4)
	if finish.Counters.Saved != 3 || finish.Counters.Accepted != 2 || finish.Actions[0].SettlementState != "" || source.lookups != 0 {
		t.Fatalf("полезный durable partial потерян или требует лишнего recovery: %+v", finish)
	}
}

func TestResearchPGSettlementRejectsMismatchedOperationWithoutLosingData(t *testing.T) {
	pool := database(t)
	now := time.Now().UTC()
	source := &settlementSourceDouble{sourceDouble: sourceDouble{provider: "proxy"}, lookupMode: "conflict"}
	svc := service(t, pool, &now, &memoryDouble{}, map[string]ra.Port{"proxy": source}, nil)
	start, err := svc.Start(context.Background(), request(t, now, "proxy"))
	if err != nil {
		t.Fatal(err)
	}
	finish := advance(t, svc, start, 4)
	now = now.Add(6 * time.Second)
	current, err := svc.Step(context.Background(), finish.Scope, finish.MissionID)
	if err != nil || current.Counters != finish.Counters || current.Actions[0].SettlementState != "quarantined" || current.Actions[0].SettlementReason != "receipt-settlement-conflict" {
		t.Fatalf("чужой settlement receipt принят или изменил состав: %+v %v", current, err)
	}
}

func TestResearchPGEmptyUncertainReceiptUsesOwnerLookupWithoutRedispatch(t *testing.T) {
	for _, noOperation := range []bool{false, true} {
		t.Run(fmt.Sprint(noOperation), func(t *testing.T) {
			pool := database(t)
			now := time.Now().UTC()
			source := &settlementSourceDouble{sourceDouble: sourceDouble{provider: "proxy"}, responseLost: true, noOperation: noOperation}
			mem := &memoryDouble{}
			svc := service(t, pool, &now, mem, map[string]ra.Port{"proxy": source}, nil)
			current, err := svc.Start(context.Background(), request(t, now, "proxy"))
			if err != nil {
				t.Fatal(err)
			}
			current, err = svc.Step(context.Background(), current.Scope, current.MissionID)
			if err != nil || current.Actions[0].SettlementState != "pending" {
				t.Fatalf("потерянный ACK не создал obligation: %+v %v", current, err)
			}
			now = now.Add(6 * time.Second)
			current, err = svc.Step(context.Background(), current.Scope, current.MissionID)
			if err != nil || current.Actions[0].SettlementState != "settled" {
				t.Fatalf("owner Lookup не разрешил исход: %+v %v", current, err)
			}
			if noOperation {
				if current.Actions[0].State != "failed" || current.Actions[0].Reason != "provider-operation-not-created" || current.Actions[0].Attempt != 1 || mem.calls != 0 {
					t.Fatalf("отсутствующая операция объявлена выполненной: %+v", current)
				}
			} else {
				finish := advance(t, svc, current, 3)
				if finish.Counters.Saved != 3 || finish.Counters.Accepted != 2 || source.calls != 1 {
					t.Fatalf("полезный persisted receipt потерян или вызван повторный POST: %+v calls=%d", finish, source.calls)
				}
			}
		})
	}
}

func TestResearchPGUnpersistedFailureCannotAdvanceAttempt(t *testing.T) {
	pool := database(t)
	now := time.Now().UTC()
	source := &settlementSourceDouble{sourceDouble: sourceDouble{provider: "proxy"}, unavailable: true}
	svc := service(t, pool, &now, &memoryDouble{}, map[string]ra.Port{"proxy": source}, nil)
	current, err := svc.Start(context.Background(), request(t, now, "proxy"))
	if err != nil {
		t.Fatal(err)
	}
	current, err = svc.Step(context.Background(), current.Scope, current.MissionID)
	if err != nil || current.Actions[0].State != "unknown" || current.Actions[0].Attempt != 1 || current.Actions[0].SettlementState != "pending" {
		t.Fatalf("потерянный owner commit позволил следующий attempt: %+v %v", current, err)
	}
}

type healthScopedRepository struct {
	ports.ResearchRepository
	tenant string
}

func (r healthScopedRepository) Ready(ctx context.Context, limit int) ([]ports.ResearchRef, error) {
	refs, err := r.ResearchRepository.Ready(ctx, limit)
	filtered := []ports.ResearchRef{}
	for _, ref := range refs {
		if ref.Scope.TenantID == r.tenant {
			filtered = append(filtered, ref)
		}
	}
	return filtered, err
}
func TestResearchPGHealthRequiresLiveWorkerAndDatabase(t *testing.T) {
	pool := database(t)
	now := time.Now().UTC()
	repo := healthScopedRepository{ResearchRepository: postgres.NewResearchRepository(pool), tenant: t.Name()}
	svc := service(t, pool, &now, &memoryDouble{}, map[string]ra.Port{"proxy": &sourceDouble{provider: "proxy"}}, repo)
	if err := svc.CheckHealth(context.Background()); err == nil {
		t.Fatal("worker не запущен, но healthy")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- svc.RunWorker(ctx, 1, nil) }()
	deadline := time.Now().Add(2 * time.Second)
	for svc.CheckHealth(context.Background()) != nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := svc.CheckHealth(context.Background()); err != nil {
		t.Fatalf("реальный worker/PG не готов: %v", err)
	}
	pool.Close()
	if err := svc.CheckHealth(context.Background()); err == nil {
		t.Fatal("закрытый PG pool не обнаружен")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := svc.CheckHealth(context.Background()); err == nil {
		t.Fatal("завершённый worker healthy")
	}
}
