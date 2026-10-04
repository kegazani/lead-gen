package domain

import (
	"fmt"
	"time"
)

// DraftRef — ссылка на черновик шага, сформированный движком последовательностей.
type DraftRef struct {
	ExecutionID ExecutionID
	Step        int
	DraftID     DraftID
}

// AdvanceReport — результат прохода движка последовательностей по агрегату:
// какие шаги сформированы, какие цепочки продвинулись, где зафиксирован простой
// и достигнут ли предел объёма черновиков.
type AdvanceReport struct {
	// Drafted — шаги, черновики которых сформированы в этом проходе.
	Drafted []DraftRef

	// Advanced — участия, продвинувшиеся к следующему шагу.
	Advanced []ExecutionID

	// Completed — участия, прошедшие все шаги.
	Completed []ExecutionID

	// Stalled — участия с неутверждённым шагом (простой).
	Stalled []ExecutionID

	// Held — участия, остановленные условием перехода: шаг утверждён, но
	// условие не выполнено, шаг не пропускается.
	Held []ExecutionID

	// Halted — предел объёма черновиков достигнут, формирование приостановлено.
	Halted bool
}

// DefineSequence создаёт сценарий последовательности и добавляет его в кампанию.
// Сценарий без шагов отвергается, повторный идентификатор — тоже.
func (c *Campaign) DefineSequence(params SequenceDefinitionParams) (*SequenceDefinition, error) {
	if c.state == CampaignStateCompleted {
		return nil, fmt.Errorf("%w: кампания %s", ErrCampaignCompleted, c.id)
	}
	if _, exists := c.definitions[params.ID]; exists {
		return nil, fmt.Errorf("%w: сценарий %s", ErrDuplicateSequence, params.ID)
	}
	definition, err := NewSequenceDefinition(params)
	if err != nil {
		return nil, err
	}
	if definition.campaign != c.id || definition.tenant != c.tenantID {
		return nil, fmt.Errorf("%w: сценарий %s", ErrExecutionTenantMismatch, params.ID)
	}
	c.definitions[definition.id] = definition
	c.definitionOrder = append(c.definitionOrder, definition.id)
	return definition, nil
}

// EnrollmentParams — данные добавления лида в цепочку.
type EnrollmentParams struct {
	ExecutionID ExecutionID
	SequenceID  SequenceID
	TenantID    TenantID
	Lead        LeadID
	Recipient   RecipientID
	At          time.Time
}

// EnrollLead добавляет лида в цепочку: создаёт участие в состоянии «активно».
// Первый черновик формирует очередной проход движка (AdvanceSequences), поэтому
// задержка первого шага соблюдается и при добавлении.
func (c *Campaign) EnrollLead(params EnrollmentParams) (*SequenceExecution, error) {
	if c.state == CampaignStateCompleted {
		return nil, fmt.Errorf("%w: кампания %s", ErrCampaignCompleted, c.id)
	}
	if _, exists := c.executions[params.ExecutionID]; exists {
		return nil, fmt.Errorf("%w: участие %s", ErrDuplicateExecution, params.ExecutionID)
	}
	definition, ok := c.definitions[params.SequenceID]
	if !ok {
		return nil, fmt.Errorf("%w: сценарий %s", ErrSequenceNotFound, params.SequenceID)
	}
	if definition.tenant != c.tenantID {
		return nil, fmt.Errorf("%w: сценарий %s", ErrSequenceNotFound, params.SequenceID)
	}
	if params.TenantID != c.tenantID {
		return nil, fmt.Errorf("%w: кампания %s", ErrExecutionTenantMismatch, c.id)
	}
	first, ok := definition.Step(0)
	if !ok {
		return nil, fmt.Errorf("%w: сценарий %s", ErrSequenceStepsEmpty, params.SequenceID)
	}
	execution, err := newSequenceExecution(
		params.ExecutionID,
		definition,
		params.TenantID,
		params.Lead,
		params.Recipient,
		params.At.Add(first.delay),
		params.At,
	)
	if err != nil {
		return nil, err
	}
	c.executions[execution.id] = execution
	c.executionOrder = append(c.executionOrder, execution.id)
	c.record(OutreachOutcomeRecorded{
		CampaignID:  c.id,
		TenantID:    c.tenantID,
		ExecutionID: execution.id,
		SequenceID:  execution.sequence,
		Lead:        execution.lead,
		Step:        0,
		Result:      OutcomeEnrolled,
		At:          params.At.UTC(),
	})
	return execution, nil
}

// RemoveLead удаляет лида из цепочки вручную: последующие шаги не формируются, а
// причина фиксируется в журнале. Остановленное участие не возобновляется.
func (c *Campaign) RemoveLead(executionID ExecutionID, actor ActorID, reason string, at time.Time) error {
	execution, ok := c.executions[executionID]
	if !ok {
		return fmt.Errorf("%w: участие %s", ErrExecutionNotFound, executionID)
	}
	if execution.state.isTerminal() {
		return fmt.Errorf("%w: участие %s", ErrExecutionTerminal, executionID)
	}
	if err := actor.validate(); err != nil {
		return err
	}
	execution.state = ExecutionStopped
	execution.stopReason = reason
	execution.appendJournal(at, execution.step, ActionChannel{}, OutcomeRemoved, actor, reason)
	c.record(OutreachOutcomeRecorded{
		CampaignID:  c.id,
		TenantID:    c.tenantID,
		ExecutionID: execution.id,
		SequenceID:  execution.sequence,
		Lead:        execution.lead,
		Step:        execution.step,
		Result:      OutcomeRemoved,
		At:          at.UTC(),
	})
	return nil
}

// RecordReactionParams — данные записи реакции лида.
type RecordReactionParams struct {
	ExecutionID ExecutionID
	Reaction    Reaction

	// Confident равно false, если классификация ответа неуверенна: реакция
	// фиксируется в журнале, но ветка автоматически не выбирается.
	Confident bool

	At     time.Time
	Reason string
}

// RecordReaction фиксирует реакцию лида.
//
// Симметрия остановки и возобновления (design.md, Решение 3): приостановка по
// ответу и остановка по отписке, отказу и стоп-листу выполняются системой;
// возобновление — только решением оператора (ResumeExecution).
func (c *Campaign) RecordReaction(params RecordReactionParams) error {
	execution, ok := c.executions[params.ExecutionID]
	if !ok {
		return fmt.Errorf("%w: участие %s", ErrExecutionNotFound, params.ExecutionID)
	}
	if !params.Reaction.isValid() {
		return fmt.Errorf("%w: %q", ErrUnknownReaction, params.Reaction)
	}
	if execution.state.isTerminal() {
		return fmt.Errorf("%w: участие %s", ErrExecutionTerminal, params.ExecutionID)
	}
	if params.At.IsZero() {
		return fmt.Errorf("%w: участие %s", ErrDecisionTimeRequired, params.ExecutionID)
	}
	step, _ := c.stepAction(execution)

	if !params.Confident {
		// Неуверенная классификация: ветка не выбирается автоматически, участие
		// не меняет состояние. Это не отказ: решение принимает оператор.
		execution.appendJournal(params.At, execution.step, step, OutcomeUnclassified, "", params.Reason)
		return nil
	}

	switch {
	case params.Reaction.isStopping():
		execution.state = ExecutionStopped
		execution.stopReason = params.Reason
		execution.appendJournal(params.At, execution.step, step, OutcomeStopped, "", params.Reason)
	case params.Reaction.isReply():
		execution.state = ExecutionSuspended
		execution.reactions[params.Reaction] = true
		execution.appendJournal(params.At, execution.step, step, OutcomeSuspended, "", params.Reason)
	default:
		execution.reactions[params.Reaction] = true
		execution.appendJournal(params.At, execution.step, step, OutcomeRecorded, "", params.Reason)
	}

	c.record(OutreachOutcomeRecorded{
		CampaignID:  c.id,
		TenantID:    c.tenantID,
		ExecutionID: execution.id,
		SequenceID:  execution.sequence,
		Lead:        execution.lead,
		Step:        execution.step,
		Result:      reactionOutcome(params.Reaction),
		At:          params.At.UTC(),
	})
	return nil
}

// ResumeExecution возобновляет приостановленное участие по решению оператора.
// Автоматического возобновления нет: состояние «приостановлено» снимает только
// эта команда.
func (c *Campaign) ResumeExecution(executionID ExecutionID, actor ActorID, at time.Time) error {
	execution, ok := c.executions[executionID]
	if !ok {
		return fmt.Errorf("%w: участие %s", ErrExecutionNotFound, executionID)
	}
	if execution.state != ExecutionSuspended {
		return fmt.Errorf("%w: участие %s в состоянии %q", ErrResumeNotSuspended, executionID, execution.state)
	}
	if err := actor.validate(); err != nil {
		return err
	}
	execution.state = ExecutionActive
	execution.appendJournal(at, execution.step, ActionChannel{}, OutcomeResumed, actor, "")
	c.record(OutreachOutcomeRecorded{
		CampaignID:  c.id,
		TenantID:    c.tenantID,
		ExecutionID: execution.id,
		SequenceID:  execution.sequence,
		Lead:        execution.lead,
		Step:        execution.step,
		Result:      OutcomeResumed,
		At:          at.UTC(),
	})
	return nil
}

// AdvanceSequences выполняет один проход движка последовательностей.
//
// Проход ничего не отправляет: для каждого активного участия он либо формирует
// черновик очередного шага и ставит его в очередь утверждений, либо, если
// черновик уже утверждён, продвигает цепочку по условию перехода. Неутверждённый
// шаг не пропускается и не утверждается по сроку: по нему фиксируется простой.
func (c *Campaign) AdvanceSequences(now time.Time, limits ParticipationLimits) (AdvanceReport, error) {
	if c.state == CampaignStateCompleted {
		return AdvanceReport{}, nil
	}
	var report AdvanceReport
	pending := len(c.PendingApproval())

	for _, id := range c.executionOrder {
		execution := c.executions[id]
		if execution.state != ExecutionActive {
			continue
		}
		definition, ok := c.definitions[execution.sequence]
		if !ok {
			continue
		}
		if err := c.advanceExecutionLoop(execution, definition, now, limits, &pending, &report); err != nil {
			return report, err
		}
	}
	return report, nil
}

// advanceExecutionLoop доводит одно участие до шага, на котором оно обязано
// остановиться: черновик сформирован и ждёт утверждения, условие перехода
// удерживает шаг или шаги исчерпаны. Возврат означает остановку прохода по
// участию.
func (c *Campaign) advanceExecutionLoop(
	execution *SequenceExecution,
	definition *SequenceDefinition,
	now time.Time,
	limits ParticipationLimits,
	pending *int,
	report *AdvanceReport,
) error {
	for {
		if execution.step >= definition.Len() {
			c.completeExecution(execution, now, report)
			return nil
		}
		step := definition.steps[execution.step]

		if !execution.draftCreated {
			if now.Before(execution.eligibleAt) {
				// Задержка шага ещё не прошла: черновик не формируется раньше срока.
				return nil
			}
			if limits.MaxPendingDrafts > 0 && *pending >= limits.MaxPendingDrafts {
				report.Halted = true
				c.markStall(execution, step.action, now, report)
				return nil
			}
			if limits.MaxTouchesPerLead > 0 &&
				c.touchesForLead(execution.lead, now, limits.TouchPeriod) >= limits.MaxTouchesPerLead {
				c.markStall(execution, step.action, now, report)
				return nil
			}
			draftID := sequenceDraftID(execution.id, execution.step)
			spec := DraftSpec{
				Material:  step.material,
				Segment:   step.segment,
				Recipient: execution.recipient,
				Channel:   step.action,
			}
			if _, _, err := c.DraftAction(draftID, spec, now); err != nil {
				return err
			}
			execution.draftCreated = true
			execution.draftID = draftID
			execution.appendJournal(now, execution.step, step.action, OutcomeDrafted, "", "")
			report.Drafted = append(report.Drafted, DraftRef{
				ExecutionID: execution.id,
				Step:        execution.step,
				DraftID:     draftID,
			})
			*pending++
			c.record(OutreachOutcomeRecorded{
				CampaignID:  c.id,
				TenantID:    c.tenantID,
				ExecutionID: execution.id,
				SequenceID:  execution.sequence,
				Lead:        execution.lead,
				Step:        execution.step,
				Result:      OutcomeDrafted,
				At:          now.UTC(),
			})
			return nil
		}

		draft, ok := c.drafts[execution.draftID]
		if !ok {
			return nil
		}
		switch draft.State() {
		case DraftStateApproved, DraftStateExecuted:
			if !c.advanceExecution(execution, definition, step, draft, now, report) {
				return nil
			}
			// Шаг продвинут: если следующий шаг без задержки и условие не требует
			// реакции, он формируется в этом же проходе.
			continue
		case DraftStateRejected:
			// Шаг отклонён: цепочка не переходит дальше и не пропускает шаг.
			// Оператор может доработать черновик и утвердить его заново.
			execution.appendJournal(now, execution.step, step.action, OutcomeRejected, c.approverFor(draft), "")
			c.markStall(execution, step.action, now, report)
			return nil
		default:
			// «Предложено», «ожидает утверждения», «просрочено» — шаг не утверждён.
			// Автоутверждения по сроку нет: фиксируем простой.
			c.markStall(execution, step.action, now, report)
			return nil
		}
	}
}

// advanceExecution продвигает цепочку по утверждённому шагу и сообщает, был ли
// шаг продвинут. Условие перехода определяет ветку: если оно не выполнено, шаг не
// пропускается и цепочка ждёт.
func (c *Campaign) advanceExecution(
	execution *SequenceExecution,
	definition *SequenceDefinition,
	step SequenceStep,
	draft *ActionDraft,
	now time.Time,
	report *AdvanceReport,
) bool {
	execution.appendJournal(now, execution.step, step.action, OutcomeApproved, c.approverFor(draft), "")
	if !conditionSatisfied(step.transition, execution) {
		execution.appendJournal(now, execution.step, step.action, OutcomeHeld, "", "")
		report.Held = append(report.Held, execution.id)
		return false
	}

	advancedStep := execution.step
	execution.step++
	execution.draftCreated = false
	execution.draftID = ""
	execution.stallReported = false
	execution.reactions = make(map[Reaction]bool)
	execution.appendJournal(now, advancedStep, step.action, OutcomeAdvanced, "", "")
	report.Advanced = append(report.Advanced, execution.id)

	if execution.step < definition.Len() {
		next := definition.steps[execution.step]
		execution.eligibleAt = now.Add(next.delay)
	}
	c.record(OutreachOutcomeRecorded{
		CampaignID:  c.id,
		TenantID:    c.tenantID,
		ExecutionID: execution.id,
		SequenceID:  execution.sequence,
		Lead:        execution.lead,
		Step:        advancedStep,
		Result:      OutcomeAdvanced,
		At:          now.UTC(),
	})
	return true
}

// completeExecution завершает цепочку после прохождения всех шагов.
func (c *Campaign) completeExecution(execution *SequenceExecution, now time.Time, report *AdvanceReport) {
	execution.state = ExecutionCompleted
	execution.appendJournal(now, execution.step, ActionChannel{}, OutcomeCompleted, "", "")
	report.Completed = append(report.Completed, execution.id)
	c.record(OutreachOutcomeRecorded{
		CampaignID:  c.id,
		TenantID:    c.tenantID,
		ExecutionID: execution.id,
		SequenceID:  execution.sequence,
		Lead:        execution.lead,
		Step:        execution.step,
		Result:      OutcomeCompleted,
		At:          now.UTC(),
	})
}

// markStall фиксирует простой участия: шаг не утверждён или удерживается
// условием. Повторные проходы не дублируют запись журнала и событие, пока
// простой не разрешён продвижением.
func (c *Campaign) markStall(execution *SequenceExecution, action ActionChannel, now time.Time, report *AdvanceReport) {
	if execution.stallReported {
		report.Stalled = append(report.Stalled, execution.id)
		return
	}
	execution.stallReported = true
	execution.appendJournal(now, execution.step, action, OutcomeStalled, "", "")
	report.Stalled = append(report.Stalled, execution.id)
	c.record(OutreachOutcomeRecorded{
		CampaignID:  c.id,
		TenantID:    c.tenantID,
		ExecutionID: execution.id,
		SequenceID:  execution.sequence,
		Lead:        execution.lead,
		Step:        execution.step,
		Result:      OutcomeStalled,
		At:          now.UTC(),
	})
}

// conditionSatisfied проверяет условие перехода по наблюдённым реакциям участия.
func conditionSatisfied(condition TransitionCondition, execution *SequenceExecution) bool {
	reaction, required := condition.transitionReaction()
	if !required {
		return true
	}
	return execution.HasReaction(reaction)
}

// touchesForLead считает касания по лиду за период: сформированные черновики
// шагов по всем цепочкам, включая текущую.
func (c *Campaign) touchesForLead(lead LeadID, now time.Time, period time.Duration) int {
	from := now.Add(-period)
	count := 0
	for _, execution := range c.executions {
		if execution.lead != lead {
			continue
		}
		for _, entry := range execution.journal {
			if entry.Result != OutcomeDrafted {
				continue
			}
			if entry.At.After(from) && !entry.At.After(now) {
				count++
			}
		}
	}
	return count
}

// approverFor возвращает оператора, утвердившего точную версию черновика.
func (c *Campaign) approverFor(draft *ActionDraft) ActorID {
	for _, decision := range c.decisions {
		if decision.draft == draft.id && decision.draftVersion == draft.version && decision.IsApproval() {
			return decision.actor
		}
	}
	return ""
}

// stepAction возвращает тип действия текущего шага участия, если он определён.
func (c *Campaign) stepAction(execution *SequenceExecution) (ActionChannel, bool) {
	definition, ok := c.definitions[execution.sequence]
	if !ok {
		return ActionChannel{}, false
	}
	step, ok := definition.Step(execution.step)
	if !ok {
		return ActionChannel{}, false
	}
	return step.action, true
}

// record добавляет событие в буфер агрегата.
func (c *Campaign) record(event DomainEvent) {
	c.events = append(c.events, event)
}

// reactionOutcome переводит реакцию в результат журнала.
func reactionOutcome(reaction Reaction) StepOutcome {
	if reaction.isReply() {
		return OutcomeSuspended
	}
	if reaction.isStopping() {
		return OutcomeStopped
	}
	return OutcomeRecorded
}

// SequenceDefinition возвращает сценарий по идентификатору.
func (c *Campaign) SequenceDefinition(id SequenceID) (*SequenceDefinition, bool) {
	definition, ok := c.definitions[id]
	return definition, ok
}

// SequenceDefinitions возвращает сценарии кампании в порядке создания.
func (c *Campaign) SequenceDefinitions() []*SequenceDefinition {
	out := make([]*SequenceDefinition, 0, len(c.definitionOrder))
	for _, id := range c.definitionOrder {
		out = append(out, c.definitions[id])
	}
	return out
}

// Execution возвращает участие по идентификатору.
func (c *Campaign) Execution(id ExecutionID) (*SequenceExecution, bool) {
	execution, ok := c.executions[id]
	return execution, ok
}

// Executions возвращает участия кампании в порядке добавления.
func (c *Campaign) Executions() []*SequenceExecution {
	out := make([]*SequenceExecution, 0, len(c.executionOrder))
	for _, id := range c.executionOrder {
		out = append(out, c.executions[id])
	}
	return out
}

// PendingSequenceDrafts возвращает число черновиков, порождённых цепочками и
// ожидающих утверждения. Показатель простоя (spec outreach-sequences).
func (c *Campaign) PendingSequenceDrafts() int {
	count := 0
	for _, execution := range c.executions {
		if !execution.draftCreated {
			continue
		}
		if draft, ok := c.drafts[execution.draftID]; ok && draft.State() == DraftStatePendingApproval {
			count++
		}
	}
	return count
}

// JournalForLead возвращает журнал всех цепочек лида, упорядоченный по времени.
func (c *Campaign) JournalForLead(lead LeadID) []JournalEntry {
	var out []JournalEntry
	for _, execution := range c.executions {
		if execution.lead != lead {
			continue
		}
		out = append(out, execution.journal...)
	}
	sortJournal(out)
	return out
}

// OutreachJournal возвращает журнал всех цепочек кампании, упорядоченный по
// времени.
func (c *Campaign) OutreachJournal() []JournalEntry {
	var out []JournalEntry
	for _, execution := range c.executions {
		out = append(out, execution.journal...)
	}
	sortJournal(out)
	return out
}

// sortJournal упорядочивает записи журнала по времени, сохраняя порядок
// добавления при равном времени.
func sortJournal(entries []JournalEntry) {
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && entries[j].At.Before(entries[j-1].At); j-- {
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}
}
