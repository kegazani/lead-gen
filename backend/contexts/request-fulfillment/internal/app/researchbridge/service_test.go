package researchbridge

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/request-fulfillment/internal/app/command"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/query"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/contracts/agent-orchestration/researchmission"
	"lead-gen/contracts/agent-orchestration/researchplan"
	"lead-gen/contracts/lead-management/researchmemory"
)

type requestFake struct {
	view             query.RequestView
	reads, approvals int
}

func (f *requestFake) Handle(ctx context.Context, q query.GetRequest) (query.RequestView, error) {
	f.reads++
	if string(q.TenantID) != f.view.TenantID || string(q.RequestID) != f.view.RequestID {
		return query.RequestView{}, ports.ErrRequestNotFound
	}
	return f.view, nil
}

type approveFake struct{ requests *requestFake }

func (f approveFake) Handle(ctx context.Context, c command.ValidateRequest) (command.TransitionResult, error) {
	f.requests.approvals++
	f.requests.view.Status = domain.StatusValidated
	f.requests.view.ExecutionMode = c.ExecutionMode
	f.requests.view.ResearchSelection = c.ResearchSelection
	f.requests.view.ValidationDecision = &query.ValidationDecisionView{Outcome: "approved", Actor: string(c.Actor)}
	return command.TransitionResult{}, nil
}

type accessFake struct {
	allowed       bool
	err           error
	actor, tenant string
}

func (f *accessFake) CanAct(ctx context.Context, a, t string) (bool, error) {
	f.actor = a
	f.tenant = t
	return f.allowed, f.err
}

type plannerFake struct {
	calls int
	brief researchplan.SearchBrief
	err   error
}

func (f *plannerFake) Plan(ctx context.Context, b researchplan.SearchBrief) (researchplan.Plan, error) {
	f.calls++
	f.brief = b
	return researchplan.Plan{Brief: b}, f.err
}

type missionFake struct {
	starts   int
	existing []researchmission.Snapshot
	start    researchmission.StartRequest
}

func (f *missionFake) Start(ctx context.Context, in researchmission.StartRequest) (researchmission.Snapshot, error) {
	f.starts++
	f.start = in
	return researchmission.Snapshot{Scope: in.Scope, MissionID: "mission", Goal: in.Plan.Brief.Goal}, nil
}
func (f *missionFake) Get(context.Context, researchmission.Scope, string) (researchmission.Snapshot, error) {
	return researchmission.Snapshot{}, nil
}
func (f *missionFake) FindByIntent(context.Context, researchmission.Scope, string) ([]researchmission.Snapshot, error) {
	return f.existing, nil
}
func (f *missionFake) Control(context.Context, researchmission.ControlRequest) (researchmission.Snapshot, error) {
	return researchmission.Snapshot{}, nil
}

type memoryFake struct {
	researchmemory.Port
	reads int
	scope researchmemory.Scope
}

func (f *memoryFake) Get(ctx context.Context, in researchmemory.GetRequest) (researchmemory.Dossier, error) {
	f.reads++
	f.scope = in.Scope
	return researchmemory.Dossier{Entity: researchmemory.Entity{ID: in.EntityID, Name: "Компания без email", Revision: 9}}, nil
}

type legacyFake struct {
	state ports.WorkflowStatus
	err   error
}

func (f legacyFake) WorkflowStatus(context.Context, string) (ports.WorkflowStatus, error) {
	return f.state, f.err
}

type fixture struct {
	service  *Service
	requests *requestFake
	access   *accessFake
	planner  *plannerFake
	missions *missionFake
	memory   *memoryFake
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	f := fixture{requests: &requestFake{view: query.RequestView{RequestID: "request", TenantID: "tenant", Status: domain.StatusValidating, Version: 2, ExecutionPurpose: "editorial", Offer: query.OfferView{Content: "Внедрение CRM", Target: "Генеральные подрядчики", Revision: 1}}}, access: &accessFake{allowed: true}, planner: &plannerFake{}, missions: &missionFake{}, memory: &memoryFake{}}
	var err error
	f.service, err = New(Config{Missions: f.missions, Memory: f.memory, Planner: f.planner, Access: f.access, Limits: researchmission.TechnicalLimits{MaxActions: 20, MaxAttempts: 2, MaxResultsPerSearch: 10, MaxDepth: 2, MaxBytesPerArtifact: 1024, ActionTimeoutSeconds: 20}, RunTimeout: time.Hour, Countries: []string{"RU", "GB"}}, f.requests, approveFake{f.requests}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func validStart() StartInput {
	return StartInput{Input: Input{TenantID: "tenant", RequestID: "request", Actor: "operator"}, Quantity: 7, Unit: researchplan.UnitCompany, Countries: []string{"RU"}, Approve: true}
}

func TestStartUsesOwnerRequestAndHumanQuantity(t *testing.T) {
	f := newFixture(t)
	_, err := f.service.Start(context.Background(), validStart())
	if err != nil {
		t.Fatal(err)
	}
	if f.requests.approvals != 1 || f.planner.calls != 1 || f.missions.starts != 1 {
		t.Fatalf("одобрения=%d планирование=%d start=%d", f.requests.approvals, f.planner.calls, f.missions.starts)
	}
	if f.planner.brief.Goal.Quantity != 7 || f.planner.brief.Purpose != "editorial" || f.planner.brief.Offer != "Внедрение CRM" || f.planner.brief.ICP != "Генеральные подрядчики" {
		t.Fatalf("искажён brief: %+v", f.planner.brief)
	}
	if f.missions.start.Scope.PurposeID != "editorial" || f.missions.start.Actor != "operator" {
		t.Fatalf("область: %+v", f.missions.start)
	}
}
func TestInvalidStartDoesNotApproveOrPlan(t *testing.T) {
	for _, mutate := range []func(*StartInput){func(in *StartInput) { in.Quantity = 0 }, func(in *StartInput) { in.Countries = []string{"US"} }, func(in *StartInput) { in.Unit = "page" }, func(in *StartInput) { in.Countries = nil }} {
		f := newFixture(t)
		in := validStart()
		mutate(&in)
		if _, err := f.service.Start(context.Background(), in); !errors.Is(err, ErrInvalid) {
			t.Fatalf("ошибка=%v", err)
		}
		if f.requests.approvals+f.planner.calls+f.missions.starts != 0 {
			t.Fatal("невалидный ввод произвёл эффект")
		}
	}
}
func TestAccessCheckedBeforeRequestOrMemoryRead(t *testing.T) {
	f := newFixture(t)
	f.access.allowed = false
	if _, err := f.service.Load(context.Background(), validStart().Input, "all"); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	if f.requests.reads != 0 || f.memory.reads != 0 {
		t.Fatal("данные прочитаны до проверки права")
	}
	f.access.err = errors.New("owner offline")
	if _, err := f.service.Start(context.Background(), validStart()); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if f.planner.calls != 0 || f.missions.starts != 0 {
		t.Fatal("недоступность права превратилась в разрешение")
	}
}
func TestApprovedRequestDoesNotLaunchAlongsideLegacy(t *testing.T) {
	f := newFixture(t)
	f.service.legacy = legacyFake{state: ports.WorkflowStatus{Execution: "running"}}
	if _, err := f.service.Start(context.Background(), validStart()); !errors.Is(err, ErrLegacyRunning) {
		t.Fatal(err)
	}
	if f.requests.approvals != 0 || f.missions.starts != 0 {
		t.Fatal("второй запуск применён")
	}
}
func TestPlannerFailureKeepsApprovalWithoutLegacyFallback(t *testing.T) {
	f := newFixture(t)
	f.planner.err = errors.New("proxy unavailable")
	if _, err := f.service.Start(context.Background(), validStart()); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if f.requests.view.Status != domain.StatusValidated || f.missions.starts != 0 {
		t.Fatal("сохранённое одобрение/отказ потеряны")
	}
	f.planner.err = nil
	in := validStart()
	in.Approve = false
	if _, err := f.service.Start(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if f.requests.approvals != 1 || f.missions.starts != 1 {
		t.Fatal("повтор одобряет заново или теряет запуск")
	}
}
func TestSavedCandidateWithoutContactAndMembershipFilters(t *testing.T) {
	f := newFixture(t)
	f.missions.existing = []researchmission.Snapshot{{MissionID: "mission", IntentID: "request", Scope: researchmission.Scope{TenantID: "tenant", PurposeID: "editorial"}, Revision: 5, Counters: researchmission.Counters{Saved: 2, Accepted: 1}, Members: []researchmission.Member{{EntityID: "entity-no-email", Reason: "contact-missing"}, {EntityID: "entity-accepted", Accepted: true, HasContact: true, HasEmail: true}}}}
	view, err := f.service.Load(context.Background(), validStart().Input, "all")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Candidates) != 2 || view.Candidates[0].Entity.Name != "Компания без email" || f.memory.scope.Purpose != "editorial" {
		t.Fatalf("кандидаты: %+v", view.Candidates)
	}
	filtered, err := f.service.Load(context.Background(), validStart().Input, "accepted")
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered.Candidates) != 1 || filtered.Snapshot.Revision != 5 {
		t.Fatal("фильтр расходится со снимком")
	}
	if _, _, err := f.service.Dossier(context.Background(), validStart().Input, "outside-mission"); !errors.Is(err, researchmemory.ErrNotFound) {
		t.Fatal("чужая карточка доступна", err)
	}
}
func TestWrongTenantDoesNotReadMission(t *testing.T) {
	f := newFixture(t)
	in := validStart().Input
	in.TenantID = "other"
	if _, err := f.service.Load(context.Background(), in, "all"); !errors.Is(err, ports.ErrRequestNotFound) {
		t.Fatal(err)
	}
	if f.memory.reads != 0 {
		t.Fatal("память другого тенанта прочитана")
	}
}
