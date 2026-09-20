package domain

import (
	"fmt"
	"strings"
	"time"
)

// DecisionOutcome — результат решения оператора.
type DecisionOutcome string

const (
	// DecisionApproved — черновик утверждён.
	DecisionApproved DecisionOutcome = "approved"

	// DecisionRejected — черновик отклонён и возвращён на доработку.
	DecisionRejected DecisionOutcome = "rejected"
)

func (o DecisionOutcome) isValid() bool {
	switch o {
	case DecisionApproved, DecisionRejected:
		return true
	default:
		return false
	}
}

// OperatorDecision — сущность агрегата campaign: аудируемое решение человека
// по точной версии черновика.
//
// Решение неизменяемо после создания: автор, время, результат и причина
// фиксируются конструктором. Привязка к версии черновика обязательна —
// решение без версии не создаётся (инвариант I2). Журнал решений — это
// выборка этих сущностей по черновику или кампании, а не отдельная модель.
type OperatorDecision struct {
	id           DecisionID
	campaign     CampaignID
	draft        DraftID
	draftVersion int
	actor        ActorID
	outcome      DecisionOutcome
	reason       string
	decidedAt    time.Time
	batchDigest  string
}

// decisionOption донастраивает решение при создании. Признак пакетного
// принятия не входит в обязательную форму решения и добавляется только
// пакетным путём, поэтому одиночное решение его не несёт.
type decisionOption func(*OperatorDecision)

// withBatchProvenance помечает решение признаком пакетного принятия: digest
// состава, в котором черновик был утверждён. Пустой digest ничего не меняет.
func withBatchProvenance(digest string) decisionOption {
	return func(d *OperatorDecision) {
		d.batchDigest = strings.TrimSpace(digest)
	}
}

// newOperatorDecision создаёт решение и проверяет его обязательные части.
// Отклонение требует причины: доработка без причины запрещена требованием.
func newOperatorDecision(
	id DecisionID,
	campaign CampaignID,
	draft DraftID,
	draftVersion int,
	actor ActorID,
	outcome DecisionOutcome,
	reason string,
	decidedAt time.Time,
	options ...decisionOption,
) (OperatorDecision, error) {
	if err := id.validate(); err != nil {
		return OperatorDecision{}, err
	}
	if err := campaign.validate(); err != nil {
		return OperatorDecision{}, err
	}
	if err := draft.validate(); err != nil {
		return OperatorDecision{}, err
	}
	if err := actor.validate(); err != nil {
		return OperatorDecision{}, err
	}
	if draftVersion <= 0 {
		return OperatorDecision{}, fmt.Errorf("%w: черновик %s", ErrDecisionWithoutVersion, draft)
	}
	if !outcome.isValid() {
		return OperatorDecision{}, fmt.Errorf("%w: %q", ErrInvalidDecisionOutcome, outcome)
	}
	if decidedAt.IsZero() {
		return OperatorDecision{}, fmt.Errorf("%w: черновик %s", ErrDecisionTimeRequired, draft)
	}
	if outcome == DecisionRejected && strings.TrimSpace(reason) == "" {
		return OperatorDecision{}, fmt.Errorf("%w: отклонение черновика %s", ErrDecisionReasonRequired, draft)
	}
	decision := OperatorDecision{
		id:           id,
		campaign:     campaign,
		draft:        draft,
		draftVersion: draftVersion,
		actor:        actor,
		outcome:      outcome,
		reason:       reason,
		decidedAt:    decidedAt.UTC(),
	}
	for _, apply := range options {
		apply(&decision)
	}
	return decision, nil
}

// ID возвращает идентификатор решения.
func (d OperatorDecision) ID() DecisionID { return d.id }

// CampaignID возвращает кампанию решения.
func (d OperatorDecision) CampaignID() CampaignID { return d.campaign }

// DraftID возвращает черновик, к которому относится решение.
func (d OperatorDecision) DraftID() DraftID { return d.draft }

// DraftVersion возвращает точную версию черновика, к которой привязано решение.
func (d OperatorDecision) DraftVersion() int { return d.draftVersion }

// Actor возвращает автора решения.
func (d OperatorDecision) Actor() ActorID { return d.actor }

// Outcome возвращает результат решения.
func (d OperatorDecision) Outcome() DecisionOutcome { return d.outcome }

// Reason возвращает причину решения (обязательна для отклонения).
func (d OperatorDecision) Reason() string { return d.reason }

// DecidedAt возвращает время решения (UTC).
func (d OperatorDecision) DecidedAt() time.Time { return d.decidedAt }

// IsApproval сообщает, что решение положительное.
func (d OperatorDecision) IsApproval() bool { return d.outcome == DecisionApproved }

// IsBatchDecision сообщает, что решение принято пакетно. Признак отличим при
// аудите: одиночное решение всегда возвращает false.
func (d OperatorDecision) IsBatchDecision() bool { return d.batchDigest != "" }

// BatchDigest возвращает digest состава, в рамках которого решение принято
// пакетно, или пустую строку для одиночного решения.
func (d OperatorDecision) BatchDigest() string { return d.batchDigest }
