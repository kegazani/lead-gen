package domain

import (
	"fmt"
	"time"
)

// AudienceSnapshot — aggregate root audience-snapshot: зафиксированный состав
// сегмента, на который ссылается запущенная кампания.
//
// Инварианты: после фиксации снимок неизменяем, а запущенная кампания ссылается
// на снимок, а не на живой сегмент. Первый обеспечен отсутствием методов-мутаторов
// и признаком frozen; второй — межконтекстный и проверяется потребителем
// (outreach-orchestration), который получает снимок по контракту audience-snapshot.
//
// Снимок хранит только ссылки на лидов: персональные данные через границу
// контекста не проходят, состав разрешается на стороне владельца.
type AudienceSnapshot struct {
	id          SnapshotID
	tenantID    TenantID
	criteria    SnapshotCriteria
	members     []LeadID
	version     int64
	generatedAt time.Time
	frozen      bool
	events      []DomainEvent
}

// NewAudienceSnapshotParams — обязательные части фиксируемого снимка.
type NewAudienceSnapshotParams struct {
	ID         SnapshotID
	TenantID   TenantID
	Provenance Provenance
}

// FixAudienceSnapshot фиксирует состав сегмента в неизменяемый снимок.
//
// Снимок получает критерии, версию сегмента и модель на момент фиксации, а также
// копию состава. Тенант снимка обязан совпадать с тенантом сегмента. Фиксация
// порождает audience-snapshot-created; повторная фиксация того же состава создаёт
// новый снимок с новой версией, а не изменяет прежний.
func FixAudienceSnapshot(segment *Segment, params NewAudienceSnapshotParams) (*AudienceSnapshot, error) {
	if segment == nil {
		return nil, fmt.Errorf("%w: сегмент не задан", ErrInvalidSnapshot)
	}
	if err := params.ID.validate(); err != nil {
		return nil, err
	}
	if err := params.TenantID.validate(); err != nil {
		return nil, err
	}
	if err := params.Provenance.validate(); err != nil {
		return nil, err
	}
	if params.TenantID != segment.tenantID {
		return nil, fmt.Errorf("%w: снимок %s, сегмент %s", ErrTenantMismatch, params.TenantID, segment.tenantID)
	}

	members := segment.Members()
	snapshot := &AudienceSnapshot{
		id:          params.ID,
		tenantID:    segment.tenantID,
		criteria:    newSnapshotCriteria(segment),
		members:     members,
		version:     1,
		generatedAt: params.Provenance.At,
		frozen:      true,
	}
	snapshot.events = append(snapshot.events, AudienceSnapshotCreated{
		EventHeader:    params.Provenance.header(segment.tenantID),
		SnapshotID:     snapshot.id,
		SegmentID:      snapshot.criteria.segmentID,
		SegmentVersion: snapshot.criteria.segmentVersion,
		ModelVersion:   snapshot.criteria.modelVersion,
		MemberCount:    len(snapshot.members),
		Version:        snapshot.version,
		GeneratedAt:    snapshot.generatedAt,
	})
	return snapshot, nil
}

// ID возвращает идентификатор снимка.
func (s *AudienceSnapshot) ID() SnapshotID { return s.id }

// TenantID возвращает тенант-владелец снимка.
func (s *AudienceSnapshot) TenantID() TenantID { return s.tenantID }

// SegmentID возвращает сегмент, из которого построен снимок.
func (s *AudienceSnapshot) SegmentID() SegmentID { return s.criteria.segmentID }

// SegmentVersion возвращает версию сегмента на момент фиксации.
func (s *AudienceSnapshot) SegmentVersion() int { return s.criteria.segmentVersion }

// ModelVersion возвращает версию модели скоринга снимка.
func (s *AudienceSnapshot) ModelVersion() ModelVersion { return s.criteria.modelVersion }

// Criteria возвращает зафиксированное выражение условий.
func (s *AudienceSnapshot) Criteria() SnapshotCriteria { return s.criteria }

// MemberIDs возвращает копию ссылок на лидов состава снимка.
func (s *AudienceSnapshot) MemberIDs() []LeadID {
	return append([]LeadID(nil), s.members...)
}

// MemberCount возвращает размер снимка.
func (s *AudienceSnapshot) MemberCount() int { return len(s.members) }

// Version возвращает версию снимка.
func (s *AudienceSnapshot) Version() int64 { return s.version }

// GeneratedAt возвращает момент фиксации снимка.
func (s *AudienceSnapshot) GeneratedAt() time.Time { return s.generatedAt }

// Frozen сообщает, что снимок зафиксирован и неизменяем.
func (s *AudienceSnapshot) Frozen() bool { return s.frozen }

// DomainEvents возвращает копию неподтверждённых событий.
func (s *AudienceSnapshot) DomainEvents() []DomainEvent {
	return append([]DomainEvent(nil), s.events...)
}

// PullDomainEvents возвращает неподтверждённые события и очищает буфер.
func (s *AudienceSnapshot) PullDomainEvents() []DomainEvent {
	events := s.events
	s.events = nil
	return events
}

// Проверка соответствия контракту производителя событий на этапе компиляции.
var _ EventProducer = (*AudienceSnapshot)(nil)
