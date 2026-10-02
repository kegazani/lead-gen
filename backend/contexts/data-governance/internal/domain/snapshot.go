package domain

import (
	"fmt"
	"time"
)

// Сохранённое состояние агрегата и его сущностей.
//
// Адаптер репозитория не имеет доступа к закрытым полям агрегата, поэтому он
// читает и пишет ProcessingBasisSnapshot с экспортированными полями. Доменный
// объект из снимка собирается только через RehydrateProcessingBasis, который
// проверяет инварианты и не эмитит события: восстановление не является новым
// фактом домена.

// SuppressionEntrySnapshot — сохранённое состояние записи стоп-листа.
type SuppressionEntrySnapshot struct {
	ID        SuppressionID
	Contact   ContactRef
	Reason    string
	Active    bool
	CreatedAt time.Time
	RevokedAt time.Time
}

// ProcessingBasisSnapshot — полное сохранённое состояние aggregate root
// processing-basis.
type ProcessingBasisSnapshot struct {
	ID          BasisID
	TenantID    TenantID
	PurposeName string
	Category    DataCategory
	Retention   int
	ValidFrom   time.Time
	ValidUntil  time.Time
	Status      BasisStatus
	Entries     []SuppressionEntrySnapshot
	Version     int
}

// Snapshot возвращает состояние агрегата для сохранения.
func (b *ProcessingBasis) Snapshot() ProcessingBasisSnapshot {
	entries := make([]SuppressionEntrySnapshot, 0, len(b.entries))
	for _, entry := range b.entries {
		entries = append(entries, entry.Snapshot())
	}
	return ProcessingBasisSnapshot{
		ID:          b.id,
		TenantID:    b.tenantID,
		PurposeName: b.purpose.Name(),
		Category:    b.purpose.Category(),
		Retention:   b.retention.Days(),
		ValidFrom:   b.validFrom,
		ValidUntil:  b.validUntil,
		Status:      b.status,
		Entries:     entries,
		Version:     b.version,
	}
}

// RehydrateProcessingBasis восстанавливает aggregate root из сохранённого
// состояния, проверяя инварианты: известность статуса и категории, непустые
// идентификаторы и цель, положительный срок хранения и версию, непротиворечивое
// окно действия и отсутствие двух активных записей стоп-листа для одного
// контакта. События не эмитятся.
func RehydrateProcessingBasis(s ProcessingBasisSnapshot) (*ProcessingBasis, error) {
	if err := s.ID.validate(); err != nil {
		return nil, err
	}
	if err := s.TenantID.validate(); err != nil {
		return nil, err
	}
	purpose, err := NewProcessingPurpose(s.PurposeName, s.Category)
	if err != nil {
		return nil, fmt.Errorf("%w: основание %s: %v", ErrInvalidSnapshot, s.ID, err)
	}
	retention, err := NewRetentionPeriod(s.Retention)
	if err != nil {
		return nil, fmt.Errorf("%w: основание %s: %v", ErrInvalidSnapshot, s.ID, err)
	}
	if err := validateValidityWindow(s.ValidFrom, s.ValidUntil); err != nil {
		return nil, fmt.Errorf("%w: основание %s: %v", ErrInvalidSnapshot, s.ID, err)
	}
	if !s.Status.isValid() {
		return nil, fmt.Errorf("%w: основание %s: неизвестный статус %q", ErrInvalidSnapshot, s.ID, s.Status)
	}
	if s.Version <= 0 {
		return nil, fmt.Errorf("%w: основание %s версии %d", ErrInvalidSnapshot, s.ID, s.Version)
	}

	entries := make([]*SuppressionEntry, 0, len(s.Entries))
	activeByContact := make(map[ContactRef]struct{}, len(s.Entries))
	for _, snapshot := range s.Entries {
		entry, err := rehydrateSuppressionEntry(snapshot)
		if err != nil {
			return nil, fmt.Errorf("%w: основание %s: %v", ErrInvalidSnapshot, s.ID, err)
		}
		if entry.active {
			if _, duplicate := activeByContact[entry.contact]; duplicate {
				return nil, fmt.Errorf(
					"%w: основание %s: две активные записи стоп-листа для контакта %s",
					ErrInvalidSnapshot, s.ID, entry.contact,
				)
			}
			activeByContact[entry.contact] = struct{}{}
		}
		entries = append(entries, entry)
	}

	return &ProcessingBasis{
		id:         s.ID,
		tenantID:   s.TenantID,
		purpose:    purpose,
		retention:  retention,
		validFrom:  s.ValidFrom,
		validUntil: s.ValidUntil,
		status:     s.Status,
		entries:    entries,
		version:    s.Version,
	}, nil
}
