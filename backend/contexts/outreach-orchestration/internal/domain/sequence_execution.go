package domain

import (
	"fmt"
	"time"
)

// ExecutionState — состояние участия лида в сценарии последовательности.
type ExecutionState string

const (
	// ExecutionActive — участие активно: движок формирует черновики шагов.
	ExecutionActive ExecutionState = "active"

	// ExecutionSuspended — участие приостановлено по ответу лида. Возобновление
	// возможно только решением оператора.
	ExecutionSuspended ExecutionState = "suspended"

	// ExecutionStopped — участие остановлено по отписке, отказу, стоп-листу или
	// решению оператора. Возобновлению не подлежит.
	ExecutionStopped ExecutionState = "stopped"

	// ExecutionCompleted — все шаги сценария пройдены.
	ExecutionCompleted ExecutionState = "completed"
)

// isTerminal сообщает, что участие вышло из жизненного цикла и не возобновляется.
func (s ExecutionState) isTerminal() bool {
	switch s {
	case ExecutionStopped, ExecutionCompleted:
		return true
	default:
		return false
	}
}

// isValid сообщает, что состояние участия объявлено доменом.
func (s ExecutionState) isValid() bool {
	switch s {
	case ExecutionActive, ExecutionSuspended, ExecutionStopped, ExecutionCompleted:
		return true
	default:
		return false
	}
}

// StepOutcome — результат по шагу последовательности, фиксируемый в журнале.
type StepOutcome string

const (
	// OutcomeEnrolled — лид добавлен в цепочку.
	OutcomeEnrolled StepOutcome = "enrolled"

	// OutcomeDrafted — черновик шага сформирован и поставлен в очередь утверждений.
	OutcomeDrafted StepOutcome = "drafted"

	// OutcomeApproved — шаг утверждён оператором.
	OutcomeApproved StepOutcome = "approved"

	// OutcomeRejected — шаг отклонён оператором.
	OutcomeRejected StepOutcome = "rejected"

	// OutcomeAdvanced — цепочка перешла к следующему шагу.
	OutcomeAdvanced StepOutcome = "advanced"

	// OutcomeHeld — шаг не пропущен: условие перехода не выполнено, цепочка ждёт.
	OutcomeHeld StepOutcome = "held"

	// OutcomeStalled — простой: шаг ждёт утверждения оператора.
	OutcomeStalled StepOutcome = "stalled"

	// OutcomeSuspended — цепочка приостановлена по ответу лида.
	OutcomeSuspended StepOutcome = "suspended"

	// OutcomeResumed — цепочка возобновлена решением оператора.
	OutcomeResumed StepOutcome = "resumed"

	// OutcomeStopped — цепочка остановлена и возобновлению не подлежит.
	OutcomeStopped StepOutcome = "stopped"

	// OutcomeRemoved — лид удалён из цепочки оператором.
	OutcomeRemoved StepOutcome = "removed"

	// OutcomeCompleted — все шаги цепочки пройдены.
	OutcomeCompleted StepOutcome = "completed"

	// OutcomeRecorded — записана реакция лида (переход, отсутствие перехода).
	OutcomeRecorded StepOutcome = "reaction-recorded"

	// OutcomeUnclassified — реакция поступила, но классификация неуверенна,
	// поэтому ветка не выбирается автоматически.
	OutcomeUnclassified StepOutcome = "classification-uncertain"
)

// Reaction — наблюдаемая реакция лида, поступающая от контекста
// engagement-observation. Набор закрыт; открытие письма в него не входит, потому
// что сигнал открытия не собирается.
type Reaction string

const (
	// ReactionTransitioned — получатель перешёл по ссылке.
	ReactionTransitioned Reaction = "transitioned"

	// ReactionNotTransitioned — получатель не перешёл по ссылке.
	ReactionNotTransitioned Reaction = "not-transitioned"

	// ReactionReplied — получатель ответил.
	ReactionReplied Reaction = "replied"

	// ReactionNotReplied — получатель не ответил.
	ReactionNotReplied Reaction = "not-replied"

	// ReactionUnsubscribed — получатель отписался.
	ReactionUnsubscribed Reaction = "unsubscribed"

	// ReactionBounced — сообщение не доставлено (жёсткий отказ).
	ReactionBounced Reaction = "bounced"

	// ReactionSuppressed — адрес попал в стоп-лист.
	ReactionSuppressed Reaction = "suppressed"
)

// isValid сообщает, что реакция объявлена доменом.
func (r Reaction) isValid() bool {
	switch r {
	case ReactionTransitioned, ReactionNotTransitioned, ReactionReplied,
		ReactionNotReplied, ReactionUnsubscribed, ReactionBounced, ReactionSuppressed:
		return true
	default:
		return false
	}
}

// isStopping сообщает, что реакция останавливает цепочку без права возобновления.
func (r Reaction) isStopping() bool {
	switch r {
	case ReactionUnsubscribed, ReactionBounced, ReactionSuppressed:
		return true
	default:
		return false
	}
}

// isReply сообщает, что реакция — ответ лида.
func (r Reaction) isReply() bool { return r == ReactionReplied }

// ParseReaction разбирает реакцию по имени. Набор закрыт; неизвестная реакция
// отвергается, чтобы потребитель не трактовал молча то, чего не знает.
func ParseReaction(name string) (Reaction, error) {
	reaction := Reaction(name)
	if !reaction.isValid() {
		return "", fmt.Errorf("%w: %q", ErrUnknownReaction, name)
	}
	return reaction, nil
}

// JournalEntry — запись журнала выполнения цепочки: какой шаг, когда, с каким
// результатом и кто утвердил (spec outreach-sequences, «Журнал выполнения»).
type JournalEntry struct {
	Execution ExecutionID
	Campaign  CampaignID
	Lead      LeadID
	Step      int
	Action    ActionChannel
	At        time.Time
	Result    StepOutcome
	Actor     ActorID
	Reason    string
}

// SequenceExecution — сущность агрегата campaign: участие конкретного лида в
// сценарии последовательности. Ведёт текущий шаг, наблюдённые реакции и журнал.
//
// Инвариант I3 (завершённое выполнение не возобновляется) выражен состоянием:
// возобновление допустимо только из ExecutionSuspended.
type SequenceExecution struct {
	id            ExecutionID
	campaign      CampaignID
	sequence      SequenceID
	tenant        TenantID
	lead          LeadID
	recipient     RecipientID
	state         ExecutionState
	step          int
	eligibleAt    time.Time
	draftCreated  bool
	draftID       DraftID
	stallReported bool
	reactions     map[Reaction]bool
	stopReason    string
	enrolledAt    time.Time
	journal       []JournalEntry
}

// newSequenceExecution создаёт участие в состоянии «активно». Момент, не раньше
// которого формируется первый черновик, задаётся вызывающим: это задержка
// первого шага относительно добавления лида.
func newSequenceExecution(
	id ExecutionID,
	definition *SequenceDefinition,
	tenant TenantID,
	lead LeadID,
	recipient RecipientID,
	eligibleAt time.Time,
	at time.Time,
) (*SequenceExecution, error) {
	if err := id.validate(); err != nil {
		return nil, err
	}
	if definition == nil {
		return nil, fmt.Errorf("%w: участие %s", ErrSequenceNotFound, id)
	}
	if err := tenant.validate(); err != nil {
		return nil, err
	}
	if err := lead.validate(); err != nil {
		return nil, err
	}
	if err := recipient.validate(); err != nil {
		return nil, err
	}
	if at.IsZero() {
		return nil, fmt.Errorf("%w: участие %s", ErrDecisionTimeRequired, id)
	}
	execution := &SequenceExecution{
		id:         id,
		campaign:   definition.campaign,
		sequence:   definition.id,
		tenant:     tenant,
		lead:       lead,
		recipient:  recipient,
		state:      ExecutionActive,
		step:       0,
		eligibleAt: eligibleAt.UTC(),
		reactions:  make(map[Reaction]bool),
		enrolledAt: at.UTC(),
	}
	execution.appendJournal(at, 0, ActionChannel{}, OutcomeEnrolled, "", "")
	return execution, nil
}

// ID возвращает идентификатор участия.
func (e *SequenceExecution) ID() ExecutionID { return e.id }

// CampaignID возвращает кампанию участия.
func (e *SequenceExecution) CampaignID() CampaignID { return e.campaign }

// SequenceID возвращает сценарий участия.
func (e *SequenceExecution) SequenceID() SequenceID { return e.sequence }

// TenantID возвращает тенанта участия.
func (e *SequenceExecution) TenantID() TenantID { return e.tenant }

// Lead возвращает лида участия.
func (e *SequenceExecution) Lead() LeadID { return e.lead }

// Recipient возвращает адресата будущих черновиков.
func (e *SequenceExecution) Recipient() RecipientID { return e.recipient }

// State возвращает состояние участия.
func (e *SequenceExecution) State() ExecutionState { return e.state }

// StepIndex возвращает индекс текущего шага.
func (e *SequenceExecution) StepIndex() int { return e.step }

// EligibleAt возвращает момент, не раньше которого формируется черновик шага.
func (e *SequenceExecution) EligibleAt() time.Time { return e.eligibleAt }

// DraftCreated сообщает, сформирован ли черновик текущего шага.
func (e *SequenceExecution) DraftCreated() bool { return e.draftCreated }

// DraftID возвращает идентификатор черновика текущего шага (пусто до его
// формирования).
func (e *SequenceExecution) DraftID() DraftID { return e.draftID }

// StopReason возвращает причину остановки участия.
func (e *SequenceExecution) StopReason() string { return e.stopReason }

// EnrolledAt возвращает момент добавления лида в цепочку.
func (e *SequenceExecution) EnrolledAt() time.Time { return e.enrolledAt }

// HasReaction сообщает, наблюдалась ли реакция по текущему шагу.
func (e *SequenceExecution) HasReaction(r Reaction) bool { return e.reactions[r] }

// Journal возвращает копию журнала участия в порядке записи.
func (e *SequenceExecution) Journal() []JournalEntry {
	out := make([]JournalEntry, len(e.journal))
	copy(out, e.journal)
	return out
}

// appendJournal добавляет запись журнала. Action может быть нулевым для записей,
// не относящихся к конкретному действию (добавление, приостановка, остановка).
func (e *SequenceExecution) appendJournal(
	at time.Time,
	step int,
	action ActionChannel,
	result StepOutcome,
	actor ActorID,
	reason string,
) {
	e.journal = append(e.journal, JournalEntry{
		Execution: e.id,
		Campaign:  e.campaign,
		Lead:      e.lead,
		Step:      step,
		Action:    action,
		At:        at.UTC(),
		Result:    result,
		Actor:     actor,
		Reason:    reason,
	})
}

// sequenceDraftID выводит идентификатор черновика шага из участия и позиции
// шага. Вывод детерминирован, поэтому повторный проход движка не создаёт второй
// черновик того же шага.
func sequenceDraftID(id ExecutionID, step int) DraftID {
	return DraftID(fmt.Sprintf("%s.step-%d", id, step))
}
