package domain

import (
	"fmt"
	"strings"
	"time"
)

// EngagementSnapshot — сохранённое состояние агрегата engagement-event.
//
// Адаптер репозитория не имеет доступа к закрытым полям агрегата, поэтому он
// читает и пишет EngagementSnapshot с экспортированными полями. Доменный объект
// из снимка собирается только через Rehydrate, который проверяет инварианты и не
// эмитит события: восстановление не является новым фактом домена.
type EngagementSnapshot struct {
	ID            EngagementEventID
	SignalID      SignalID
	TenantID      TenantID
	CompanyID     CompanyID
	Kind          EngagementKind
	Confidence    SignalConfidence
	Outcome       Outcome
	LinkRef       LinkRef
	Target        string
	OccurredAt    time.Time
	CorrelationID CorrelationID
	CausationID   CausationID
	Version       int
}

// Snapshot возвращает состояние агрегата для сохранения.
func (e *EngagementEvent) Snapshot() EngagementSnapshot {
	if e == nil {
		return EngagementSnapshot{}
	}
	return EngagementSnapshot{
		ID:            e.id,
		SignalID:      e.signalID,
		TenantID:      e.tenantID,
		CompanyID:     e.companyID,
		Kind:          e.kind,
		Confidence:    e.confidence,
		Outcome:       e.outcome,
		LinkRef:       e.linkRef,
		Target:        e.target,
		OccurredAt:    e.occurredAt,
		CorrelationID: e.correlationID,
		CausationID:   e.causationID,
		Version:       e.version,
	}
}

// Rehydrate восстанавливает агрегат из сохранённого состояния, проверяя
// инварианты: непустые идентификаторы и время, известный вид, согласованный
// исход, известную уверенность, положительную версию и адрес перехода.
// События не эмитятся.
func Rehydrate(s EngagementSnapshot) (*EngagementEvent, error) {
	if err := s.ID.validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSnapshot, err)
	}
	if err := s.TenantID.validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSnapshot, err)
	}
	if err := s.CompanyID.validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSnapshot, err)
	}
	if s.OccurredAt.IsZero() {
		return nil, fmt.Errorf("%w: событие %s без времени", ErrInvalidSnapshot, s.ID)
	}
	if s.Version <= 0 {
		return nil, fmt.Errorf("%w: событие %s версии %d", ErrInvalidSnapshot, s.ID, s.Version)
	}
	if !s.Kind.isValid() {
		return nil, fmt.Errorf("%w: событие %s: неизвестный вид %q", ErrInvalidSnapshot, s.ID, s.Kind)
	}
	if !s.Confidence.isValid() {
		return nil, fmt.Errorf("%w: событие %s: неизвестная уверенность %q", ErrInvalidSnapshot, s.ID, s.Confidence)
	}
	if _, err := ParseOutcome(s.Kind, string(s.Outcome)); err != nil {
		return nil, fmt.Errorf("%w: событие %s: %v", ErrInvalidSnapshot, s.ID, err)
	}
	if s.Kind == KindTransition {
		if err := s.LinkRef.validate(); err != nil {
			return nil, fmt.Errorf("%w: событие %s: %v", ErrInvalidSnapshot, s.ID, err)
		}
		if strings.TrimSpace(s.Target) == "" {
			return nil, fmt.Errorf("%w: событие %s: переход без адреса", ErrInvalidSnapshot, s.ID)
		}
	}

	return &EngagementEvent{
		id:            s.ID,
		signalID:      s.SignalID,
		tenantID:      s.TenantID,
		companyID:     s.CompanyID,
		kind:          s.Kind,
		confidence:    s.Confidence,
		outcome:       s.Outcome,
		linkRef:       s.LinkRef,
		target:        strings.TrimSpace(s.Target),
		occurredAt:    s.OccurredAt,
		correlationID: s.CorrelationID,
		causationID:   s.CausationID,
		version:       s.Version,
	}, nil
}
