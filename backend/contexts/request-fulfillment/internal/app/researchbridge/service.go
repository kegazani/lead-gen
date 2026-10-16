// Package researchbridge связывает одобренную человеком заявку с исследованием.
// Права и исходная заявка проверяются до чтения памяти и до внешнего планировщика.
package researchbridge

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"lead-gen/backend/contexts/request-fulfillment/internal/app/command"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/query"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/contracts/agent-orchestration/researchmission"
	"lead-gen/contracts/agent-orchestration/researchplan"
	"lead-gen/contracts/lead-management/researchmemory"
	"lead-gen/contracts/platform-governance/accessdecision"
)

var (
	ErrDenied           = errors.New("действие недоступно в выбранной компании")
	ErrApprovalRequired = errors.New("сначала необходимо одобрение заявки человеком")
	ErrLegacyRunning    = errors.New("у этой заявки есть прежнее исполнение; для нового исследования создайте новую заявку")
	ErrInvalid          = errors.New("некорректные параметры исследования")
	ErrUnavailable      = errors.New("исследование или проверка доступа сейчас недоступны")
)

type Config struct {
	OnlyResearch        bool
	Identity            researchmemory.IdentityPort
	Missions            researchmission.Port
	Memory              researchmemory.Port
	Planner             researchplan.Planner
	Access              accessdecision.Decider
	Limits              researchmission.TechnicalLimits
	RunTimeout          time.Duration
	EmailReadyAvailable bool
	Countries           []string
	Now                 func() time.Time
}

type RequestReader interface {
	Handle(context.Context, query.GetRequest) (query.RequestView, error)
}
type Approver interface {
	Handle(context.Context, command.ValidateRequest) (command.TransitionResult, error)
}
type Service struct {
	reconciler *Reconciler
	cfg        Config
	requests   RequestReader
	approve    Approver
	legacy     ports.WorkflowStatusReader
}
type Input struct{ TenantID, RequestID, Actor string }
type StartInput struct {
	Input
	Quantity  int
	Unit      string
	Countries []string
	Languages map[string][]string
	Approve   bool
}
type Candidate struct {
	Member      researchmission.Member
	Entity      researchmemory.Entity
	Unavailable bool
}
type View struct {
	Request    query.RequestView
	Snapshot   *researchmission.Snapshot
	Candidates []Candidate
}

func New(cfg Config, requests RequestReader, approve Approver, legacy ports.WorkflowStatusReader) (*Service, error) {
	if requests == nil || approve == nil || cfg.Missions == nil || cfg.Memory == nil || cfg.Planner == nil || cfg.Access == nil {
		return nil, ErrUnavailable
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.RunTimeout <= 0 {
		return nil, fmt.Errorf("%w: срок исследования должен быть положительным", ErrInvalid)
	}
	limits := cfg.Limits
	limits.Deadline = cfg.Now().Add(cfg.RunTimeout)
	if err := limits.Validate(); err != nil {
		return nil, fmt.Errorf("%w: технические пределы", ErrInvalid)
	}
	if len(cfg.Countries) == 0 {
		return nil, fmt.Errorf("%w: страны доступных источников не заданы", ErrInvalid)
	}
	seen := map[string]bool{}
	for _, country := range cfg.Countries {
		if _, ok := researchplan.LookupCountry(country); !ok || seen[country] {
			return nil, fmt.Errorf("%w: страна %s", ErrInvalid, country)
		}
		seen[country] = true
	}
	cfg.Countries = append([]string(nil), cfg.Countries...)
	return &Service{cfg: cfg, requests: requests, approve: approve, legacy: legacy}, nil
}

func (s *Service) SetReconciler(reconciler *Reconciler) { s.reconciler = reconciler }

func (s *Service) observed(ctx context.Context, snapshot researchmission.Snapshot, err error) (researchmission.Snapshot, error) {
	if err != nil {
		return snapshot, err
	}
	if s.reconciler != nil {
		err = s.reconciler.Observe(ctx, snapshot)
	}
	return snapshot, err
}

func (s *Service) OnlyResearch() bool { return s.cfg.OnlyResearch }

func (s *Service) EmailReadyAvailable() bool { return s.cfg.EmailReadyAvailable }

func (s *Service) Countries() []string { return append([]string(nil), s.cfg.Countries...) }

func (s *Service) request(ctx context.Context, in Input) (query.RequestView, error) {
	if strings.TrimSpace(in.Actor) == "" || strings.TrimSpace(in.TenantID) == "" || strings.TrimSpace(in.RequestID) == "" {
		return query.RequestView{}, ErrDenied
	}
	allowed, err := s.cfg.Access.CanAct(ctx, in.Actor, in.TenantID)
	if err != nil {
		return query.RequestView{}, fmt.Errorf("%w: проверка полномочий", ErrUnavailable)
	}
	if !allowed {
		return query.RequestView{}, ErrDenied
	}
	view, err := s.requests.Handle(ctx, query.GetRequest{RequestID: domain.RequestID(in.RequestID), TenantID: domain.TenantID(in.TenantID)})
	if err != nil {
		return query.RequestView{}, err
	}
	if strings.TrimSpace(view.ExecutionPurpose) == "" {
		return query.RequestView{}, fmt.Errorf("%w: у заявки отсутствует назначение обработки", ErrInvalid)
	}
	return view, nil
}
func scope(view query.RequestView) researchmission.Scope {
	return researchmission.Scope{TenantID: view.TenantID, PurposeID: view.ExecutionPurpose}
}
func memoryScope(view query.RequestView) researchmemory.Scope {
	return researchmemory.Scope{TenantID: view.TenantID, Purpose: view.ExecutionPurpose}
}

func (s *Service) ReadStatus(ctx context.Context, in Input) (View, error) {
	req, err := s.request(ctx, in)
	if err != nil {
		return View{}, err
	}
	out := View{Request: req}
	missions, err := s.cfg.Missions.FindByIntent(ctx, scope(req), req.RequestID)
	if err != nil {
		return View{}, err
	}
	if len(missions) == 0 {
		return out, nil
	}
	sort.Slice(missions, func(i, j int) bool { return missions[i].CreatedAt.After(missions[j].CreatedAt) })
	snapshot := missions[0]
	if snapshot.Scope != scope(req) || snapshot.IntentID != req.RequestID {
		return View{}, fmt.Errorf("%w: область снимка не соответствует заявке", ErrUnavailable)
	}
	out.Snapshot = &snapshot
	return out, nil
}

func (s *Service) Load(ctx context.Context, in Input, filter string) (View, error) {
	out, err := s.ReadStatus(ctx, in)
	if err != nil || out.Snapshot == nil {
		return out, err
	}
	for _, member := range out.Snapshot.Members {
		if !matchesFilter(member, filter) {
			continue
		}
		row := Candidate{Member: member}
		dossier, getErr := s.cfg.Memory.Get(ctx, researchmemory.GetRequest{Scope: memoryScope(out.Request), EntityID: member.EntityID})
		if getErr != nil {
			row.Unavailable = true
			row.Entity.ID = member.EntityID
		} else {
			row.Entity = dossier.Entity
		}
		out.Candidates = append(out.Candidates, row)
	}
	return out, nil
}
func matchesFilter(m researchmission.Member, filter string) bool {
	switch filter {
	case "accepted":
		return m.Accepted
	case "contact":
		return m.HasContact
	case "email":
		return m.EmailReady
	default:
		return true
	}
}

func (s *Service) Dossier(ctx context.Context, in Input, entityID string) (View, researchmemory.Dossier, error) {
	// Проверяем принадлежность именно снимку заявки, а не только tenant памяти.
	view, err := s.Load(ctx, in, "all")
	if err != nil {
		return View{}, researchmemory.Dossier{}, err
	}
	if view.Snapshot == nil {
		return View{}, researchmemory.Dossier{}, researchmemory.ErrNotFound
	}
	found := false
	for _, m := range view.Snapshot.Members {
		if m.EntityID == entityID {
			found = true
			break
		}
	}
	if !found {
		return View{}, researchmemory.Dossier{}, researchmemory.ErrNotFound
	}
	dossier, err := s.cfg.Memory.Get(ctx, researchmemory.GetRequest{Scope: memoryScope(view.Request), EntityID: entityID})
	return view, dossier, err
}

func (s *Service) Start(ctx context.Context, in StartInput) (researchmission.Snapshot, error) {
	req, err := s.request(ctx, in.Input)
	if err != nil {
		return researchmission.Snapshot{}, err
	}
	if in.Unit == researchplan.UnitEmailReady && !s.cfg.EmailReadyAvailable {
		return researchmission.Snapshot{}, fmt.Errorf("%w: проверка готовности email-адресата пока не подключена", ErrInvalid)
	}
	if in.Quantity < 1 {
		return researchmission.Snapshot{}, fmt.Errorf("%w: количество задаётся человеком", ErrInvalid)
	}
	markets := make([]researchplan.MarketSelection, 0, len(in.Countries))
	allowed := map[string]bool{}
	for _, c := range s.cfg.Countries {
		allowed[c] = true
	}
	for _, c := range in.Countries {
		if !allowed[c] {
			return researchmission.Snapshot{}, fmt.Errorf("%w: источник для выбранной страны не подключён", ErrInvalid)
		}
		markets = append(markets, researchplan.MarketSelection{Country: c, Languages: append([]string(nil), in.Languages[c]...), Origin: researchplan.OriginHuman})
	}
	if len(markets) == 0 {
		return researchmission.Snapshot{}, fmt.Errorf("%w: выберите страну", ErrInvalid)
	}
	// Весь пользовательский ввод проверяется до применения одобрения.
	brief, err := researchplan.NewBrief(researchplan.SearchBrief{ID: "request-" + req.RequestID, Revision: req.Offer.Revision, TenantID: req.TenantID, Purpose: req.ExecutionPurpose, RequestID: req.RequestID, IntentVersion: req.Offer.Revision, ActorID: in.Actor, Offer: req.Offer.Content, ICP: req.Offer.Target, Criteria: researchplan.CriteriaSnapshot{Version: req.Offer.Revision}, Markets: markets, Goal: researchplan.Goal{Quantity: in.Quantity, Unit: in.Unit}})
	if err != nil {
		return researchmission.Snapshot{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	selection := domain.ResearchSelection{Quantity: brief.Goal.Quantity, Unit: brief.Goal.Unit, OfferRevision: brief.Revision}
	for _, market := range brief.Markets {
		selection.Markets = append(selection.Markets, domain.ResearchMarket{Country: market.Country, Languages: append([]string(nil), market.Languages...)})
	}
	if req.ExecutionMode != "" && req.ExecutionMode != domain.ExecutionModeResearch {
		return researchmission.Snapshot{}, domain.ErrExecutionModeConflict
	}
	if req.ResearchSelection != nil && !req.ResearchSelection.Equal(selection) {
		return researchmission.Snapshot{}, domain.ErrResearchSelectionConflict
	}
	existing, err := s.cfg.Missions.FindByIntent(ctx, scope(req), req.RequestID)
	if err != nil {
		return researchmission.Snapshot{}, err
	}
	if len(existing) > 0 {
		latest := existing[0]
		for _, m := range existing[1:] {
			if m.CreatedAt.After(latest.CreatedAt) {
				latest = m
			}
		}
		if latest.CriteriaVersion != req.Offer.Revision || latest.Goal != brief.Goal || !sameMarkets(latest.Markets, brief.Markets) {
			return researchmission.Snapshot{}, researchmission.ErrConflict
		}
		return s.observed(ctx, latest, nil)
	}
	if req.Status != domain.StatusValidating && req.Status != domain.StatusValidated && req.Status != domain.StatusProcessing {
		return researchmission.Snapshot{}, ErrApprovalRequired
	}
	if s.legacy != nil {
		state, readErr := s.legacy.WorkflowStatus(ctx, req.RequestID)
		if readErr == nil {
			_ = state
			return researchmission.Snapshot{}, ErrLegacyRunning
		} else if !errors.Is(readErr, ports.ErrWorkflowNotFound) {
			return researchmission.Snapshot{}, fmt.Errorf("%w: состояние прежнего исполнения неизвестно", ErrUnavailable)
		}
	}
	if req.Status == domain.StatusValidating {
		if !in.Approve {
			return researchmission.Snapshot{}, ErrApprovalRequired
		}
		_, err = s.approve.Handle(ctx, command.ValidateRequest{ExecutionMode: domain.ExecutionModeResearch, ResearchSelection: &selection, LifecycleFields: command.LifecycleFields{RequestID: domain.RequestID(req.RequestID), TenantID: domain.TenantID(req.TenantID), Actor: domain.ActorID(in.Actor)}})
		if err != nil {
			return researchmission.Snapshot{}, err
		}
		req, err = s.request(ctx, in.Input)
		if err != nil {
			return researchmission.Snapshot{}, err
		}
	}
	if req.ExecutionMode != domain.ExecutionModeResearch || req.ResearchSelection == nil || !req.ResearchSelection.Equal(selection) {
		return researchmission.Snapshot{}, domain.ErrResearchSelectionConflict
	}
	if req.ValidationDecision == nil || req.ValidationDecision.Outcome != string(domain.ValidationApproved) {
		return researchmission.Snapshot{}, ErrApprovalRequired
	}
	plan, err := s.cfg.Planner.Plan(ctx, brief)
	if err != nil {
		return researchmission.Snapshot{}, fmt.Errorf("%w: планирование не завершено", ErrUnavailable)
	}
	limits := s.cfg.Limits
	limits.Deadline = s.cfg.Now().Add(s.cfg.RunTimeout)
	snapshot, startErr := s.cfg.Missions.Start(ctx, researchmission.StartRequest{Scope: scope(req), IntentID: req.RequestID, Actor: in.Actor, Generation: fmt.Sprintf("offer-%d", req.Offer.Revision), CriteriaVersion: req.Offer.Revision, Plan: plan, Limits: limits})
	return s.observed(ctx, snapshot, startErr)
}

func (s *Service) Control(ctx context.Context, in Input, revision int64, command string) (researchmission.Snapshot, error) {
	view, err := s.Load(ctx, in, "all")
	if err != nil {
		return researchmission.Snapshot{}, err
	}
	if view.Snapshot == nil {
		return researchmission.Snapshot{}, researchmission.ErrNotFound
	}
	snapshot, controlErr := s.cfg.Missions.Control(ctx, researchmission.ControlRequest{Scope: scope(view.Request), MissionID: view.Snapshot.MissionID, Actor: in.Actor, Command: command, ExpectedRevision: revision, Reason: "Решение оператора заявки"})
	return s.observed(ctx, snapshot, controlErr)
}

// Повтор формы не меняет страны и языки уже принятого поручения.
func sameMarkets(left, right []researchplan.MarketSelection) bool {
	if len(left) != len(right) {
		return false
	}
	values := func(items []researchplan.MarketSelection) map[string]string {
		out := map[string]string{}
		for _, m := range items {
			langs := append([]string(nil), m.Languages...)
			sort.Strings(langs)
			out[m.Country] = strings.Join(langs, ",")
		}
		return out
	}
	l, r := values(left), values(right)
	for country, langs := range l {
		if r[country] != langs {
			return false
		}
	}
	return true
}
