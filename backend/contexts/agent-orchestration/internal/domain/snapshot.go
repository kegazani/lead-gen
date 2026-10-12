package domain

import (
	"fmt"
	"time"
)

// Снимки состояния агрегатов. Снимок — неизменяемое представление агрегата для
// хранилища: сохранённый агрегат изолирован от вызывающего, а загрузка заново
// проверяет инварианты (Rehydrate*). Неподтверждённые события в снимок не входят:
// их вычитывает app-слой и пишет в outbox (transactional outbox).

// BlockSnapshot — снимок описания блока в версии пайплайна.
type BlockSnapshot struct {
	Ref          BlockRef `json:"ref"`
	Name         string   `json:"name"`
	Contract     string   `json:"contract"`
	Irreversible bool     `json:"irreversible"`
	Mandatory    bool     `json:"mandatory"`
}

// StepConfigSnapshot — снимок конфигурации шага.
type StepConfigSnapshot struct {
	Params      map[string]string `json:"params,omitempty"`
	MaxAttempts int               `json:"max_attempts"`
}

// PipelineStepSnapshot — снимок шага версии пайплайна.
type PipelineStepSnapshot struct {
	ID     PipelineStepID     `json:"id"`
	Block  BlockSnapshot      `json:"block"`
	Order  int                `json:"order"`
	Config StepConfigSnapshot `json:"config"`
}

// PipelineSnapshot — снимок агрегата agent-pipeline.
type PipelineSnapshot struct {
	ID          PipelineID             `json:"id"`
	TenantID    TenantID               `json:"tenant_id"`
	Version     int                    `json:"version"`
	Steps       []PipelineStepSnapshot `json:"steps"`
	Published   bool                   `json:"published"`
	PublishedAt time.Time              `json:"published_at,omitempty"`
	Revision    int                    `json:"revision"`
}

// MissionSnapshot — снимок миссии агента.
type MissionSnapshot struct {
	ID        MissionID `json:"id"`
	CompanyID CompanyID `json:"company_id"`
	TenantID  TenantID  `json:"tenant_id"`
	Criteria  string    `json:"criteria,omitempty"`
}

// OutcomeSnapshot — снимок исхода шага.
type OutcomeSnapshot struct {
	Kind   BlockOutcomeKind `json:"kind"`
	Reason string           `json:"reason,omitempty"`
}

// StepRunSnapshot — снимок шага запуска.
type StepRunSnapshot struct {
	ID           StepRunID            `json:"id"`
	Step         PipelineStepSnapshot `json:"step"`
	Status       StepRunStatus        `json:"status"`
	Attempts     int                  `json:"attempts"`
	MaxAttempts  int                  `json:"max_attempts"`
	Outcome      OutcomeSnapshot      `json:"outcome"`
	ArtifactRef  string               `json:"artifact_ref,omitempty"`
	DraftRef     DraftRef             `json:"draft_ref,omitempty"`
	DraftVersion int                  `json:"draft_version,omitempty"`
	StartedAt    time.Time            `json:"started_at,omitempty"`
	FinishedAt   time.Time            `json:"finished_at,omitempty"`
}

// ApprovalSnapshot — снимок ожидания решения оператора.
type ApprovalSnapshot struct {
	DraftRef     DraftRef  `json:"draft_ref"`
	DraftVersion int       `json:"draft_version"`
	RequestedAt  time.Time `json:"requested_at"`
	Granted      bool      `json:"granted"`
	GrantedAt    time.Time `json:"granted_at,omitempty"`
	GrantedBy    ActorID   `json:"granted_by,omitempty"`
}

// RunSnapshot — снимок агрегата agent-run.
type RunSnapshot struct {
	ID              RunID             `json:"id"`
	Mission         MissionSnapshot   `json:"mission"`
	PipelineID      PipelineID        `json:"pipeline_id"`
	PipelineVersion int               `json:"pipeline_version"`
	Status          RunStatus         `json:"status"`
	Steps           []StepRunSnapshot `json:"steps"`
	Cursor          int               `json:"cursor"`
	Approval        *ApprovalSnapshot `json:"approval,omitempty"`
	Revision        int               `json:"revision"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

// Snapshot возвращает снимок версии пайплайна.
func (p *AgentPipeline) Snapshot() PipelineSnapshot {
	steps := make([]PipelineStepSnapshot, 0, len(p.steps))
	for _, step := range p.steps {
		steps = append(steps, stepSnapshot(step))
	}
	return PipelineSnapshot{
		ID:          p.id,
		TenantID:    p.tenantID,
		Version:     p.version,
		Steps:       steps,
		Published:   p.published,
		PublishedAt: p.publishedAt,
		Revision:    p.revision,
	}
}

// RehydratePipeline восстанавливает версию пайплайна из снимка, проверяя
// инварианты.
func RehydratePipeline(s PipelineSnapshot) (*AgentPipeline, error) {
	if err := s.ID.validate(); err != nil {
		return nil, err
	}
	if err := s.TenantID.validate(); err != nil {
		return nil, err
	}
	if s.Version < 1 {
		return nil, fmt.Errorf("пайплайн: версия должна быть положительной: %d", s.Version)
	}
	steps := make([]PipelineStep, 0, len(s.Steps))
	seen := make(map[PipelineStepID]struct{}, len(s.Steps))
	for index, snapshot := range s.Steps {
		step, err := rehydrateStep(snapshot)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[step.id]; exists {
			return nil, fmt.Errorf("%w: %s", ErrDuplicateStep, step.id)
		}
		seen[step.id] = struct{}{}
		if step.order != index {
			return nil, fmt.Errorf("пайплайн: порядок шага %s расходится с позицией", step.id)
		}
		steps = append(steps, step)
	}
	if s.Published && len(steps) == 0 {
		return nil, fmt.Errorf("%w: %s v%d", ErrEmptyPipeline, s.ID, s.Version)
	}
	return &AgentPipeline{
		id:          s.ID,
		tenantID:    s.TenantID,
		version:     s.Version,
		steps:       steps,
		published:   s.Published,
		publishedAt: s.PublishedAt,
		revision:    s.Revision,
	}, nil
}

// Snapshot возвращает снимок запуска.
func (r *AgentRun) Snapshot() RunSnapshot {
	steps := make([]StepRunSnapshot, 0, len(r.steps))
	for _, step := range r.steps {
		steps = append(steps, step.runSnapshot())
	}
	var approval *ApprovalSnapshot
	if r.approval != nil {
		approval = &ApprovalSnapshot{
			DraftRef:     r.approval.draftRef,
			DraftVersion: r.approval.draftVersion,
			RequestedAt:  r.approval.requestedAt,
			Granted:      r.approval.granted,
			GrantedAt:    r.approval.grantedAt,
			GrantedBy:    r.approval.grantedBy,
		}
	}
	return RunSnapshot{
		ID:              r.id,
		Mission:         MissionSnapshot{ID: r.mission.id, CompanyID: r.mission.companyID, TenantID: r.mission.tenantID, Criteria: r.mission.criteria},
		PipelineID:      r.pipelineID,
		PipelineVersion: r.pipelineVersion,
		Status:          r.status,
		Steps:           steps,
		Cursor:          r.cursor,
		Approval:        approval,
		Revision:        r.revision,
		CreatedAt:       r.createdAt,
		UpdatedAt:       r.updatedAt,
	}
}

// RehydrateRun восстанавливает запуск из снимка, проверяя инварианты статуса и
// шагов.
func RehydrateRun(s RunSnapshot) (*AgentRun, error) {
	if err := s.ID.validate(); err != nil {
		return nil, err
	}
	mission, err := NewAgentMission(s.Mission.ID, s.Mission.CompanyID, s.Mission.TenantID, s.Mission.Criteria)
	if err != nil {
		return nil, err
	}
	if err := s.PipelineID.validate(); err != nil {
		return nil, err
	}
	if err := s.Status.validate(); err != nil {
		return nil, err
	}
	if s.Cursor < 0 || s.Cursor > len(s.Steps) {
		return nil, fmt.Errorf("запуск: курсор %d вне диапазона шагов", s.Cursor)
	}
	steps := make([]*AgentStepRun, 0, len(s.Steps))
	for _, snapshot := range s.Steps {
		step, err := rehydrateStepRun(snapshot)
		if err != nil {
			return nil, err
		}
		steps = append(steps, step)
	}
	run := &AgentRun{
		id:              s.ID,
		mission:         mission,
		pipelineID:      s.PipelineID,
		pipelineVersion: s.PipelineVersion,
		status:          s.Status,
		steps:           steps,
		cursor:          s.Cursor,
		revision:        s.Revision,
		createdAt:       s.CreatedAt,
		updatedAt:       s.UpdatedAt,
	}
	if s.Approval != nil {
		run.approval = &ApprovalState{
			draftRef:     s.Approval.DraftRef,
			draftVersion: s.Approval.DraftVersion,
			requestedAt:  s.Approval.RequestedAt,
			granted:      s.Approval.Granted,
			grantedAt:    s.Approval.GrantedAt,
			grantedBy:    s.Approval.GrantedBy,
		}
	}
	return run, nil
}

// stepSnapshot снимает шаг пайплайна.
func stepSnapshot(step PipelineStep) PipelineStepSnapshot {
	return PipelineStepSnapshot{
		ID: step.id,
		Block: BlockSnapshot{
			Ref:          step.block.ref,
			Name:         step.block.name,
			Contract:     step.block.contract,
			Irreversible: step.block.irreversible,
			Mandatory:    step.block.mandatory,
		},
		Order: step.order,
		Config: StepConfigSnapshot{
			Params:      step.config.Params(),
			MaxAttempts: step.config.maxAttempts,
		},
	}
}

// runSnapshot снимает шаг запуска.
func (s *AgentStepRun) runSnapshot() StepRunSnapshot {
	return StepRunSnapshot{
		ID:           s.id,
		Step:         stepSnapshot(s.step),
		Status:       s.status,
		Attempts:     s.attempts,
		MaxAttempts:  s.maxAttempts,
		Outcome:      OutcomeSnapshot{Kind: s.outcome.kind, Reason: s.outcome.reason},
		ArtifactRef:  s.artifactRef,
		DraftRef:     s.draftRef,
		DraftVersion: s.draftVersion,
		StartedAt:    s.startedAt,
		FinishedAt:   s.finishedAt,
	}
}

// rehydrateStep восстанавливает шаг версии пайплайна из снимка.
func rehydrateStep(s PipelineStepSnapshot) (PipelineStep, error) {
	block, err := NewBlock(s.Block.Ref, s.Block.Name, s.Block.Contract, s.Block.Irreversible, s.Block.Mandatory)
	if err != nil {
		return PipelineStep{}, err
	}
	config, err := NewStepConfig(s.Config.Params, s.Config.MaxAttempts)
	if err != nil {
		return PipelineStep{}, err
	}
	if err := s.ID.validate(); err != nil {
		return PipelineStep{}, err
	}
	return PipelineStep{id: s.ID, block: block, order: s.Order, config: config}, nil
}

// rehydrateStepRun восстанавливает шаг запуска из снимка.
func rehydrateStepRun(s StepRunSnapshot) (*AgentStepRun, error) {
	step, err := rehydrateStep(s.Step)
	if err != nil {
		return nil, err
	}
	if !s.Status.isValid() {
		return nil, fmt.Errorf("%w: %q", ErrUnknownStepStatus, s.Status)
	}
	run := &AgentStepRun{
		id:           s.ID,
		step:         step,
		status:       s.Status,
		attempts:     s.Attempts,
		maxAttempts:  s.MaxAttempts,
		outcome:      BlockOutcome{kind: s.Outcome.Kind, reason: s.Outcome.Reason},
		artifactRef:  s.ArtifactRef,
		draftRef:     s.DraftRef,
		draftVersion: s.DraftVersion,
		startedAt:    s.StartedAt,
		finishedAt:   s.FinishedAt,
	}
	if err := run.validate(); err != nil {
		return nil, err
	}
	return run, nil
}
