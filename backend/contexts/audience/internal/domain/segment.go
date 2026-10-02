package domain

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Segment — aggregate root audience-segment: сегмент целевой аудитории,
// объединяющий оценки лидов (сущности lead-score) и состав сегмента.
//
// Инвариант: скоринг воспроизводим по версии модели. Версия модели неизменяема в
// пределах сегмента, поэтому все его оценки получены одним словарём и весами.
// Состав сегмента — производное от критериев и оценок: он меняется только по
// итогам скоринга, и каждое изменение порождает segment-membership-changed.
type Segment struct {
	id           SegmentID
	tenantID     TenantID
	name         string
	modelVersion ModelVersion
	criteria     SegmentCriteria

	scores  map[LeadID]LeadScore
	members map[LeadID]struct{}

	lastChangedAt time.Time
	version       int
	events        []DomainEvent
}

// NewSegmentParams — обязательные части нового сегмента.
type NewSegmentParams struct {
	ID           SegmentID
	TenantID     TenantID
	Name         string
	ModelVersion ModelVersion
	Criteria     SegmentCriteria
}

// NewSegment создаёт пустой сегмент. Создание сегмента не производит доменного
// события: каталог объявляет для audience-segment только изменение состава.
func NewSegment(params NewSegmentParams) (*Segment, error) {
	if err := params.ID.validate(); err != nil {
		return nil, err
	}
	if err := params.TenantID.validate(); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return nil, ErrEmptySegmentName
	}
	if err := params.ModelVersion.validate(); err != nil {
		return nil, err
	}
	if !KnownModelVersion(params.ModelVersion) {
		return nil, fmt.Errorf("%w: %q", ErrUnknownModelVersion, params.ModelVersion)
	}
	if err := params.Criteria.validate(); err != nil {
		return nil, err
	}
	return &Segment{
		id:           params.ID,
		tenantID:     params.TenantID,
		name:         name,
		modelVersion: params.ModelVersion,
		criteria:     params.Criteria,
		scores:       make(map[LeadID]LeadScore),
		members:      make(map[LeadID]struct{}),
		version:      1,
	}, nil
}

// ID возвращает идентификатор сегмента.
func (s *Segment) ID() SegmentID { return s.id }

// TenantID возвращает тенант-владелец сегмента.
func (s *Segment) TenantID() TenantID { return s.tenantID }

// Name возвращает имя сегмента.
func (s *Segment) Name() string { return s.name }

// ModelVersion возвращает версию модели скоринга сегмента.
func (s *Segment) ModelVersion() ModelVersion { return s.modelVersion }

// Criteria возвращает критерии сегмента.
func (s *Segment) Criteria() SegmentCriteria { return s.criteria }

// Version возвращает версию агрегата.
func (s *Segment) Version() int { return s.version }

// LastChangedAt возвращает момент последнего изменения состава сегмента. Нулевое
// значение означает, что состав ещё ни разу не менялся.
func (s *Segment) LastChangedAt() time.Time { return s.lastChangedAt }

// ScoreResult — итог скоринга одного лида.
type ScoreResult struct {
	LeadID            LeadID
	Value             ScoreValue
	Eligible          bool
	MembershipChanged bool
	Added             bool
	MemberCount       int
	Version           int
}

// ScoreLead оценивает лид и пересчитывает его принадлежность сегменту.
//
// Оценка вычисляется детерминированно по версии модели сегмента. Если
// принадлежность изменилась, агрегат увеличивает версию и порождает
// segment-membership-changed; повторный скоринг без изменения состава события не
// порождает. Тенант профиля обязан совпадать с тенантом сегмента.
func (s *Segment) ScoreLead(profile LeadProfile, provenance Provenance) (ScoreResult, error) {
	if err := profile.validate(); err != nil {
		return ScoreResult{}, err
	}
	if err := provenance.validate(); err != nil {
		return ScoreResult{}, err
	}
	if profile.TenantID != s.tenantID {
		return ScoreResult{}, fmt.Errorf("%w: профиль %s, сегмент %s", ErrTenantMismatch, profile.TenantID, s.tenantID)
	}

	value, err := scoreProfile(profile, s.modelVersion)
	if err != nil {
		return ScoreResult{}, err
	}
	score, err := newLeadScore(profile, s.modelVersion, value, provenance.At)
	if err != nil {
		return ScoreResult{}, err
	}

	_, wasMember := s.members[score.leadID]
	s.scores[score.leadID] = *score

	eligible := Eligible(s.criteria, score)
	changed := eligible != wasMember
	added := eligible && !wasMember
	if eligible {
		s.members[score.leadID] = struct{}{}
	} else {
		delete(s.members, score.leadID)
	}
	if changed {
		s.version++
		s.lastChangedAt = provenance.At
		s.emit(SegmentMembershipChanged{
			EventHeader:  provenance.header(s.tenantID),
			SegmentID:    s.id,
			LeadID:       score.leadID,
			Added:        added,
			Value:        score.value,
			ModelVersion: s.modelVersion,
			MemberCount:  len(s.members),
			Version:      s.version,
		})
	}

	return ScoreResult{
		LeadID:            score.leadID,
		Value:             value,
		Eligible:          eligible,
		MembershipChanged: changed,
		Added:             added,
		MemberCount:       len(s.members),
		Version:           s.version,
	}, nil
}

// LeadProfiles возвращает профили всех оценённых лидов в порядке идентификаторов.
// Нужны регулярному пересчёту сегментации без обращения к внешнему источнику.
func (s *Segment) LeadProfiles() []LeadProfile {
	ids := s.sortedLeadIDs()
	profiles := make([]LeadProfile, 0, len(ids))
	for _, id := range ids {
		score := s.scores[id]
		profiles = append(profiles, score.Profile(s.tenantID))
	}
	return profiles
}

// LeadScores возвращает копии оценок в порядке идентификаторов лидов.
func (s *Segment) LeadScores() []LeadScore {
	ids := s.sortedLeadIDs()
	scores := make([]LeadScore, 0, len(ids))
	for _, id := range ids {
		scores = append(scores, s.scores[id])
	}
	return scores
}

// Members возвращает идентификаторы лидов состава сегмента в порядке возрастания.
func (s *Segment) Members() []LeadID {
	members := make([]LeadID, 0, len(s.members))
	for id := range s.members {
		members = append(members, id)
	}
	sort.Slice(members, func(i, j int) bool { return members[i] < members[j] })
	return members
}

// IsMember сообщает, входит ли лид в состав сегмента.
func (s *Segment) IsMember(id LeadID) bool {
	_, ok := s.members[id]
	return ok
}

// sortedLeadIDs возвращает идентификаторы оценённых лидов в порядке возрастания.
func (s *Segment) sortedLeadIDs() []LeadID {
	ids := make([]LeadID, 0, len(s.scores))
	for id := range s.scores {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// DomainEvents возвращает копию неподтверждённых событий.
func (s *Segment) DomainEvents() []DomainEvent {
	return append([]DomainEvent(nil), s.events...)
}

// PullDomainEvents возвращает неподтверждённые события и очищает буфер.
func (s *Segment) PullDomainEvents() []DomainEvent {
	events := s.events
	s.events = nil
	return events
}

// emit добавляет событие в буфер агрегата.
func (s *Segment) emit(event DomainEvent) {
	s.events = append(s.events, event)
}

// Проверка соответствия контракту производителя событий на этапе компиляции.
var _ EventProducer = (*Segment)(nil)
