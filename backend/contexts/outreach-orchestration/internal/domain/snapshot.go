package domain

import (
	"fmt"
	"strings"
	"time"
)

// Сохранённое состояние агрегата и его сущностей.
//
// Адаптер репозитория не имеет доступа к закрытым полям агрегата, поэтому он
// читает и пишет CampaignSnapshot с экспортированными полями. Доменный объект
// из снимка собирается только через Rehydrate*, который проверяет инварианты и
// не эмитит события: восстановление не является новым фактом домена.

// ActionDraftSnapshot — сохранённое состояние черновика действия.
type ActionDraftSnapshot struct {
	ID       DraftID
	Campaign CampaignID
	Spec     DraftSpec
	State    DraftState
	Version  int
}

// OperatorDecisionSnapshot — сохранённое состояние решения оператора.
// BatchDigest пуст у одиночного решения и содержит digest состава у решения,
// принятого пакетно.
type OperatorDecisionSnapshot struct {
	ID           DecisionID
	Campaign     CampaignID
	Draft        DraftID
	DraftVersion int
	Actor        ActorID
	Outcome      DecisionOutcome
	Reason       string
	DecidedAt    time.Time
	BatchDigest  string
}

// CampaignSnapshot — полное сохранённое состояние aggregate root campaign.
// Порядок Drafts и Decisions сохраняет порядок постановки черновиков и
// принятия решений.
type CampaignSnapshot struct {
	ID        CampaignID
	TenantID  TenantID
	Name      string
	Window    CampaignWindow
	State     CampaignState
	Drafts    []ActionDraftSnapshot
	Decisions []OperatorDecisionSnapshot

	// Sequences и Executions — сценарии и участия последовательностей. Их
	// состояние хранится примитивным снимком (snapshot_sequence.go).
	Sequences  []SequenceDefinitionSnapshot `json:"sequences,omitempty"`
	Executions []SequenceExecutionSnapshot  `json:"executions,omitempty"`
}

// Snapshot возвращает состояние черновика для сохранения.
func (d *ActionDraft) Snapshot() ActionDraftSnapshot {
	return ActionDraftSnapshot{
		ID:       d.id,
		Campaign: d.campaign,
		Spec:     d.spec,
		State:    d.state,
		Version:  d.version,
	}
}

// Snapshot возвращает состояние решения для сохранения.
func (d OperatorDecision) Snapshot() OperatorDecisionSnapshot {
	return OperatorDecisionSnapshot{
		ID:           d.id,
		Campaign:     d.campaign,
		Draft:        d.draft,
		DraftVersion: d.draftVersion,
		Actor:        d.actor,
		Outcome:      d.outcome,
		Reason:       d.reason,
		DecidedAt:    d.decidedAt,
		BatchDigest:  d.batchDigest,
	}
}

// Snapshot возвращает состояние агрегата для сохранения.
func (c *Campaign) Snapshot() CampaignSnapshot {
	drafts := make([]ActionDraftSnapshot, 0, len(c.order))
	for _, id := range c.order {
		drafts = append(drafts, c.drafts[id].Snapshot())
	}
	decisions := make([]OperatorDecisionSnapshot, 0, len(c.decisions))
	for _, d := range c.decisions {
		decisions = append(decisions, d.Snapshot())
	}
	sequences := make([]SequenceDefinitionSnapshot, 0, len(c.definitionOrder))
	for _, id := range c.definitionOrder {
		sequences = append(sequences, c.definitions[id].Snapshot())
	}
	executions := make([]SequenceExecutionSnapshot, 0, len(c.executionOrder))
	for _, id := range c.executionOrder {
		executions = append(executions, c.executions[id].Snapshot())
	}
	return CampaignSnapshot{
		ID:         c.id,
		TenantID:   c.tenantID,
		Name:       c.name,
		Window:     c.window,
		State:      c.state,
		Drafts:     drafts,
		Decisions:  decisions,
		Sequences:  sequences,
		Executions: executions,
	}
}

// RehydrateActionDraft восстанавливает черновик из сохранённого состояния.
// Проверяются обязательные части состава, известность состояния и версия.
func RehydrateActionDraft(s ActionDraftSnapshot) (*ActionDraft, error) {
	if err := s.ID.validate(); err != nil {
		return nil, err
	}
	if err := s.Campaign.validate(); err != nil {
		return nil, err
	}
	if err := s.Spec.validate(); err != nil {
		return nil, err
	}
	if !s.State.isValid() {
		return nil, fmt.Errorf("%w: %q у черновика %s", ErrInvalidDraftState, s.State, s.ID)
	}
	if s.Version <= 0 {
		return nil, fmt.Errorf("%w: черновик %s версии %d", ErrDraftVersionMismatch, s.ID, s.Version)
	}
	return &ActionDraft{
		id:       s.ID,
		campaign: s.Campaign,
		spec:     s.Spec,
		state:    s.State,
		version:  s.Version,
	}, nil
}

// RehydrateOperatorDecision восстанавливает решение из сохранённого состояния.
// Требования к обязательным частям те же, что у newOperatorDecision; признак
// пакетного принятия восстанавливается из снимка.
func RehydrateOperatorDecision(s OperatorDecisionSnapshot) (OperatorDecision, error) {
	return newOperatorDecision(
		s.ID,
		s.Campaign,
		s.Draft,
		s.DraftVersion,
		s.Actor,
		s.Outcome,
		s.Reason,
		s.DecidedAt,
		withBatchProvenance(s.BatchDigest),
	)
}

// RehydrateCampaign восстанавливает aggregate root из сохранённого состояния.
//
// Восстановление проверяет инварианты: состав и границы кампании, принадлежность
// черновиков и решений этой кампании, отсутствие дублей черновиков и решений по
// одной версии, а также согласованность состояния черновика с его журналом
// решений (I1/I2). События не эмитятся: восстановление — не новый факт домена.
func RehydrateCampaign(s CampaignSnapshot) (*Campaign, error) {
	if err := s.ID.validate(); err != nil {
		return nil, err
	}
	if err := s.TenantID.validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(s.Name) == "" {
		return nil, fmt.Errorf("%w: кампания %s", ErrEmptyCampaignName, s.ID)
	}
	if err := s.Window.validate(); err != nil {
		return nil, err
	}
	if !s.State.isValid() {
		return nil, fmt.Errorf("%w: %q у кампании %s", ErrInvalidCampaignState, s.State, s.ID)
	}

	c := &Campaign{
		id:          s.ID,
		tenantID:    s.TenantID,
		name:        strings.TrimSpace(s.Name),
		window:      s.Window,
		state:       s.State,
		drafts:      make(map[DraftID]*ActionDraft),
		order:       make([]DraftID, 0, len(s.Drafts)),
		definitions: make(map[SequenceID]*SequenceDefinition),
		executions:  make(map[ExecutionID]*SequenceExecution),
	}

	for _, ds := range s.Drafts {
		draft, err := RehydrateActionDraft(ds)
		if err != nil {
			return nil, fmt.Errorf("%w: черновик %s: %v", ErrInvalidSnapshot, ds.ID, err)
		}
		if draft.campaign != c.id {
			return nil, fmt.Errorf(
				"%w: черновик %s принадлежит кампании %s, а не %s",
				ErrInvalidSnapshot, draft.id, draft.campaign, c.id,
			)
		}
		if _, exists := c.drafts[draft.id]; exists {
			return nil, fmt.Errorf("%w: черновик %s", ErrDuplicateDraft, draft.id)
		}
		c.drafts[draft.id] = draft
		c.order = append(c.order, draft.id)
	}

	for _, ds := range s.Decisions {
		decision, err := RehydrateOperatorDecision(ds)
		if err != nil {
			return nil, fmt.Errorf("%w: решение %s: %v", ErrInvalidSnapshot, ds.ID, err)
		}
		if decision.campaign != c.id {
			return nil, fmt.Errorf(
				"%w: решение %s относится к кампании %s, а не %s",
				ErrInvalidSnapshot, decision.id, decision.campaign, c.id,
			)
		}
		draft, ok := c.drafts[decision.draft]
		if !ok {
			return nil, fmt.Errorf(
				"%w: решение %s ссылается на отсутствующий черновик %s",
				ErrInvalidSnapshot, decision.id, decision.draft,
			)
		}
		if decision.draftVersion > draft.version {
			return nil, fmt.Errorf(
				"%w: решение %s ссылается на версию %d черновика %s, текущая %d",
				ErrInvalidSnapshot, decision.id, decision.draftVersion, decision.draft, draft.version,
			)
		}
		if _, exists := c.decisionFor(decision.draft, decision.draftVersion); exists {
			return nil, fmt.Errorf(
				"%w: черновик %s версии %d",
				ErrDecisionConflict, decision.draft, decision.draftVersion,
			)
		}
		c.decisions = append(c.decisions, decision)
	}

	if err := c.validateRehydratedState(); err != nil {
		return nil, err
	}

	for _, ds := range s.Sequences {
		definition, err := RehydrateSequenceDefinition(c.id, ds)
		if err != nil {
			return nil, fmt.Errorf("%w: сценарий %s: %v", ErrInvalidSnapshot, ds.ID, err)
		}
		if definition.tenant != c.tenantID {
			return nil, fmt.Errorf(
				"%w: сценарий %s принадлежит тенанту %s, а кампания — %s",
				ErrInvalidSnapshot, definition.id, definition.tenant, c.tenantID,
			)
		}
		if _, exists := c.definitions[definition.id]; exists {
			return nil, fmt.Errorf("%w: сценарий %s", ErrDuplicateSequence, definition.id)
		}
		c.definitions[definition.id] = definition
		c.definitionOrder = append(c.definitionOrder, definition.id)
	}

	for _, es := range s.Executions {
		execution, err := RehydrateSequenceExecution(c.id, es)
		if err != nil {
			return nil, fmt.Errorf("%w: участие %s: %v", ErrInvalidSnapshot, es.ID, err)
		}
		if execution.tenant != c.tenantID {
			return nil, fmt.Errorf(
				"%w: участие %s принадлежит тенанту %s, а кампания — %s",
				ErrInvalidSnapshot, execution.id, execution.tenant, c.tenantID,
			)
		}
		if _, ok := c.definitions[execution.sequence]; !ok {
			return nil, fmt.Errorf(
				"%w: участие %s ссылается на отсутствующий сценарий %s",
				ErrInvalidSnapshot, execution.id, execution.sequence,
			)
		}
		if _, exists := c.executions[execution.id]; exists {
			return nil, fmt.Errorf("%w: участие %s", ErrDuplicateExecution, execution.id)
		}
		c.executions[execution.id] = execution
		c.executionOrder = append(c.executionOrder, execution.id)
	}
	return c, nil
}

// validateRehydratedState проверяет, что состояние агрегата и черновиков
// согласовано с журналом решений.
func (c *Campaign) validateRehydratedState() error {
	switch c.state {
	case CampaignStateDraft:
		if len(c.order) != 0 {
			return fmt.Errorf(
				"%w: кампания %s в состоянии %q не может содержать черновики",
				ErrInvalidSnapshot, c.id, c.state,
			)
		}
	case CampaignStateActive:
		if len(c.order) == 0 {
			return fmt.Errorf(
				"%w: кампания %s в состоянии %q не содержит черновиков",
				ErrInvalidSnapshot, c.id, c.state,
			)
		}
	}

	for _, id := range c.order {
		draft := c.drafts[id]
		_, hasApproval := c.approvalFor(id, draft.version)
		hasRejection := c.rejectionFor(id, draft.version)
		switch draft.state {
		case DraftStateApproved, DraftStateExecuted, DraftStateFailed:
			if !hasApproval {
				return fmt.Errorf(
					"%w: черновик %s версии %d в состоянии %q без положительного решения",
					ErrInvalidSnapshot, id, draft.version, draft.state,
				)
			}
		case DraftStateRejected:
			if !hasRejection {
				return fmt.Errorf(
					"%w: черновик %s версии %d в состоянии %q без решения об отклонении",
					ErrInvalidSnapshot, id, draft.version, draft.state,
				)
			}
		default:
			if hasApproval || hasRejection {
				return fmt.Errorf(
					"%w: черновик %s версии %d в состоянии %q уже имеет решение",
					ErrInvalidSnapshot, id, draft.version, draft.state,
				)
			}
		}
	}
	return nil
}
