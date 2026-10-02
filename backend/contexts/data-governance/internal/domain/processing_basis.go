package domain

import (
	"fmt"
	"strings"
	"time"
)

// BasisStatus — статус основания обработки (VO в составе агрегата).
type BasisStatus string

const (
	// BasisActive — основание действует.
	BasisActive BasisStatus = "active"

	// BasisRevoked — основание отозвано; из него нет изменений.
	BasisRevoked BasisStatus = "revoked"
)

// isValid сообщает, что статус объявлен доменом.
func (s BasisStatus) isValid() bool { return s == BasisActive || s == BasisRevoked }

// IsTerminal сообщает, что из статуса нет переходов.
func (s BasisStatus) IsTerminal() bool { return s == BasisRevoked }

// ProcessingBasis — aggregate root processing-basis: основание обработки данных
// тенанта.
//
// Чтобы полномочие было действительно только для указанной цели, категории,
// тенанта и периода (инвариант I1), все четыре условия хранятся в самом агрегате
// и проверяются методом Authorizes. Записи стоп-листа (entity suppression-entry)
// входят в состав агрегата по каталогу; активная запись имеет приоритет над
// кампанией, решением оператора и поставщиком (инвариант I2) — это проверяется
// чистой политикой SuppressionPolicy и методом IsSuppressed.
type ProcessingBasis struct {
	id         BasisID
	tenantID   TenantID
	purpose    ProcessingPurpose
	retention  RetentionPeriod
	validFrom  time.Time
	validUntil time.Time
	status     BasisStatus
	entries    []*SuppressionEntry
	version    int
	events     []DomainEvent
}

// NewBasisParams — вход создания основания обработки.
type NewBasisParams struct {
	ID            BasisID
	TenantID      TenantID
	Purpose       ProcessingPurpose
	Retention     RetentionPeriod
	ValidFrom     time.Time
	ValidUntil    time.Time
	Actor         ActorID
	Reason        string
	At            time.Time
	EventID       EventID
	CorrelationID CorrelationID
	CausationID   CausationID
}

// ChangeAuthorityParams — вход изменения условий действующего основания.
type ChangeAuthorityParams struct {
	Retention     RetentionPeriod
	ValidFrom     time.Time
	ValidUntil    time.Time
	Actor         ActorID
	Reason        string
	At            time.Time
	EventID       EventID
	CorrelationID CorrelationID
	CausationID   CausationID
}

// RevokeParams — вход отзыва основания.
type RevokeParams struct {
	Actor         ActorID
	Reason        string
	At            time.Time
	EventID       EventID
	CorrelationID CorrelationID
	CausationID   CausationID
}

// SuppressEntryParams — вход внесения контакта в стоп-лист агрегата.
type SuppressEntryParams struct {
	ID            SuppressionID
	Contact       ContactRef
	Reason        string
	Actor         ActorID
	At            time.Time
	EventID       EventID
	CorrelationID CorrelationID
	CausationID   CausationID
}

// LiftParams — вход снятия записи стоп-листа.
type LiftParams struct {
	EntryID       SuppressionID
	Actor         ActorID
	Reason        string
	At            time.Time
	EventID       EventID
	CorrelationID CorrelationID
	CausationID   CausationID
}

// NewProcessingBasis создаёт действующее основание обработки и эмитит
// processing-authority-changed. Идентификатор, тенант, цель (с категорией), срок
// хранения и непротиворечивое окно действия обязательны.
func NewProcessingBasis(p NewBasisParams) (*ProcessingBasis, error) {
	if err := p.ID.validate(); err != nil {
		return nil, err
	}
	if err := p.TenantID.validate(); err != nil {
		return nil, err
	}
	if err := p.Purpose.validate(); err != nil {
		return nil, err
	}
	if err := p.Retention.validate(); err != nil {
		return nil, err
	}
	if err := validateValidityWindow(p.ValidFrom, p.ValidUntil); err != nil {
		return nil, err
	}
	if err := validateChangeInput(p.Actor, "", false, p.At, p.EventID); err != nil {
		return nil, err
	}

	basis := &ProcessingBasis{
		id:         p.ID,
		tenantID:   p.TenantID,
		purpose:    p.Purpose,
		retention:  p.Retention,
		validFrom:  p.ValidFrom,
		validUntil: p.ValidUntil,
		status:     BasisActive,
		version:    1,
	}
	basis.emit(ProcessingAuthorityChanged{
		EventHeader:   eventHeader(p.TenantID, p.At, p.EventID, p.CorrelationID, p.CausationID),
		BasisID:       p.ID,
		Change:        AuthorityCreated,
		Status:        basis.status,
		Purpose:       p.Purpose.Name(),
		Category:      p.Purpose.Category(),
		RetentionDays: p.Retention.Days(),
		ValidFrom:     p.ValidFrom,
		ValidUntil:    p.ValidUntil,
		Actor:         p.Actor,
		Reason:        strings.TrimSpace(p.Reason),
	})
	return basis, nil
}

// ID возвращает идентификатор основания.
func (b *ProcessingBasis) ID() BasisID { return b.id }

// TenantID возвращает тенанта-владельца основания.
func (b *ProcessingBasis) TenantID() TenantID { return b.tenantID }

// Purpose возвращает цель обработки вместе с категорией.
func (b *ProcessingBasis) Purpose() ProcessingPurpose { return b.purpose }

// Retention возвращает срок хранения.
func (b *ProcessingBasis) Retention() RetentionPeriod { return b.retention }

// ValidFrom возвращает начало окна действия полномочия.
func (b *ProcessingBasis) ValidFrom() time.Time { return b.validFrom }

// ValidUntil возвращает конец окна действия полномочия.
func (b *ProcessingBasis) ValidUntil() time.Time { return b.validUntil }

// Status возвращает статус основания.
func (b *ProcessingBasis) Status() BasisStatus { return b.status }

// Version возвращает версию агрегата для оптимистичной блокировки.
func (b *ProcessingBasis) Version() int { return b.version }

// Expired сообщает, что окно действия основания истекло к моменту at.
func (b *ProcessingBasis) Expired(at time.Time) bool {
	return b.status == BasisActive && !at.Before(b.validUntil)
}

// Suppressions возвращает копию записей стоп-листа агрегата.
func (b *ProcessingBasis) Suppressions() []SuppressionEntry {
	entries := make([]SuppressionEntry, 0, len(b.entries))
	for _, entry := range b.entries {
		entries = append(entries, *entry)
	}
	return entries
}

// IsSuppressed сообщает, действует ли стоп-лист для контакта, и возвращает
// активную запись. Инвариант I2: активная запись подавляет любое действие.
func (b *ProcessingBasis) IsSuppressed(contact ContactRef) (*SuppressionEntry, bool) {
	for _, entry := range b.entries {
		if entry.active && entry.contact == contact {
			return entry, true
		}
	}
	return nil, false
}

// Authorizes сообщает, действует ли полномочие для заданной цели и категории в
// момент at. Инвариант I1: полномочие действительно только для указанной цели,
// категории, тенанта и периода.
func (b *ProcessingBasis) Authorizes(purpose ProcessingPurpose, at time.Time) bool {
	if b.status != BasisActive {
		return false
	}
	if !b.purpose.Equal(purpose) {
		return false
	}
	if at.IsZero() {
		return false
	}
	if at.Before(b.validFrom) {
		return false
	}
	return at.Before(b.validUntil)
}

// ChangeAuthority меняет срок хранения и окно действия действующего основания и
// эмитит processing-authority-changed. Отозванное основание не меняется.
func (b *ProcessingBasis) ChangeAuthority(p ChangeAuthorityParams) ([]DomainEvent, error) {
	if b.status != BasisActive {
		return nil, fmt.Errorf("%w: основание %s", ErrBasisRevoked, b.id)
	}
	if err := p.Retention.validate(); err != nil {
		return nil, err
	}
	if err := validateValidityWindow(p.ValidFrom, p.ValidUntil); err != nil {
		return nil, err
	}
	if err := validateChangeInput(p.Actor, p.Reason, true, p.At, p.EventID); err != nil {
		return nil, err
	}

	b.retention = p.Retention
	b.validFrom = p.ValidFrom
	b.validUntil = p.ValidUntil
	b.version++
	b.emit(ProcessingAuthorityChanged{
		EventHeader:   eventHeader(b.tenantID, p.At, p.EventID, p.CorrelationID, p.CausationID),
		BasisID:       b.id,
		Change:        AuthorityUpdated,
		Status:        b.status,
		Purpose:       b.purpose.Name(),
		Category:      b.purpose.Category(),
		RetentionDays: p.Retention.Days(),
		ValidFrom:     p.ValidFrom,
		ValidUntil:    p.ValidUntil,
		Actor:         p.Actor,
		Reason:        strings.TrimSpace(p.Reason),
	})
	return b.events[len(b.events)-1:], nil
}

// Revoke отзывает действующее основание и эмитит processing-authority-changed.
// Причина обязательна (auditability). Повторный отзыв отвергается.
func (b *ProcessingBasis) Revoke(p RevokeParams) ([]DomainEvent, error) {
	if b.status != BasisActive {
		return nil, fmt.Errorf("%w: основание %s", ErrBasisRevoked, b.id)
	}
	if err := validateChangeInput(p.Actor, p.Reason, true, p.At, p.EventID); err != nil {
		return nil, err
	}

	b.status = BasisRevoked
	b.version++
	b.emit(ProcessingAuthorityChanged{
		EventHeader:   eventHeader(b.tenantID, p.At, p.EventID, p.CorrelationID, p.CausationID),
		BasisID:       b.id,
		Change:        AuthorityRevoked,
		Status:        b.status,
		Purpose:       b.purpose.Name(),
		Category:      b.purpose.Category(),
		RetentionDays: b.retention.Days(),
		ValidFrom:     b.validFrom,
		ValidUntil:    b.validUntil,
		Actor:         p.Actor,
		Reason:        strings.TrimSpace(p.Reason),
	})
	return b.events[len(b.events)-1:], nil
}

// SuppressContact вносит контакт в стоп-лист и эмитит contact-suppressed.
// Повторное внесение контакта с активной записью отвергается.
func (b *ProcessingBasis) SuppressContact(p SuppressEntryParams) ([]DomainEvent, error) {
	if err := validateChangeInput(p.Actor, p.Reason, true, p.At, p.EventID); err != nil {
		return nil, err
	}
	if entry, ok := b.IsSuppressed(p.Contact); ok {
		return nil, fmt.Errorf("%w: контакт %s (запись %s)", ErrAlreadySuppressed, entry.contact, entry.id)
	}
	entry, err := NewSuppressionEntry(SuppressParams{ID: p.ID, Contact: p.Contact, Reason: p.Reason, At: p.At})
	if err != nil {
		return nil, err
	}
	b.entries = append(b.entries, entry)
	b.version++
	b.emit(ContactSuppressed{
		EventHeader:   eventHeader(b.tenantID, p.At, p.EventID, p.CorrelationID, p.CausationID),
		BasisID:       b.id,
		SuppressionID: entry.id,
		Contact:       entry.contact,
		Action:        SuppressionApplied,
		Actor:         p.Actor,
		Reason:        entry.reason,
	})
	return b.events[len(b.events)-1:], nil
}

// LiftSuppression снимает активную запись стоп-листа и эмитит contact-suppressed.
// Запись не удаляется: она становится неактивной и сохраняет историю решения.
func (b *ProcessingBasis) LiftSuppression(p LiftParams) ([]DomainEvent, error) {
	if err := validateChangeInput(p.Actor, p.Reason, true, p.At, p.EventID); err != nil {
		return nil, err
	}
	var entry *SuppressionEntry
	for _, candidate := range b.entries {
		if candidate.id == p.EntryID {
			entry = candidate
			break
		}
	}
	if entry == nil {
		return nil, fmt.Errorf("%w: %s у основания %s", ErrSuppressionNotFound, p.EntryID, b.id)
	}
	if err := entry.lift(p.At); err != nil {
		return nil, err
	}
	b.version++
	b.emit(ContactSuppressed{
		EventHeader:   eventHeader(b.tenantID, p.At, p.EventID, p.CorrelationID, p.CausationID),
		BasisID:       b.id,
		SuppressionID: entry.id,
		Contact:       entry.contact,
		Action:        SuppressionLifted,
		Actor:         p.Actor,
		Reason:        strings.TrimSpace(p.Reason),
	})
	return b.events[len(b.events)-1:], nil
}

// DomainEvents возвращает копию неподтверждённых событий.
func (b *ProcessingBasis) DomainEvents() []DomainEvent {
	return append([]DomainEvent(nil), b.events...)
}

// PullDomainEvents возвращает неподтверждённые события и очищает буфер.
func (b *ProcessingBasis) PullDomainEvents() []DomainEvent {
	events := b.events
	b.events = nil
	return events
}

// emit добавляет событие в буфер агрегата.
func (b *ProcessingBasis) emit(event DomainEvent) {
	b.events = append(b.events, event)
}

// validateValidityWindow проверяет непротиворечивость окна действия: начало
// задано, конец задан и позже начала.
func validateValidityWindow(from, until time.Time) error {
	if from.IsZero() {
		return fmt.Errorf("%w: начало действия", ErrMissingTime)
	}
	if until.IsZero() {
		return fmt.Errorf("%w: конец действия", ErrMissingTime)
	}
	if !until.After(from) {
		return fmt.Errorf("%w: конец %s не позже начала %s", ErrInvalidValidityWindow, until, from)
	}
	return nil
}

// validateChangeInput проверяет общие поля изменения: актора, время,
// идентификатор события и — когда требуется — причину.
func validateChangeInput(actor ActorID, reason string, reasonRequired bool, at time.Time, eventID EventID) error {
	if err := actor.validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrMissingActor, err)
	}
	if at.IsZero() {
		return ErrMissingTime
	}
	if err := eventID.validate(); err != nil {
		return err
	}
	if reasonRequired && strings.TrimSpace(reason) == "" {
		return ErrReasonRequired
	}
	return nil
}
