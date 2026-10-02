package domain

import (
	"fmt"
	"strings"
	"time"
)

// EngagementEvent — aggregate root engagement-event: квалифицированный факт
// взаимодействия (architecture/domain-catalog.yaml, aggregate engagement,
// AR engagement-event; зона analytics).
//
// Агрегат собирается только через Qualify из сырого сигнала: сырой сигнал не
// равен квалифицированному событию. Квалификация отвергает пиксельные сигналы
// (policy pixel-free-observation), проверяет вид, исход и уверенность.
type EngagementEvent struct {
	id            EngagementEventID
	signalID      SignalID
	tenantID      TenantID
	companyID     CompanyID
	kind          EngagementKind
	confidence    SignalConfidence
	outcome       Outcome
	linkRef       LinkRef
	target        string
	occurredAt    time.Time
	correlationID CorrelationID
	causationID   CausationID
	version       int

	events []DomainEvent
}

// QualifyParams — вход квалификации сырого сигнала в событие взаимодействия.
type QualifyParams struct {
	// ID — идентификатор создаваемого события взаимодействия.
	ID EngagementEventID
	// Signal — сырой сигнал к квалификации.
	Signal RawSignal
	// EventID — идентификатор доменного события engagement-observed.
	EventID EventID
	// Confidence — уверенность в квалификации. Пусто — выводится из источника
	// сигнала (ConfidenceForSource).
	Confidence SignalConfidence
	// CorrelationID — сквозной сценарий; пусто — берётся из сигнала, затем из
	// идентификатора события.
	CorrelationID CorrelationID
	// CausationID — сообщение-причина; пусто — берётся из сигнала.
	CausationID CausationID
}

// Qualify квалифицирует сырой сигнал в событие взаимодействия и фиксирует
// доменное событие engagement-observed. Квалификация отвергает пиксельный вид
// ErrPixelBasedKind, проверяет вид, исход, уверенность и наличие ссылки перехода.
func Qualify(p QualifyParams) (*EngagementEvent, error) {
	if err := p.ID.validate(); err != nil {
		return nil, err
	}
	if err := p.EventID.validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMissingEventID, err)
	}
	if err := p.Signal.validate(); err != nil {
		return nil, err
	}

	kind, err := ParseEngagementKind(p.Signal.KindHint)
	if err != nil {
		return nil, err
	}
	outcome, err := ParseOutcome(kind, p.Signal.Outcome)
	if err != nil {
		return nil, err
	}

	confidence := p.Confidence
	if confidence == "" {
		confidence = ConfidenceForSource(p.Signal.Source)
	}
	if !confidence.isValid() {
		return nil, fmt.Errorf("%w: %q", ErrUnknownConfidence, confidence)
	}

	var linkRef LinkRef
	var target string
	if kind == KindTransition {
		if err := p.Signal.LinkRef.validate(); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrMissingLinkRef, err)
		}
		if strings.TrimSpace(p.Signal.Target) == "" {
			return nil, fmt.Errorf("%w: ссылка %s", ErrMissingTarget, p.Signal.LinkRef)
		}
		linkRef = p.Signal.LinkRef
		target = strings.TrimSpace(p.Signal.Target)
	}

	correlation := p.CorrelationID
	if correlation == "" {
		correlation = p.Signal.CorrelationID
	}
	if correlation == "" {
		correlation = CorrelationID(p.ID)
	}
	causation := p.CausationID
	if causation == "" {
		causation = p.Signal.CausationID
	}

	event := &EngagementEvent{
		id:            p.ID,
		signalID:      p.Signal.SignalID,
		tenantID:      p.Signal.TenantID,
		companyID:     p.Signal.CompanyID,
		kind:          kind,
		confidence:    confidence,
		outcome:       outcome,
		linkRef:       linkRef,
		target:        target,
		occurredAt:    p.Signal.OccurredAt,
		correlationID: correlation,
		causationID:   causation,
		version:       1,
	}

	event.record(EngagementObserved{
		EventHeader: EventHeader{
			EventID:       p.EventID,
			OccurredAt:    p.Signal.OccurredAt,
			TenantID:      p.Signal.TenantID,
			CorrelationID: correlation,
			CausationID:   causation,
		},
		EngagementEventID: p.ID,
		CompanyID:         p.Signal.CompanyID,
		Kind:              kind,
		Confidence:        confidence,
		Outcome:           outcome,
		LinkRef:           linkRef,
		Target:            target,
	})
	return event, nil
}

// record добавляет доменное событие в буфер агрегата.
func (e *EngagementEvent) record(event DomainEvent) {
	if e == nil || event == nil {
		return
	}
	e.events = append(e.events, event)
}

// ID возвращает идентификатор события взаимодействия.
func (e *EngagementEvent) ID() EngagementEventID { return e.id }

// SignalID возвращает идентификатор сырого сигнала.
func (e *EngagementEvent) SignalID() SignalID { return e.signalID }

// TenantID возвращает тенант-владельца.
func (e *EngagementEvent) TenantID() TenantID { return e.tenantID }

// CompanyID возвращает компанию, к которой отнесено взаимодействие.
func (e *EngagementEvent) CompanyID() CompanyID { return e.companyID }

// Kind возвращает квалифицированный вид взаимодействия.
func (e *EngagementEvent) Kind() EngagementKind { return e.kind }

// Confidence возвращает уверенность в квалификации.
func (e *EngagementEvent) Confidence() SignalConfidence { return e.confidence }

// Outcome возвращает квалифицированный исход.
func (e *EngagementEvent) Outcome() Outcome { return e.outcome }

// LinkRef возвращает непрозрачную ссылку перехода.
func (e *EngagementEvent) LinkRef() LinkRef { return e.linkRef }

// Target возвращает адрес назначения перехода.
func (e *EngagementEvent) Target() string { return e.target }

// OccurredAt возвращает момент взаимодействия.
func (e *EngagementEvent) OccurredAt() time.Time { return e.occurredAt }

// CorrelationID возвращает сквозной сценарий события.
func (e *EngagementEvent) CorrelationID() CorrelationID { return e.correlationID }

// CausationID возвращает сообщение-причину события.
func (e *EngagementEvent) CausationID() CausationID { return e.causationID }

// Version возвращает версию агрегата для оптимистичной блокировки.
func (e *EngagementEvent) Version() int { return e.version }

// DomainEvents возвращает копию неподтверждённых событий.
func (e *EngagementEvent) DomainEvents() []DomainEvent {
	if e == nil || len(e.events) == 0 {
		return nil
	}
	events := make([]DomainEvent, len(e.events))
	copy(events, e.events)
	return events
}

// PullDomainEvents возвращает неподтверждённые события и очищает буфер.
func (e *EngagementEvent) PullDomainEvents() []DomainEvent {
	events := e.DomainEvents()
	if e != nil {
		e.events = nil
	}
	return events
}
