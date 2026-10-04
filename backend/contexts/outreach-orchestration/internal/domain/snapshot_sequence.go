package domain

import (
	"fmt"
	"time"
)

// Сохранённое состояние сценариев и участий последовательностей.
//
// В отличие от черновиков и решений, состояние цепочек хранится снимком с
// примитивными полями (строки, числа, время): это вложенная и часто пустая
// структура, а идентификаторы значений восстанавливаются через конструкторы
// домена при загрузке. Снимок не содержит персональных данных — только ссылки.

// SequenceStepSnapshot — сохранённое состояние шага сценария.
type SequenceStepSnapshot struct {
	Action     string `json:"action"`
	Material   string `json:"material"`
	Segment    string `json:"segment"`
	DelayNanos int64  `json:"delay_nanos"`
	Transition string `json:"transition"`
}

// SequenceDefinitionSnapshot — сохранённое состояние сценария.
type SequenceDefinitionSnapshot struct {
	ID       string                 `json:"id"`
	TenantID string                 `json:"tenant_id"`
	Name     string                 `json:"name"`
	Steps    []SequenceStepSnapshot `json:"steps"`
}

// JournalEntrySnapshot — сохранённая запись журнала выполнения.
type JournalEntrySnapshot struct {
	Step   int       `json:"step"`
	Action string    `json:"action"`
	At     time.Time `json:"at"`
	Result string    `json:"result"`
	Actor  string    `json:"actor"`
	Reason string    `json:"reason"`
}

// SequenceExecutionSnapshot — сохранённое состояние участия лида.
type SequenceExecutionSnapshot struct {
	ID            string                 `json:"id"`
	Sequence      string                 `json:"sequence"`
	TenantID      string                 `json:"tenant_id"`
	Lead          string                 `json:"lead"`
	Recipient     string                 `json:"recipient"`
	State         string                 `json:"state"`
	Step          int                    `json:"step"`
	EligibleAt    time.Time              `json:"eligible_at"`
	DraftCreated  bool                   `json:"draft_created"`
	DraftID       string                 `json:"draft_id"`
	StallReported bool                   `json:"stall_reported"`
	Reactions     []string               `json:"reactions"`
	StopReason    string                 `json:"stop_reason"`
	EnrolledAt    time.Time              `json:"enrolled_at"`
	Journal       []JournalEntrySnapshot `json:"journal"`
}

// Snapshot возвращает состояние сценария для сохранения.
func (d *SequenceDefinition) Snapshot() SequenceDefinitionSnapshot {
	steps := make([]SequenceStepSnapshot, 0, len(d.steps))
	for _, step := range d.steps {
		steps = append(steps, SequenceStepSnapshot{
			Action:     step.action.String(),
			Material:   step.material.String(),
			Segment:    step.segment.String(),
			DelayNanos: int64(step.delay),
			Transition: step.transition.String(),
		})
	}
	return SequenceDefinitionSnapshot{
		ID:       d.id.String(),
		TenantID: d.tenant.String(),
		Name:     d.name,
		Steps:    steps,
	}
}

// Snapshot возвращает состояние участия для сохранения.
func (e *SequenceExecution) Snapshot() SequenceExecutionSnapshot {
	reactions := make([]string, 0, len(e.reactions))
	for reaction := range e.reactions {
		reactions = append(reactions, string(reaction))
	}
	journal := make([]JournalEntrySnapshot, 0, len(e.journal))
	for _, entry := range e.journal {
		journal = append(journal, JournalEntrySnapshot{
			Step:   entry.Step,
			Action: entry.Action.String(),
			At:     entry.At,
			Result: string(entry.Result),
			Actor:  entry.Actor.String(),
			Reason: entry.Reason,
		})
	}
	return SequenceExecutionSnapshot{
		ID:            e.id.String(),
		Sequence:      e.sequence.String(),
		TenantID:      e.tenant.String(),
		Lead:          e.lead.String(),
		Recipient:     e.recipient.String(),
		State:         string(e.state),
		Step:          e.step,
		EligibleAt:    e.eligibleAt,
		DraftCreated:  e.draftCreated,
		DraftID:       e.draftID.String(),
		StallReported: e.stallReported,
		Reactions:     reactions,
		StopReason:    e.stopReason,
		EnrolledAt:    e.enrolledAt,
		Journal:       journal,
	}
}

// RehydrateSequenceDefinition восстанавливает сценарий из сохранённого
// состояния. Кампания задаётся вызывающим: снимок не хранит её идентификатор,
// потому что лежит внутри снимка кампании.
func RehydrateSequenceDefinition(campaign CampaignID, s SequenceDefinitionSnapshot) (*SequenceDefinition, error) {
	steps := make([]SequenceStep, 0, len(s.Steps))
	for _, step := range s.Steps {
		rehydrated, err := rehydrateSequenceStep(step)
		if err != nil {
			return nil, fmt.Errorf("%w: шаг сценария %s: %v", ErrInvalidSnapshot, s.ID, err)
		}
		steps = append(steps, rehydrated)
	}
	return NewSequenceDefinition(SequenceDefinitionParams{
		ID:       SequenceID(s.ID),
		Campaign: campaign,
		Tenant:   TenantID(s.TenantID),
		Name:     s.Name,
		Steps:    steps,
	})
}

// rehydrateSequenceStep восстанавливает шаг сценария из снимка.
func rehydrateSequenceStep(s SequenceStepSnapshot) (SequenceStep, error) {
	action, err := ParseActionChannel(s.Action)
	if err != nil {
		return SequenceStep{}, err
	}
	transition, err := ParseTransitionCondition(s.Transition)
	if err != nil {
		return SequenceStep{}, err
	}
	return NewSequenceStep(SequenceStepParams{
		Action:     action,
		Material:   MaterialID(s.Material),
		Segment:    SegmentID(s.Segment),
		Delay:      time.Duration(s.DelayNanos),
		Transition: transition,
	})
}

// RehydrateSequenceExecution восстанавливает участие лида из сохранённого
// состояния. Проверяются идентификаторы, состояние и реакции.
func RehydrateSequenceExecution(campaign CampaignID, s SequenceExecutionSnapshot) (*SequenceExecution, error) {
	execution := &SequenceExecution{
		id:            ExecutionID(s.ID),
		campaign:      campaign,
		sequence:      SequenceID(s.Sequence),
		tenant:        TenantID(s.TenantID),
		lead:          LeadID(s.Lead),
		recipient:     RecipientID(s.Recipient),
		state:         ExecutionState(s.State),
		step:          s.Step,
		eligibleAt:    s.EligibleAt,
		draftCreated:  s.DraftCreated,
		draftID:       DraftID(s.DraftID),
		stallReported: s.StallReported,
		reactions:     make(map[Reaction]bool, len(s.Reactions)),
		stopReason:    s.StopReason,
		enrolledAt:    s.EnrolledAt,
		journal:       make([]JournalEntry, 0, len(s.Journal)),
	}
	if err := execution.id.validate(); err != nil {
		return nil, err
	}
	if err := execution.sequence.validate(); err != nil {
		return nil, err
	}
	if err := execution.tenant.validate(); err != nil {
		return nil, err
	}
	if err := execution.lead.validate(); err != nil {
		return nil, err
	}
	if err := execution.recipient.validate(); err != nil {
		return nil, err
	}
	if !execution.state.isValid() {
		return nil, fmt.Errorf("%w: участие %s в состоянии %q", ErrInvalidExecutionState, execution.id, execution.state)
	}
	if execution.step < 0 {
		return nil, fmt.Errorf("%w: участие %s, шаг %d", ErrInvalidSnapshot, execution.id, execution.step)
	}
	for _, reaction := range s.Reactions {
		parsed := Reaction(reaction)
		if !parsed.isValid() {
			return nil, fmt.Errorf("%w: %q у участия %s", ErrUnknownReaction, reaction, execution.id)
		}
		execution.reactions[parsed] = true
	}
	for _, entry := range s.Journal {
		action, err := rehydrateJournalAction(entry.Action)
		if err != nil {
			return nil, err
		}
		if entry.Result == "" {
			return nil, fmt.Errorf("%w: запись журнала участия %s без результата", ErrInvalidSnapshot, execution.id)
		}
		execution.journal = append(execution.journal, JournalEntry{
			Execution: execution.id,
			Campaign:  campaign,
			Lead:      execution.lead,
			Step:      entry.Step,
			Action:    action,
			At:        entry.At,
			Result:    StepOutcome(entry.Result),
			Actor:     ActorID(entry.Actor),
			Reason:    entry.Reason,
		})
	}
	return execution, nil
}

// rehydrateJournalAction восстанавливает канал записи журнала. Пустая строка
// допустима: запись может не относиться к конкретному действию.
func rehydrateJournalAction(name string) (ActionChannel, error) {
	if name == "" {
		return ActionChannel{}, nil
	}
	action, err := ParseActionChannel(name)
	if err != nil {
		return ActionChannel{}, fmt.Errorf("%w: %v", ErrInvalidSnapshot, err)
	}
	return action, nil
}
