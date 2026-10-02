package domain

import (
	"fmt"
	"strings"
	"time"
)

// SuppressionEntry — entity внутри aggregate processing-basis: запись стоп-листа
// для одного контакта.
//
// Активная запись имеет приоритет над кампанией, решением оператора и
// поставщиком (инвариант I2). Снятие записи не удаляет её: запись становится
// неактивной и сохраняет историю решения (auditability), поэтому повторное
// внесение того же контакта создаёт новую запись, а не воскрешает старую.
type SuppressionEntry struct {
	id        SuppressionID
	contact   ContactRef
	reason    string
	active    bool
	createdAt time.Time
	revokedAt time.Time
}

// SuppressParams — вход создания записи стоп-листа.
type SuppressParams struct {
	ID      SuppressionID
	Contact ContactRef
	Reason  string
	At      time.Time
}

// NewSuppressionEntry создаёт активную запись стоп-листа. Контакт и причина
// обязательны.
func NewSuppressionEntry(p SuppressParams) (*SuppressionEntry, error) {
	if err := p.ID.validate(); err != nil {
		return nil, err
	}
	if err := p.Contact.validate(); err != nil {
		return nil, err
	}
	reason := strings.TrimSpace(p.Reason)
	if reason == "" {
		return nil, fmt.Errorf("%w: запись стоп-листа %s", ErrReasonRequired, p.ID)
	}
	if p.At.IsZero() {
		return nil, fmt.Errorf("%w: запись стоп-листа %s", ErrMissingTime, p.ID)
	}
	return &SuppressionEntry{
		id:        p.ID,
		contact:   p.Contact,
		reason:    reason,
		active:    true,
		createdAt: p.At,
	}, nil
}

// ID возвращает идентификатор записи.
func (e *SuppressionEntry) ID() SuppressionID { return e.id }

// Contact возвращает подавленный контакт.
func (e *SuppressionEntry) Contact() ContactRef { return e.contact }

// Reason возвращает причину внесения в стоп-лист.
func (e *SuppressionEntry) Reason() string { return e.reason }

// IsActive сообщает, действует ли запись.
func (e *SuppressionEntry) IsActive() bool { return e.active }

// CreatedAt возвращает момент внесения в стоп-лист.
func (e *SuppressionEntry) CreatedAt() time.Time { return e.createdAt }

// RevokedAt возвращает момент снятия записи (нулевое время — запись активна).
func (e *SuppressionEntry) RevokedAt() time.Time { return e.revokedAt }

// lift снимает активную запись. Повторное снятие отвергается.
func (e *SuppressionEntry) lift(at time.Time) error {
	if !e.active {
		return fmt.Errorf("%w: запись %s", ErrSuppressionNotActive, e.id)
	}
	if at.IsZero() {
		return fmt.Errorf("%w: снятие записи %s", ErrMissingTime, e.id)
	}
	e.active = false
	e.revokedAt = at
	return nil
}

// Snapshot возвращает состояние записи для сохранения.
func (e *SuppressionEntry) Snapshot() SuppressionEntrySnapshot {
	return SuppressionEntrySnapshot{
		ID:        e.id,
		Contact:   e.contact,
		Reason:    e.reason,
		Active:    e.active,
		CreatedAt: e.createdAt,
		RevokedAt: e.revokedAt,
	}
}

// rehydrateSuppressionEntry восстанавливает запись из снимка, проверяя
// инварианты. События не эмитятся: восстановление — не новый факт домена.
func rehydrateSuppressionEntry(s SuppressionEntrySnapshot) (*SuppressionEntry, error) {
	if err := s.ID.validate(); err != nil {
		return nil, err
	}
	if err := s.Contact.validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(s.Reason) == "" {
		return nil, fmt.Errorf("%w: запись стоп-листа %s без причины", ErrInvalidSnapshot, s.ID)
	}
	if s.CreatedAt.IsZero() {
		return nil, fmt.Errorf("%w: запись стоп-листа %s без времени создания", ErrInvalidSnapshot, s.ID)
	}
	if s.Active && !s.RevokedAt.IsZero() {
		return nil, fmt.Errorf("%w: активная запись %s имеет время снятия", ErrInvalidSnapshot, s.ID)
	}
	if !s.Active && s.RevokedAt.IsZero() {
		return nil, fmt.Errorf("%w: неактивная запись %s без времени снятия", ErrInvalidSnapshot, s.ID)
	}
	return &SuppressionEntry{
		id:        s.ID,
		contact:   s.Contact,
		reason:    s.Reason,
		active:    s.Active,
		createdAt: s.CreatedAt,
		revokedAt: s.RevokedAt,
	}, nil
}
