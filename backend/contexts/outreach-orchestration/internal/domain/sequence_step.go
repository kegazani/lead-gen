package domain

import (
	"fmt"
	"strings"
	"time"
)

// TransitionCondition — условие перехода от шага к следующему шагу сценария.
//
// Набор закрыт и не содержит условия по открытию письма: сигнал открытия не
// собирается (read-tracking), поэтому попытка задать его отвергается, а не
// игнорируется (design.md, Решение 3; spec outreach-sequences).
type TransitionCondition string

const (
	// TransitionDefault — перейти к следующему шагу без условия. Применяется,
	// когда условие перехода не задано.
	TransitionDefault TransitionCondition = "default"

	// TransitionOnTransition — перейти, если получатель перешёл по ссылке.
	TransitionOnTransition TransitionCondition = "transitioned"

	// TransitionOnNoTransition — перейти, если получатель не перешёл по ссылке.
	TransitionOnNoTransition TransitionCondition = "not-transitioned"

	// TransitionOnReply — перейти, если получатель ответил.
	TransitionOnReply TransitionCondition = "replied"

	// TransitionOnNoReply — перейти, если получатель не ответил.
	TransitionOnNoReply TransitionCondition = "not-replied"
)

// ParseTransitionCondition разбирает условие перехода. Пустое значение даёт
// переход по умолчанию. Условие по открытию письма отвергается явной ошибкой:
// сигнал не собирается, поэтому условие не существует в языке ветвления.
func ParseTransitionCondition(name string) (TransitionCondition, error) {
	normalized := strings.ToLower(strings.TrimSpace(name))
	switch normalized {
	case "", string(TransitionDefault):
		return TransitionDefault, nil
	case string(TransitionOnTransition):
		return TransitionOnTransition, nil
	case string(TransitionOnNoTransition):
		return TransitionOnNoTransition, nil
	case string(TransitionOnReply):
		return TransitionOnReply, nil
	case string(TransitionOnNoReply):
		return TransitionOnNoReply, nil
	case "opened", "open", "opened-email":
		return "", fmt.Errorf("%w: %q", ErrOpenedSignalUnavailable, name)
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownTransitionCondition, name)
	}
}

// String возвращает каноническое имя условия.
func (c TransitionCondition) String() string { return string(c) }

// isValid сообщает, что условие объявлено доменом.
func (c TransitionCondition) isValid() bool {
	switch c {
	case TransitionDefault, TransitionOnTransition, TransitionOnNoTransition,
		TransitionOnReply, TransitionOnNoReply:
		return true
	default:
		return false
	}
}

// transitionReaction возвращает реакцию, наличие которой требуется условием, и
// признак того, что условие вообще зависит от реакции. Условие по умолчанию
// реакции не требует.
func (c TransitionCondition) transitionReaction() (Reaction, bool) {
	switch c {
	case TransitionOnTransition:
		return ReactionTransitioned, true
	case TransitionOnNoTransition:
		return ReactionNotTransitioned, true
	case TransitionOnReply:
		return ReactionReplied, true
	case TransitionOnNoReply:
		return ReactionNotReplied, true
	default:
		return "", false
	}
}

// SequenceStep — value object: неизменяемое описание шага последовательности и
// его задержки (architecture/domain-catalog.yaml, aggregate campaign,
// value_objects sequence-step). Шаг задаёт тип действия (канал), материал и
// сегмент будущего черновика, задержку перед шагом и условие перехода.
//
// Значение неизменяемо: поля закрыты, создать шаг можно только конструктором
// NewSequenceStep, который проверяет состав и условие.
type SequenceStep struct {
	action     ActionChannel
	material   MaterialID
	segment    SegmentID
	delay      time.Duration
	transition TransitionCondition
}

// SequenceStepParams — данные шага сценария.
type SequenceStepParams struct {
	// Action — тип действия шага: канал, по которому шаг будет исполнен после
	// утверждения.
	Action ActionChannel

	// Material — ссылка на версию материала (владение content-production).
	Material MaterialID

	// Segment — ссылка на снимок аудитории (владение audience).
	Segment SegmentID

	// Delay — задержка перед шагом; неотрицательна. Ноль означает «без
	// задержки».
	Delay time.Duration

	// Transition — условие перехода к следующему шагу. Пустое значение
	// означает TransitionDefault.
	Transition TransitionCondition
}

// NewSequenceStep создаёт проверенный шаг сценария.
func NewSequenceStep(params SequenceStepParams) (SequenceStep, error) {
	if params.Action.IsZero() {
		return SequenceStep{}, fmt.Errorf("%w: тип действия шага не задан", ErrEmptyDraftField)
	}
	if err := params.Material.validate(); err != nil {
		return SequenceStep{}, err
	}
	if err := params.Segment.validate(); err != nil {
		return SequenceStep{}, err
	}
	if params.Delay < 0 {
		return SequenceStep{}, fmt.Errorf("%w: %s", ErrInvalidStepDelay, params.Delay)
	}
	transition := params.Transition
	if transition == "" {
		transition = TransitionDefault
	}
	if !transition.isValid() {
		return SequenceStep{}, fmt.Errorf("%w: %q", ErrUnknownTransitionCondition, transition)
	}
	return SequenceStep{
		action:     params.Action,
		material:   params.Material,
		segment:    params.Segment,
		delay:      params.Delay,
		transition: transition,
	}, nil
}

// Action возвращает тип действия шага.
func (s SequenceStep) Action() ActionChannel { return s.action }

// Material возвращает материал шага.
func (s SequenceStep) Material() MaterialID { return s.material }

// Segment возвращает сегмент шага.
func (s SequenceStep) Segment() SegmentID { return s.segment }

// Delay возвращает задержку перед шагом.
func (s SequenceStep) Delay() time.Duration { return s.delay }

// Transition возвращает условие перехода к следующему шагу.
func (s SequenceStep) Transition() TransitionCondition { return s.transition }
