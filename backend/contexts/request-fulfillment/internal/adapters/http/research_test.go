package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"lead-gen/backend/contexts/request-fulfillment/internal/app/command"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/query"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/researchbridge"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/backend/platform/consoleauth"
	"lead-gen/contracts/agent-orchestration/researchmission"
	"lead-gen/contracts/agent-orchestration/researchplan"
	"lead-gen/contracts/lead-management/researchmemory"
)

type researchRequestReader struct{ request query.RequestView }

func (f *researchRequestReader) Handle(ctx context.Context, in query.GetRequest) (query.RequestView, error) {
	if string(in.RequestID) != f.request.RequestID || string(in.TenantID) != f.request.TenantID {
		return query.RequestView{}, ports.ErrRequestNotFound
	}
	return f.request, nil
}

type researchApprove struct{}

func (researchApprove) Handle(context.Context, command.ValidateRequest) (command.TransitionResult, error) {
	return command.TransitionResult{}, nil
}

type researchAccess struct{ actor string }

func (f *researchAccess) CanAct(ctx context.Context, actor, tenant string) (bool, error) {
	f.actor = actor
	return actor == "operator" && tenant == "tenant-a", nil
}

type researchPlanner struct{}

func (researchPlanner) Plan(context.Context, researchplan.SearchBrief) (researchplan.Plan, error) {
	return researchplan.Plan{}, errors.New("тест не должен запускать планировщик")
}

type researchMissions struct {
	researchmission.Port
	snapshot researchmission.Snapshot
	err      error
	reads    int
}

func (f *researchMissions) FindByIntent(context.Context, researchmission.Scope, string) ([]researchmission.Snapshot, error) {
	f.reads++
	if f.err != nil {
		return nil, f.err
	}
	return []researchmission.Snapshot{f.snapshot}, nil
}

type researchMemory struct {
	researchmemory.Port
	reads int
}

func (f *researchMemory) Get(ctx context.Context, in researchmemory.GetRequest) (researchmemory.Dossier, error) {
	f.reads++
	return researchmemory.Dossier{Entity: researchmemory.Entity{ID: in.EntityID, Name: "Компания без email", Revision: 3}, Assertions: []researchmemory.Assertion{{Value: "<script>alert(1)</script>", Knowledge: "observed", Freshness: "current", Access: "available", Locator: researchmemory.Locator{Quote: "Компания без email"}}}, Needs: []researchmemory.OpenNeed{{Predicate: "email", KnowledgeState: "open", ExecutionState: "dormant"}}}, nil
}

type researchHTTPFixture struct {
	server        *Server
	session, csrf string
	missions      *researchMissions
	memory        *researchMemory
	access        *researchAccess
}

func newResearchHTTPFixture(t *testing.T, options ...func(*researchbridge.Config)) researchHTTPFixture {
	t.Helper()
	f := researchHTTPFixture{missions: &researchMissions{snapshot: researchmission.Snapshot{MissionID: "mission-a", IntentID: "request-a", Scope: researchmission.Scope{TenantID: "tenant-a", PurposeID: "editorial"}, Revision: 4, Goal: researchplan.Goal{Quantity: 13, Unit: "company"}, State: "running", Counters: researchmission.Counters{Saved: 2, Accepted: 1}, Members: []researchmission.Member{{EntityID: "entity-empty", EntityRevision: 1, Reason: "contact-missing"}, {EntityID: "entity-accepted", Accepted: true, HasContact: true}}}}, memory: &researchMemory{}, access: &researchAccess{}}
	reader := &researchRequestReader{request: query.RequestView{RequestID: "request-a", TenantID: "tenant-a", ExecutionPurpose: "editorial", Status: domain.StatusValidated, Offer: query.OfferView{Content: "Внедрение CRM", Target: "Строительные компании", Revision: 1}}}
	config := researchbridge.Config{Missions: f.missions, Memory: f.memory, Planner: researchPlanner{}, Access: f.access, RunTimeout: time.Hour, Countries: []string{"RU"}, Limits: researchmission.TechnicalLimits{MaxActions: 4, MaxAttempts: 1, MaxResultsPerSearch: 4, MaxBytesPerArtifact: 4096, ActionTimeoutSeconds: 10}}
	for _, option := range options {
		option(&config)
	}
	bridge, err := researchbridge.New(config, reader, researchApprove{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.server = NewServer(Dependencies{Research: bridge, Auth: consoleauth.Config{Username: "operator", Password: "secret"}})
	if f.server.pages == nil {
		t.Fatal("шаблоны не разобраны")
	}
	f.session, err = f.server.sessions.Login("operator", "secret")
	if err != nil {
		t.Fatal(err)
	}
	session, ok := f.server.sessions.Authenticate(f.session)
	if !ok {
		t.Fatal("сессия недоступна")
	}
	f.csrf = session.CSRF()
	return f
}
func (f researchHTTPFixture) request(method, path, body string, authenticated bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if method == http.MethodPost {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if authenticated {
		r.AddCookie(&http.Cookie{Name: consoleauth.CookieName, Value: f.session})
	}
	w := httptest.NewRecorder()
	f.server.ServeHTTP(w, r)
	return w
}

func TestResearchScopeAndSessionProtectAllReadRoutes(t *testing.T) {
	paths := []string{"/requests/request-a/research", "/requests/request-a/research/progress", "/requests/request-a/research/candidates/entity-empty"}
	for _, path := range paths {
		f := newResearchHTTPFixture(t)
		anon := f.request(http.MethodGet, path+"?tenant_id=tenant-a&actor=operator", "", false)
		expected := http.StatusSeeOther
		if strings.HasSuffix(path, "/progress") {
			expected = http.StatusUnauthorized
		}
		if anon.Code != expected {
			t.Fatalf("%s anonymous=%d", path, anon.Code)
		}
		denied := f.request(http.MethodGet, path+"?tenant_id=tenant-b&actor=admin", "", true)
		if denied.Code != http.StatusForbidden || f.missions.reads != 0 || f.memory.reads != 0 {
			t.Fatalf("%s: чужая область прочитана: %d", path, denied.Code)
		}
		if f.access.actor != "operator" {
			t.Fatalf("actor query подменил сессию: %q", f.access.actor)
		}
	}
}
func TestResearchProgressSnapshotAndETag(t *testing.T) {
	f := newResearchHTTPFixture(t)
	path := "/requests/request-a/research/progress?tenant_id=tenant-a&filter=accepted"
	first := f.request(http.MethodGet, path, "", true)
	if first.Code != http.StatusOK {
		t.Fatalf("progress=%d %s", first.Code, first.Body.String())
	}
	body := first.Body.String()
	if !strings.Contains(body, `data-revision="4"`) || !strings.Contains(body, `data-entity-id="entity-accepted"`) || strings.Contains(body, `data-entity-id="entity-empty"`) {
		t.Fatalf("снимок/фильтр: %s", body)
	}
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.AddCookie(&http.Cookie{Name: consoleauth.CookieName, Value: f.session})
	r.Header.Set("If-None-Match", first.Header().Get("ETag"))
	w := httptest.NewRecorder()
	f.server.ServeHTTP(w, r)
	if w.Code != http.StatusNotModified || w.Body.Len() != 0 {
		t.Fatalf("повторный снимок=%d", w.Code)
	}
	f.missions.err = errors.New("storage offline")
	failed := f.request(http.MethodGet, path, "", true)
	if failed.Code != http.StatusServiceUnavailable || strings.Contains(failed.Body.String(), "storage offline") {
		t.Fatalf("недоступность замаскирована или детали раскрыты: %d %s", failed.Code, failed.Body.String())
	}
}
func TestResearchCandidateWithoutEmailHasVisibleEvidence(t *testing.T) {
	f := newResearchHTTPFixture(t)
	page := f.request(http.MethodGet, "/requests/request-a/research?tenant_id=tenant-a", "", true)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Компания без email") || !strings.Contains(page.Body.String(), "Контакт пока не найден") {
		t.Fatalf("кандидат потерян: %d %s", page.Code, page.Body.String())
	}
	dossier := f.request(http.MethodGet, "/requests/request-a/research/candidates/entity-empty?tenant_id=tenant-a", "", true)
	body := dossier.Body.String()
	if dossier.Code != http.StatusOK || !strings.Contains(body, "Активный поиск не запущен") || !strings.Contains(body, "Наблюдается в источнике") || strings.Contains(body, "<script>alert(1)</script>") {
		t.Fatalf("карточка или escaping: %d %s", dossier.Code, body)
	}
	foreign := f.request(http.MethodGet, "/requests/request-a/research/candidates/not-in-mission?tenant_id=tenant-a", "", true)
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("карточка вне миссии=%d", foreign.Code)
	}
}
func TestResearchMutationRequiresCSRF(t *testing.T) {
	f := newResearchHTTPFixture(t)
	form := url.Values{"tenant_id": {"tenant-a"}, "research_quantity": {"13"}, "research_unit": {"company"}, "research_country": {"RU"}}
	for _, path := range []string{"/requests/request-a/research/start", "/requests/request-a/research/control", "/requests/request-a/validation/approve"} {
		w := f.request(http.MethodPost, path, form.Encode(), true)
		if w.Code != http.StatusForbidden {
			t.Fatalf("POST %s withoutCSRF=%d", path, w.Code)
		}
	}
}

func (f *researchMemory) List(ctx context.Context, in researchmemory.ListRequest) (researchmemory.ListResult, error) {
	return researchmemory.ListResult{Total: 1, Entities: []researchmemory.Entity{{ID: "second", Name: "Вторая компания", Revision: 3}}}, nil
}

type researchIdentityOwner struct {
	researchmemory.IdentityPort
	calls    int
	received researchmemory.IdentityDecisionRequest
}

func (f *researchIdentityOwner) DecideIdentity(ctx context.Context, in researchmemory.IdentityDecisionRequest) (researchmemory.IdentityDecisionResult, error) {
	f.calls++
	f.received = in
	if in.ExpectedRevisions["entity-empty"] != 3 || in.ExpectedRevisions["second"] != 3 {
		return researchmemory.IdentityDecisionResult{}, researchmemory.ErrConflict
	}
	return researchmemory.IdentityDecisionResult{}, nil
}
func (f *researchIdentityOwner) ResolveIdentity(ctx context.Context, in researchmemory.GetRequest) (researchmemory.IdentityGroup, error) {
	return researchmemory.IdentityGroup{CanonicalEntityID: in.EntityID, MemberEntityIDs: []string{in.EntityID}, MemberRevisions: map[string]int64{in.EntityID: 3}}, nil
}
func (f *researchIdentityOwner) ListIdentityDecisions(context.Context, researchmemory.IdentityHistoryRequest) ([]researchmemory.IdentityDecision, error) {
	return nil, nil
}

func TestResearchOnlyRejectsLegacyBeforeMutation(t *testing.T) {
	f := newResearchHTTPFixture(t, func(c *researchbridge.Config) { c.OnlyResearch = true })
	// ValidateRequest/WorkflowStarter отсутствуют: обращение к ним было бы panic.
	form := url.Values{"tenant_id": {"tenant-a"}, "csrf": {f.csrf}}
	for _, path := range []string{"/requests/request-a/validation/approve", "/requests/request-a/workflow", "/requests/request-a/validation/complete"} {
		response := f.request(http.MethodPost, path, form.Encode(), true)
		if response.Code != http.StatusConflict {
			t.Fatalf("legacy %s=%d", path, response.Code)
		}
	}
}
func TestResearchIdentityReviewUsesSessionScopeAndOwnerCAS(t *testing.T) {
	owner := &researchIdentityOwner{}
	f := newResearchHTTPFixture(t, func(c *researchbridge.Config) { c.Identity = owner })
	path := "/requests/request-a/research/candidates/entity-empty/identity"
	page := f.request(http.MethodGet, path+"?tenant_id=tenant-a&other=second", "", true)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `name="revision.second" value="3"`) {
		t.Fatalf("identity form=%d %s", page.Code, page.Body.String())
	}
	form := url.Values{"tenant_id": {"tenant-a"}, "csrf": {f.csrf}, "actor": {"spoofed-admin"}, "decision_id": {"human-decision"}, "action": {"link"}, "right_entity_id": {"second"}, "revision.entity-empty": {"2"}, "revision.second": {"3"}, "evidence": {"fact-left", "fact-right"}, "reason": {"Реквизиты в обоих источниках совпадают"}}
	stale := f.request(http.MethodPost, path, form.Encode(), true)
	if stale.Code != http.StatusConflict || owner.received.ExpectedRevisions["entity-empty"] != 2 {
		t.Fatalf("stale CAS overwritten: %d %+v", stale.Code, owner.received)
	}
	form.Set("revision.entity-empty", "3")
	accepted := f.request(http.MethodPost, path, form.Encode(), true)
	if accepted.Code != http.StatusSeeOther || owner.received.Actor != "operator" || owner.received.Scope.TenantID != "tenant-a" || owner.received.Scope.Purpose != "editorial" {
		t.Fatalf("identity actor/scope=%d %+v", accepted.Code, owner.received)
	}
	calls := owner.calls
	form.Set("tenant_id", "tenant-b")
	denied := f.request(http.MethodPost, path, form.Encode(), true)
	if denied.Code != http.StatusForbidden || owner.calls != calls {
		t.Fatal("чужой tenant получил identitycommand")
	}
}

func TestResearchDiagnosticsRetainSourceAndExplainPartialAnalysis(t *testing.T) {
	f := newResearchHTTPFixture(t)
	f.missions.snapshot.Warnings = []researchmission.Warning{{Code: "subject-limit-reached", ActionID: "action-a", SourceID: "source-a", Country: "RU", Language: "ru"}, {Code: "new-runtime-code", ActionID: "action-b"}}
	f.missions.snapshot.Actions = []researchmission.ActionStatus{{ID: "action-a", Diagnostics: []string{"subject-limit-reached"}, Reason: "receipt-geography-mismatch"}}
	response := f.request(http.MethodGet, "/requests/request-a/research/progress?tenant_id=tenant-a", "", true)
	body := response.Body.String()
	for _, text := range []string{"data-research-warnings", "Часть списка ещё не разобрана", "source-a", "action-a", "new-runtime-code", "География ответа не совпала"} {
		if response.Code != http.StatusOK || !strings.Contains(body, text) {
			t.Fatalf("предупреждение не видно: %q status=%d", text, response.Code)
		}
	}
}

type researchQueueRepository struct {
	ports.RequestRepository
	request *domain.Request
}

func (r researchQueueRepository) ListByTenant(context.Context, domain.TenantID) ([]*domain.Request, error) {
	return []*domain.Request{r.request}, nil
}
func TestResearchRequestQueueShowsPersistedProgressWithoutLegacyWorkflow(t *testing.T) {
	f := newResearchHTTPFixture(t, func(c *researchbridge.Config) { c.OnlyResearch = true })
	request, err := domain.NewRequest(domain.NewRequestParams{ID: "request-a", TenantID: "tenant-a", OfferID: "offer-a", Content: "Автоматизация", Target: "Производители", Actor: "human", At: time.Now(), EventID: "create"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = request.StartValidation(domain.TransitionInput{Actor: "operator", At: time.Now(), EventID: "validate"}); err != nil {
		t.Fatal(err)
	}
	selection := domain.ResearchSelection{Quantity: 13, Unit: "company", OfferRevision: 1, Markets: []domain.ResearchMarket{{Country: "RU", Languages: []string{"ru"}}}}
	if _, err = request.ValidateForExecution(domain.TransitionInput{Actor: "operator", At: time.Now(), EventID: "approve"}, domain.ExecutionModeResearch, &selection); err != nil {
		t.Fatal(err)
	}
	f.server.deps.ValidationQueue = query.NewValidationQueueHandler(researchQueueRepository{request: request})
	f.missions.snapshot.Phase = "discovery"
	response := f.request(http.MethodGet, "/validation?tenant_id=tenant-a", "", true)
	body := response.Body.String()
	for _, want := range []string{"Поиск новых компаний", "Выполняется", "1 / 13", "Сохранённые кандидаты: 2", "/requests/request-a/research?tenant_id=tenant-a"} {
		if response.Code != http.StatusOK || !strings.Contains(body, want) {
			t.Fatalf("список не показал %q: %d", want, response.Code)
		}
	}
	execution := f.request(http.MethodGet, "/approvals?tenant_id=tenant-a", "", true)
	if execution.Code != http.StatusOK || !strings.Contains(execution.Body.String(), "1 / 13") || strings.Contains(execution.Body.String(), "Temporal") || !strings.Contains(execution.Body.String(), "Исполнение исследований") {
		t.Fatalf("исследование потерялось в исполнении: %d", execution.Code)
	}
	if f.memory.reads != 0 {
		t.Fatal("список заявок прочитал все dossiers")
	}
	if strings.Contains(body, "/requests/request-a/approval?") {
		t.Fatal("исследование открывается через legacy workflow")
	}
}
