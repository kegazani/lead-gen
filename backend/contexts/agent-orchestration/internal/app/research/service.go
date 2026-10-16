// Package research исполняет сохраняемую frontier одного человеческого поручения.
package research

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
	rm "lead-gen/contracts/agent-orchestration/researchmission"
	"lead-gen/contracts/agent-orchestration/researchplan"
	ra "lead-gen/contracts/integration-platform/researchacquisition"
	memory "lead-gen/contracts/lead-management/researchmemory"
)

type AnalysisRequest struct {
	Scope          rm.Scope
	MissionID      string
	ActionID       string
	QueryAttemptID string
	Brief          researchplan.SearchBrief
	Receipt        ra.Receipt
}
type Assessment = rm.CriteriaAssessment

type Followup struct {
	URL            string `json:"url"`
	Reason         string `json:"reason"`
	SourceURL      string `json:"source_url"`
	SourceRevision string `json:"source_revision"`
	Representation string `json:"representation"`
	Start          int    `json:"start"`
	End            int    `json:"end"`
	Quote          string `json:"quote"`
}
type AnalysisResult struct {
	Diagnostics   []string                        `json:"diagnostics,omitempty"`
	ModelID       string                          `json:"model_id,omitempty"`
	MethodVersion string                          `json:"method_version,omitempty"`
	SchemaVersion string                          `json:"schema_version,omitempty"`
	Observations  []memory.SaveObservationRequest `json:"observations"`
	Assessments   []Assessment                    `json:"assessments"`
	Followups     []Followup                      `json:"followups,omitempty"`
}
type Analyzer interface {
	Analyze(context.Context, AnalysisRequest) (AnalysisResult, error)
}

type Config struct {
	Repository            ports.ResearchRepository
	Sources               map[string]ra.Port
	FetchSourceIDs        []string
	Ledger                ra.Ledger
	FirecrawlAccountID    string
	MaxFirecrawlCredits   int
	Memory                memory.Port
	Analyzer              Analyzer
	Now                   func() time.Time
	LeaseDuration         time.Duration
	TerminalDrainDuration time.Duration
}
type Service struct {
	config   Config
	workerMu sync.Mutex
	worker   *workerHealth
}

func New(config Config) (*Service, error) {
	if config.Repository == nil || len(config.Sources) == 0 || config.Memory == nil || config.Analyzer == nil || config.MaxFirecrawlCredits < 0 {
		return nil, rm.ErrInvalid
	}
	for id, source := range config.Sources {
		if id == "" || source == nil {
			return nil, rm.ErrInvalid
		}
		if source.Capabilities().Provider == "firecrawl" && (config.Ledger == nil || config.FirecrawlAccountID == "") {
			return nil, fmt.Errorf("%w: требуется владелец Firecrawl grant", rm.ErrInvalid)
		}
	}
	for _, id := range config.FetchSourceIDs {
		source := config.Sources[id]
		if source == nil || !source.Capabilities().Fetch {
			return nil, rm.ErrInvalid
		}
	}
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	if config.TerminalDrainDuration <= 0 {
		config.TerminalDrainDuration = 10 * time.Minute
	}
	if config.LeaseDuration <= 0 {
		config.LeaseDuration = 6 * time.Minute
	}
	return &Service{config: config}, nil
}
func (s *Service) now() time.Time { return s.config.Now().UTC() }
func (s *Service) Start(ctx context.Context, req rm.StartRequest) (rm.Snapshot, error) {
	if req.FirecrawlMaxCredits < 0 || (s.config.MaxFirecrawlCredits > 0 && req.FirecrawlMaxCredits > s.config.MaxFirecrawlCredits) {
		return rm.Snapshot{}, rm.ErrInvalid
	}
	if req.FirecrawlMaxCredits == 0 {
		req.FirecrawlMaxCredits = s.config.MaxFirecrawlCredits
	}
	// Ревизия критериев/запуск не создаёт новую кассу того же поручения.
	grantID := domain.ResearchID(req.Scope.TenantID, req.Scope.PurposeID, req.IntentID, "firecrawl-grant")
	needsFirecrawl := false
	for _, cell := range req.Plan.Cells {
		source := s.config.Sources[cell.SourceID]
		if source == nil || !source.Capabilities().Search || source.Capabilities().Revision != cell.SourceRevision {
			return rm.Snapshot{}, fmt.Errorf("%w: источник %s", rm.ErrUnavailable, cell.SourceID)
		}
		if source.Capabilities().Provider == "firecrawl" {
			needsFirecrawl = true
		}
	}
	for _, sourceID := range s.config.FetchSourceIDs {
		if s.config.Sources[sourceID].Capabilities().Provider == "firecrawl" {
			needsFirecrawl = true
		}
	}
	if needsFirecrawl {
		prior, err := s.config.Repository.FindByIntent(ctx, req.Scope, req.IntentID)
		if err != nil {
			return rm.Snapshot{}, err
		}
		// Старые сохранённые миссии могли включать generation в grant ID.
		// Переиспользуем его без пересоздания ledger и без сброса cap.
		var first *domain.ResearchMission
		for _, old := range prior {
			if old.Request.FirecrawlGrantRef != "" && (first == nil || old.CreatedAt.Before(first.CreatedAt) || (old.CreatedAt.Equal(first.CreatedAt) && old.ID < first.ID)) {
				first = old
			}
		}
		if first != nil {
			grantID = first.Request.FirecrawlGrantRef
			if first.Request.FirecrawlMaxCredits != req.FirecrawlMaxCredits {
				return rm.Snapshot{}, rm.ErrConflict
			}
		}
		if req.FirecrawlGrantRef != "" && req.FirecrawlGrantRef != grantID {
			return rm.Snapshot{}, rm.ErrInvalid
		}
		req.FirecrawlGrantRef = grantID
	} else if req.FirecrawlGrantRef != "" {
		return rm.Snapshot{}, rm.ErrInvalid
	}
	existingID := domain.ResearchID(req.Scope.TenantID, req.Scope.PurposeID, req.IntentID, fmt.Sprint(req.CriteriaVersion), req.Generation)
	if existing, lookupErr := s.config.Repository.Get(ctx, req.Scope, existingID); lookupErr == nil {
		raw, _ := json.Marshal(req)
		hash := sha256.Sum256(raw)
		if existing.InputHash != hex.EncodeToString(hash[:]) {
			return rm.Snapshot{}, rm.ErrConflict
		}
		if needsFirecrawl && !existing.Terminal() {
			if err := s.ensureGrant(ctx, existing); err != nil {
				return existing.Snapshot(), err
			}
		}
		return existing.Snapshot(), nil
	} else if !errors.Is(lookupErr, rm.ErrNotFound) {
		return rm.Snapshot{}, lookupErr
	}
	m, err := domain.NewResearchMission(req, s.now())
	if err != nil {
		return rm.Snapshot{}, err
	}
	m, err = s.config.Repository.Create(ctx, m)
	if err != nil {
		return rm.Snapshot{}, err
	}
	if needsFirecrawl {
		if err = s.ensureGrant(ctx, m); err != nil {
			return m.Snapshot(), err
		}
	}
	return m.Snapshot(), nil
}
func (s *Service) ensureGrant(ctx context.Context, m *domain.ResearchMission) error {
	if m.Request.FirecrawlGrantRef == "" {
		return nil
	}
	return s.config.Ledger.EnsureGrant(ctx, ra.Grant{Scope: ra.Scope{TenantID: m.Request.Scope.TenantID, PurposeID: m.Request.Scope.PurposeID}, ID: m.Request.FirecrawlGrantRef, AccountID: s.config.FirecrawlAccountID, IntentID: m.Request.IntentID, IntentKind: "mission", MaxCredits: m.Request.FirecrawlMaxCredits})
}
func (s *Service) Get(ctx context.Context, scope rm.Scope, id string) (rm.Snapshot, error) {
	m, err := s.config.Repository.Get(ctx, scope, id)
	if err != nil {
		return rm.Snapshot{}, err
	}
	return m.Snapshot(), nil
}
func (s *Service) FindByIntent(ctx context.Context, scope rm.Scope, id string) ([]rm.Snapshot, error) {
	missions, err := s.config.Repository.FindByIntent(ctx, scope, id)
	if err != nil {
		return nil, err
	}
	out := make([]rm.Snapshot, 0, len(missions))
	for _, m := range missions {
		out = append(out, m.Snapshot())
	}
	return out, nil
}
func (s *Service) Control(ctx context.Context, req rm.ControlRequest) (rm.Snapshot, error) {
	m, err := s.config.Repository.Update(ctx, req.Scope, req.MissionID, func(m *domain.ResearchMission) error { return m.Control(req, s.now()) })
	if err != nil {
		return rm.Snapshot{}, err
	}
	return m.Snapshot(), nil
}

// RunReady выполняет максимум limit действий. Несколько процессов допускаются:
// каждый claim получает persisted epoch, а внешний вызов идёт после COMMIT.
func (s *Service) RunReady(ctx context.Context, limit int) error {
	return s.runReady(ctx, limit, false)
}

func (s *Service) runReady(ctx context.Context, limit int, observe bool) error {
	if observe {
		s.workerActivity(10 * time.Second)
	}
	scanCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	refs, err := s.config.Repository.Ready(scanCtx, limit)
	cancel()
	if observe {
		s.workerScanResult(err)
	}
	if err != nil {
		return err
	}
	var failures []error
	for _, ref := range refs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Все стадии, включая чтение memory, ограничены сверху. Внешний шаг
		// имеет собственный timeout <=300sec; добавлен запас на owner commit.
		if observe {
			s.workerActivity(330 * time.Second)
		}
		stepCtx, stop := context.WithTimeout(ctx, 330*time.Second)
		_, err = s.Step(stepCtx, ref.Scope, ref.ID)
		stop()
		if observe {
			s.workerActivity(30 * time.Second)
		}
		if err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (s *Service) Step(ctx context.Context, scope rm.Scope, id string) (rm.Snapshot, error) {
	if settled, err := s.settlePending(ctx, scope, id); settled || err != nil {
		s.refreshBudget(ctx, scope, id)
		snapshot, readErr := s.Get(ctx, scope, id)
		if readErr != nil {
			return rm.Snapshot{}, readErr
		}
		return snapshot, err
	}
	if err := s.refreshIdentity(ctx, scope, id); err != nil {
		return rm.Snapshot{}, err
	}
	m, action, stage, err := s.claim(ctx, scope, id)
	if err != nil {
		return rm.Snapshot{}, err
	}
	if action == nil {
		return m.Snapshot(), nil
	}
	switch stage {
	case "dispatch":
		err = s.dispatch(ctx, m, *action, false)
	case "recover":
		err = s.dispatch(ctx, m, *action, true)
	case "analyze":
		err = s.analyze(ctx, m, *action)
	case "ingest":
		err = s.ingest(ctx, m, *action)
	}
	// Снимок учёта читается отдельно от транзакции mission. Ошибка проекции не
	// превращает неизвестные credits в ноль и не отменяет сохранённый результат.
	s.refreshBudget(ctx, scope, id)
	snapshot, readErr := s.Get(ctx, scope, id)
	if readErr != nil {
		return rm.Snapshot{}, readErr
	}
	return snapshot, err
}
func (s *Service) refreshBudget(ctx context.Context, scope rm.Scope, id string) {
	if s.config.Ledger == nil {
		return
	}
	m, err := s.config.Repository.Get(ctx, scope, id)
	if err != nil || m.Request.FirecrawlGrantRef == "" {
		return
	}
	balance, err := s.config.Ledger.Snapshot(ctx, ra.Scope{TenantID: scope.TenantID, PurposeID: scope.PurposeID}, m.Request.FirecrawlGrantRef)
	if err != nil {
		return
	}
	_, _ = s.config.Repository.Update(ctx, scope, id, func(current *domain.ResearchMission) error {
		current.Firecrawl.Available = true
		current.Firecrawl.Actual = balance.Allocation.SpentCredits
		current.Firecrawl.Reserved = balance.Allocation.ReservedCredits
		current.Firecrawl.Unknown = balance.Allocation.UnknownCredits
		current.Touch(s.now())
		return nil
	})
}

func (s *Service) claim(ctx context.Context, scope rm.Scope, id string) (*domain.ResearchMission, *domain.ResearchAction, string, error) {
	var selected *domain.ResearchAction
	stage := ""
	now := s.now()
	m, err := s.config.Repository.Update(ctx, scope, id, func(m *domain.ResearchMission) error {
		if !m.Terminal() && !now.Before(m.Request.Limits.Deadline) {
			m.Finish("completed", "technical-deadline", now)
		}
		if !m.Terminal() && m.Counters().Accepted >= m.Request.Plan.Brief.Goal.Quantity {
			m.Finish("completed", "goal-reached", now)
		}
		for i := range m.Actions {
			a := &m.Actions[i]
			if a.LeaseUntil == nil || now.Before(*a.LeaseUntil) {
				continue
			}
			switch a.State {
			case "running":
				a.State = "unknown"
				a.Reason = "lease-expired-effect-unknown"
			case "analyzing":
				a.State = "received"
				a.Reason = "analysis-worker-restarted"
				if a.AnalysisAttempts >= m.Request.Limits.MaxAttempts {
					a.State = "failed"
					a.Reason = "analysis-attempts-exhausted"
				}
			case "ingesting":
				a.State = "analyzed"
				a.Reason = "ingest-worker-restarted"
			}
			a.LeaseUntil = nil
		}
		if m.Terminal() && m.CompletedAt != nil && !now.Before(m.CompletedAt.Add(s.config.TerminalDrainDuration)) {
			for i := range m.Actions {
				a := &m.Actions[i]
				switch a.State {
				case "received", "analyzed", "analyzing", "ingesting", "running":
					a.State = "reconciliation-required"
					a.Reason = "terminal-drain-expired"
					a.LeaseUntil = nil
					a.Epoch++
				}
			}
			m.Touch(now)
			return nil
		}
		choose := func(a *domain.ResearchAction, kind string) {
			a.Epoch++
			leaseDuration := s.config.LeaseDuration
			if minimum := time.Duration(m.Request.Limits.ActionTimeoutSeconds+15) * time.Second; leaseDuration < minimum {
				leaseDuration = minimum
			}
			until := now.Add(leaseDuration)
			a.LeaseUntil = &until
			a.NextAttemptAt = nil
			stage = kind
			switch kind {
			case "dispatch", "recover":
				a.State = "running"
			case "analyze":
				a.State = "analyzing"
				a.AnalysisAttempts++
			case "ingest":
				a.State = "ingesting"
			}
			copy := *a
			selected = &copy
			m.Phase = a.Queue + ":" + kind
			if m.State != "paused" {
				m.WaitingReason = ""
			}
			m.LastQueue = a.Queue
		}
		// Восстановление уже полученных данных важнее нового внешнего действия.
		for i := range m.Actions {
			a := &m.Actions[i]
			if a.NextAttemptAt != nil && now.Before(*a.NextAttemptAt) {
				continue
			}
			if a.State == "analyzed" {
				choose(a, "ingest")
				break
			}
			if a.State == "received" {
				choose(a, "analyze")
				break
			}
		}
		if selected == nil {
			for i := range m.Actions {
				a := &m.Actions[i]
				if a.State == "unknown" && a.Reason == "lease-expired-effect-unknown" {
					if a.NextAttemptAt != nil && now.Before(*a.NextAttemptAt) {
						continue
					}
					if _, ok := s.config.Sources[a.SourceID].(ReceiptReader); ok {
						choose(a, "recover")
						break
					}
				}
			}
		}
		if selected == nil && !m.Terminal() && m.State != "paused" {
			m.State = "running"
			// Отложенные hits остаются в receipts. Когда активные ветви исчерпаны,
			// открываем одну новую страницу, сохраняя место для её контактов.
			s.refillDeferredFrontier(m)
			if index := nextPendingActionIndex(m, now); index >= 0 {
				choose(&m.Actions[index], "dispatch")
			}

			if selected == nil {
				active := false
				unknown := false
				for _, a := range m.Actions {
					switch a.State {
					case "pending", "running", "analyzing", "ingesting", "received", "analyzed":
						active = true
					case "unknown":
						unknown = true
						if a.Reason == "lease-expired-effect-unknown" && a.NextAttemptAt != nil {
							active = true
						}
					}
				}
				if active {
					m.WaitingReason = "source-backoff-or-worker"
				} else {
					reason := "strategies-exhausted"
					if m.Firecrawl.Exhausted {
						reason = "firecrawl-exhausted-no-alternative"
					}
					if unknown {
						reason = "external-effect-unknown"
					}
					m.Finish("completed", reason, now)
				}
			}
		}
		m.HeartbeatAt = now
		m.Touch(now)
		return nil
	})
	return m, selected, stage, err
}

type ReceiptReader interface {
	Lookup(context.Context, ra.Scope, string, int) (ra.Receipt, error)
}

func (s *Service) mutateAction(ctx context.Context, m *domain.ResearchMission, a domain.ResearchAction, fn func(*domain.ResearchMission, *domain.ResearchAction) error) error {
	_, err := s.config.Repository.Update(ctx, m.Request.Scope, m.ID, func(current *domain.ResearchMission) error {
		target := current.Action(a.ID)
		if target == nil || target.Epoch != a.Epoch {
			return rm.ErrConflict
		}
		if err := fn(current, target); err != nil {
			return err
		}
		current.Touch(s.now())
		return nil
	})
	return err
}
func actionJSON(value any) json.RawMessage { raw, _ := json.Marshal(value); return raw }
