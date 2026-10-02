package domain

import (
	"fmt"
	"strings"
	"time"
)

// Transition — запись перехода статуса публикации. Журнал переходов хранит
// автора и время решения, поэтому утверждение и отказ восстановимы.
type Transition struct {
	From   PublicationStatus
	To     PublicationStatus
	Actor  ActorID
	At     time.Time
	Reason string
}

// Approval — решение человека об утверждении материала (policy
// approved-publication-only). Автор и время обязательны: утверждение
// безымянным не бывает.
type Approval struct {
	Actor ActorID
	At    time.Time
}

// Outcome — зафиксированный результат попытки публикации: адрес материала при
// успехе либо причина отказа площадки, и время попытки.
type Outcome struct {
	Address       string
	FailureReason string
	At            time.Time
}

// NewPublicationParams — параметры создания черновика публикации.
type NewPublicationParams struct {
	ID             PublicationID
	TenantID       TenantID
	Digest         DigestRef
	Target         PublicationTarget
	Material       Material
	IdempotencyKey IdempotencyKey
	At             time.Time
}

// PublishParams — параметры фиксации успешной публикации.
type PublishParams struct {
	Actor         ActorID
	At            time.Time
	Address       string
	EventID       EventID
	CorrelationID CorrelationID
	CausationID   CausationID
}

// FailParams — параметры фиксации отказа площадки.
type FailParams struct {
	Actor       ActorID
	At          time.Time
	Reason      string
	CausationID CausationID
}

// Publication — aggregate root контекста publishing. Одна публикация — один
// материал под одну площадку; материалы под разные площадки задаются разными
// агрегатами (design.md, Решение 4).
//
// Инварианты:
//   - публикуется только утверждённая версия (approved-publication-only);
//   - повтор с тем же idempotency key не создаёт вторую публикацию;
//   - терминальное состояние не меняется.
type Publication struct {
	id             PublicationID
	tenantID       TenantID
	digest         DigestRef
	target         PublicationTarget
	material       Material
	status         PublicationStatus
	idempotencyKey IdempotencyKey
	approval       *Approval
	outcome        *Outcome
	transitions    []Transition
	version        int
	events         []DomainEvent
}

// Publication реализует EventProducer.
var _ EventProducer = (*Publication)(nil)

// NewPublication создаёт черновик публикации. Публикация не эмитит событий: факт
// появляется только при успешной публикации на площадке.
func NewPublication(params NewPublicationParams) (*Publication, error) {
	if err := params.ID.validate(); err != nil {
		return nil, err
	}
	if err := params.TenantID.validate(); err != nil {
		return nil, err
	}
	if err := params.Digest.Validate(); err != nil {
		return nil, err
	}
	if !params.Target.Platform().Valid() || params.Target.Account() == "" {
		return nil, fmt.Errorf("%w: %s", ErrUnknownPlatform, params.Target)
	}
	if params.Material.Body() == "" {
		return nil, ErrEmptyMaterial
	}
	if err := params.IdempotencyKey.validate(); err != nil {
		return nil, err
	}
	if params.At.IsZero() {
		return nil, ErrMissingTime
	}
	p := &Publication{
		id:             params.ID,
		tenantID:       params.TenantID,
		digest:         params.Digest,
		target:         params.Target,
		material:       params.Material,
		status:         StatusDraft,
		idempotencyKey: params.IdempotencyKey,
		version:        1,
	}
	p.transitions = append(p.transitions, Transition{To: StatusDraft, At: params.At})
	return p, nil
}

// ID возвращает идентификатор публикации.
func (p *Publication) ID() PublicationID { return p.id }

// TenantID возвращает тенанта-владельца записи.
func (p *Publication) TenantID() TenantID { return p.tenantID }

// Digest возвращает ссылку на выжимку-источник.
func (p *Publication) Digest() DigestRef { return p.digest }

// Target возвращает площадку и учётную запись назначения.
func (p *Publication) Target() PublicationTarget { return p.target }

// Material возвращает материал публикации.
func (p *Publication) Material() Material { return p.material }

// Status возвращает текущий статус.
func (p *Publication) Status() PublicationStatus { return p.status }

// IdempotencyKey возвращает ключ идемпотентности публикации.
func (p *Publication) IdempotencyKey() IdempotencyKey { return p.idempotencyKey }

// Approval возвращает решение об утверждении, если оно принято.
func (p *Publication) Approval() (Approval, bool) {
	if p.approval == nil {
		return Approval{}, false
	}
	return *p.approval, true
}

// Outcome возвращает зафиксированный результат попытки публикации, если он есть.
func (p *Publication) Outcome() (Outcome, bool) {
	if p.outcome == nil {
		return Outcome{}, false
	}
	return *p.outcome, true
}

// Transitions возвращает копию журнала переходов.
func (p *Publication) Transitions() []Transition {
	out := make([]Transition, len(p.transitions))
	copy(out, p.transitions)
	return out
}

// Version возвращает версию агрегата для оптимистичной блокировки.
func (p *Publication) Version() int { return p.version }

// DomainEvents возвращает копию неподтверждённых событий.
func (p *Publication) DomainEvents() []DomainEvent {
	out := make([]DomainEvent, len(p.events))
	copy(out, p.events)
	return out
}

// PullDomainEvents возвращает неподтверждённые события и очищает буфер.
func (p *Publication) PullDomainEvents() []DomainEvent {
	out := p.events
	p.events = nil
	return out
}

// Approve утверждает материал к публикации. Переход draft → approved; автор и
// время решения обязательны.
func (p *Publication) Approve(actor ActorID, at time.Time) error {
	if err := requireActor(actor); err != nil {
		return err
	}
	if at.IsZero() {
		return ErrMissingTime
	}
	if err := p.applyTransition(StatusApproved, actor, at, ""); err != nil {
		return err
	}
	p.approval = &Approval{Actor: actor, At: at}
	return nil
}

// Queue откладывает утверждённую публикацию ограничением частоты площадки.
func (p *Publication) Queue(actor ActorID, at time.Time, reason string) error {
	if err := requireActor(actor); err != nil {
		return err
	}
	if at.IsZero() {
		return ErrMissingTime
	}
	return p.applyTransition(StatusQueued, actor, at, reason)
}

// Reject отклоняет материал без публикации. Причина обязательна.
func (p *Publication) Reject(actor ActorID, at time.Time, reason string) error {
	if err := requireActor(actor); err != nil {
		return err
	}
	if at.IsZero() {
		return ErrMissingTime
	}
	if strings.TrimSpace(reason) == "" {
		return ErrReasonRequired
	}
	return p.applyTransition(StatusRejected, actor, at, reason)
}

// MarkPublished фиксирует успех публикации и эмитит material-published. Переход
// разрешён только из утверждённого состояния — иначе ErrApprovalRequired.
func (p *Publication) MarkPublished(params PublishParams) error {
	if err := requireActor(params.Actor); err != nil {
		return err
	}
	if params.At.IsZero() {
		return ErrMissingTime
	}
	if strings.TrimSpace(params.Address) == "" {
		return ErrAddressRequired
	}
	if err := params.EventID.validate(); err != nil {
		return err
	}
	from := p.status
	if !canTransition(from, StatusPublished) {
		return p.transitionError(from, StatusPublished)
	}
	p.applyTransitionUnchecked(StatusPublished, params.Actor, params.At, "")
	p.outcome = &Outcome{Address: params.Address, At: params.At}

	approvedBy := ActorID("")
	if p.approval != nil {
		approvedBy = p.approval.Actor
	}
	p.events = append(p.events, MaterialPublished{
		EventHeader: EventHeader{
			EventID:       params.EventID,
			OccurredAt:    params.At,
			TenantID:      p.tenantID,
			CorrelationID: params.CorrelationID,
			CausationID:   params.CausationID,
		},
		PublicationID: p.id,
		From:          from,
		To:            StatusPublished,
		Actor:         params.Actor,
		Platform:      p.target.Platform(),
		Account:       p.target.Account(),
		Address:       params.Address,
		ApprovedBy:    approvedBy,
	})
	return nil
}

// MarkFailed фиксирует отказ площадки. Автоматического повтора не предполагает:
// повтор — отдельное решение оператора. Причина обязательна.
func (p *Publication) MarkFailed(params FailParams) error {
	if err := requireActor(params.Actor); err != nil {
		return err
	}
	if params.At.IsZero() {
		return ErrMissingTime
	}
	if strings.TrimSpace(params.Reason) == "" {
		return ErrReasonRequired
	}
	if err := p.applyTransition(StatusFailed, params.Actor, params.At, params.Reason); err != nil {
		return err
	}
	p.outcome = &Outcome{FailureReason: strings.TrimSpace(params.Reason), At: params.At}
	return nil
}

// applyTransition проверяет допустимость перехода и применяет его.
func (p *Publication) applyTransition(to PublicationStatus, actor ActorID, at time.Time, reason string) error {
	if !to.Valid() {
		return fmt.Errorf("%w: %q", ErrUnknownStatus, to)
	}
	if p.status.IsTerminal() {
		return fmt.Errorf("%w: %s", ErrTerminalState, p.status)
	}
	if !canTransition(p.status, to) {
		return p.transitionError(p.status, to)
	}
	p.applyTransitionUnchecked(to, actor, at, reason)
	return nil
}

// applyTransitionUnchecked записывает переход без проверки: вызывающий уже
// проверил допустимость.
func (p *Publication) applyTransitionUnchecked(to PublicationStatus, actor ActorID, at time.Time, reason string) {
	from := p.status
	p.status = to
	p.transitions = append(p.transitions, Transition{From: from, To: to, Actor: actor, At: at, Reason: reason})
	p.version++
}

// transitionError различает два запрета: публикацию без утверждения и прочие
// недопустимые переходы.
func (p *Publication) transitionError(from, to PublicationStatus) error {
	if (to == StatusPublished || to == StatusFailed) && (from == StatusDraft || from == StatusRejected) {
		return fmt.Errorf("%w: %s → %s", ErrApprovalRequired, from, to)
	}
	return fmt.Errorf("%w: %s → %s", ErrInvalidTransition, from, to)
}

// requireActor отвергает пустого актора.
func requireActor(actor ActorID) error {
	return actor.validate()
}
