package domain

import (
	"fmt"
	"sort"
	"time"
)

// Сохранённое состояние агрегатов audience.
//
// Адаптер репозитория не имеет доступа к закрытым полям агрегата, поэтому он
// читает и пишет снимки состояния с экспортированными полями. Доменный объект из
// снимка собирается только через Rehydrate*, который проверяет инварианты и не
// эмитит события: восстановление не является новым фактом домена.

// LeadScoreSnapshot — сохранённое состояние сущности lead-score.
type LeadScoreSnapshot struct {
	LeadID       LeadID
	Value        ScoreValue
	ModelVersion ModelVersion
	Signals      []Signal
	ComputedAt   time.Time
}

// SegmentSnapshot — полное сохранённое состояние aggregate root segment: оценки
// и явный состав. Состав проверяется на согласованность с оценками и критериями.
type SegmentSnapshot struct {
	ID            SegmentID
	TenantID      TenantID
	Name          string
	ModelVersion  ModelVersion
	Criteria      SegmentCriteria
	Scores        []LeadScoreSnapshot
	Members       []LeadID
	LastChangedAt time.Time
	Version       int
}

// AudienceSnapshotState — сохранённое состояние aggregate root audience-snapshot.
// Снимок по определению зафиксирован: восстановление всегда даёт frozen=true.
type AudienceSnapshotState struct {
	ID          SnapshotID
	TenantID    TenantID
	Criteria    SnapshotCriteria
	Members     []LeadID
	Version     int64
	GeneratedAt time.Time
}

// Snapshot возвращает состояние сегмента для сохранения.
func (s *Segment) Snapshot() SegmentSnapshot {
	ids := s.sortedLeadIDs()
	scores := make([]LeadScoreSnapshot, 0, len(ids))
	for _, id := range ids {
		score := s.scores[id]
		scores = append(scores, LeadScoreSnapshot{
			LeadID:       score.leadID,
			Value:        score.value,
			ModelVersion: score.modelVersion,
			Signals:      append([]Signal(nil), score.signals...),
			ComputedAt:   score.computedAt,
		})
	}
	return SegmentSnapshot{
		ID:            s.id,
		TenantID:      s.tenantID,
		Name:          s.name,
		ModelVersion:  s.modelVersion,
		Criteria:      s.criteria,
		Scores:        scores,
		Members:       s.Members(),
		LastChangedAt: s.lastChangedAt,
		Version:       s.version,
	}
}

// State возвращает состояние снимка аудитории для сохранения.
func (s *AudienceSnapshot) State() AudienceSnapshotState {
	return AudienceSnapshotState{
		ID:          s.id,
		TenantID:    s.tenantID,
		Criteria:    s.criteria,
		Members:     append([]LeadID(nil), s.members...),
		Version:     s.version,
		GeneratedAt: s.generatedAt,
	}
}

// RehydrateSegment восстанавливает aggregate root segment из сохранённого
// состояния, проверяя инварианты: известность версии модели, корректность
// критериев и оценок, согласованность явного состава с выводимым по политике.
// События не эмитятся.
func RehydrateSegment(s SegmentSnapshot) (*Segment, error) {
	if err := s.ID.validate(); err != nil {
		return nil, err
	}
	if err := s.TenantID.validate(); err != nil {
		return nil, err
	}
	if s.Name == "" {
		return nil, ErrEmptySegmentName
	}
	if err := s.ModelVersion.validate(); err != nil {
		return nil, err
	}
	if !KnownModelVersion(s.ModelVersion) {
		return nil, fmt.Errorf("%w: %q", ErrUnknownModelVersion, s.ModelVersion)
	}
	if err := s.Criteria.validate(); err != nil {
		return nil, err
	}
	if s.Version <= 0 {
		return nil, fmt.Errorf("%w: сегмент %s версии %d", ErrInvalidSnapshot, s.ID, s.Version)
	}

	segment := &Segment{
		id:            s.ID,
		tenantID:      s.TenantID,
		name:          s.Name,
		modelVersion:  s.ModelVersion,
		criteria:      s.Criteria,
		scores:        make(map[LeadID]LeadScore, len(s.Scores)),
		members:       make(map[LeadID]struct{}, len(s.Members)),
		lastChangedAt: s.LastChangedAt,
		version:       s.Version,
	}

	for _, ls := range s.Scores {
		score, err := rehydrateLeadScore(ls)
		if err != nil {
			return nil, fmt.Errorf("%w: сегмент %s: %v", ErrInvalidSnapshot, s.ID, err)
		}
		if score.modelVersion != s.ModelVersion {
			return nil, fmt.Errorf(
				"%w: сегмент %s: оценка лида %s по модели %q, а сегмент по %q",
				ErrInvalidSnapshot, s.ID, score.leadID, score.modelVersion, s.ModelVersion,
			)
		}
		if _, exists := segment.scores[score.leadID]; exists {
			return nil, fmt.Errorf("%w: сегмент %s: дубль оценки лида %s", ErrInvalidSnapshot, s.ID, score.leadID)
		}
		segment.scores[score.leadID] = *score
	}

	members := append([]LeadID(nil), s.Members...)
	sort.Slice(members, func(i, j int) bool { return members[i] < members[j] })
	for i, id := range members {
		if err := id.validate(); err != nil {
			return nil, fmt.Errorf("%w: сегмент %s: %v", ErrInvalidSnapshot, s.ID, err)
		}
		if i > 0 && members[i-1] == id {
			return nil, fmt.Errorf("%w: сегмент %s: дубль в составе %s", ErrInvalidSnapshot, s.ID, id)
		}
		score, ok := segment.scores[id]
		if !ok {
			return nil, fmt.Errorf("%w: сегмент %s: состав ссылается на неоценённого лида %s", ErrInvalidSnapshot, s.ID, id)
		}
		if !Eligible(s.Criteria, &score) {
			return nil, fmt.Errorf("%w: сегмент %s: лид %s в составе не проходит политику", ErrInvalidSnapshot, s.ID, id)
		}
		segment.members[id] = struct{}{}
	}
	// Оценённый и проходящий политику лид обязан быть в составе: состав — не
	// независимое множество, а вывод из оценок и критериев.
	for id, score := range segment.scores {
		if !Eligible(s.Criteria, &score) {
			continue
		}
		if _, ok := segment.members[id]; !ok {
			return nil, fmt.Errorf("%w: сегмент %s: лид %s проходит политику вне состава", ErrInvalidSnapshot, s.ID, id)
		}
	}
	return segment, nil
}

// rehydrateLeadScore проверяет и восстанавливает оценку лида.
func rehydrateLeadScore(s LeadScoreSnapshot) (*LeadScore, error) {
	if err := s.LeadID.validate(); err != nil {
		return nil, err
	}
	if s.Value.value < 0 || s.Value.value > 1 {
		return nil, fmt.Errorf("%w: оценка лида %s = %s", ErrInvalidScore, s.LeadID, s.Value)
	}
	if err := s.ModelVersion.validate(); err != nil {
		return nil, err
	}
	if s.ComputedAt.IsZero() {
		return nil, ErrMissingTime
	}
	if len(s.Signals) == 0 {
		return nil, ErrNoSignals
	}
	seen := make(map[string]struct{}, len(s.Signals))
	for _, signal := range s.Signals {
		if err := signal.validate(); err != nil {
			return nil, err
		}
		if _, ok := seen[signal.name]; ok {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateSignal, signal.name)
		}
		seen[signal.name] = struct{}{}
	}
	return &LeadScore{
		leadID:       s.LeadID,
		value:        s.Value,
		modelVersion: s.ModelVersion,
		signals:      append([]Signal(nil), s.Signals...),
		computedAt:   s.ComputedAt,
	}, nil
}

// RehydrateAudienceSnapshot восстанавливает aggregate root audience-snapshot из
// сохранённого состояния, проверяя инварианты: корректность зафиксированного
// выражения, положительную версию, наличие времени фиксации и уникальность
// состава. Снимок всегда восстанавливается как зафиксированный. События не
// эмитятся.
func RehydrateAudienceSnapshot(s AudienceSnapshotState) (*AudienceSnapshot, error) {
	if err := s.ID.validate(); err != nil {
		return nil, err
	}
	if err := s.TenantID.validate(); err != nil {
		return nil, err
	}
	if err := s.Criteria.validate(); err != nil {
		return nil, err
	}
	if s.Version <= 0 {
		return nil, fmt.Errorf("%w: снимок %s версии %d", ErrInvalidSnapshot, s.ID, s.Version)
	}
	if s.GeneratedAt.IsZero() {
		return nil, fmt.Errorf("%w: снимок %s без времени фиксации", ErrInvalidSnapshot, s.ID)
	}

	members := append([]LeadID(nil), s.Members...)
	sort.Slice(members, func(i, j int) bool { return members[i] < members[j] })
	for i, id := range members {
		if err := id.validate(); err != nil {
			return nil, fmt.Errorf("%w: снимок %s: %v", ErrInvalidSnapshot, s.ID, err)
		}
		if i > 0 && members[i-1] == id {
			return nil, fmt.Errorf("%w: снимок %s: дубль в составе %s", ErrInvalidSnapshot, s.ID, id)
		}
	}

	return &AudienceSnapshot{
		id:          s.ID,
		tenantID:    s.TenantID,
		criteria:    s.Criteria,
		members:     members,
		version:     s.Version,
		generatedAt: s.GeneratedAt,
		frozen:      true,
	}, nil
}
