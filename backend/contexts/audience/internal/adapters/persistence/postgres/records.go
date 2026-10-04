package postgres

import (
	"fmt"
	"time"

	"lead-gen/backend/contexts/audience/internal/domain"
)

// Записываемые формы снимков агрегатов.
//
// Домен хранит value objects непрозрачными (segment-criteria, score-value,
// signal, snapshot-criteria: поля не экспортируются), поэтому снимок нельзя
// сериализовать в JSONB напрямую — encoding/json оставил бы их пустыми. Эти
// структуры раскладывают value objects на примитивы через экспортированные
// аксессоры домена, а обратный перевод собирает их через экспортированные
// конструкторы. Граница сериализации остаётся в адаптере и не проникает в домен.

// criteriaRecord — записываемая форма VO segment-criteria.
type criteriaRecord struct {
	MinScore        float64  `json:"min_score"`
	RequiredSignals []string `json:"required_signals"`
	RequireAll      bool     `json:"require_all"`
}

// signalRecord — записываемая форма VO signal.
type signalRecord struct {
	Name     string  `json:"name"`
	Strength float64 `json:"strength"`
}

// leadScoreRecord — записываемая форма сущности lead-score.
type leadScoreRecord struct {
	LeadID       string         `json:"lead_id"`
	Value        float64        `json:"value"`
	ModelVersion string         `json:"model_version"`
	Signals      []signalRecord `json:"signals"`
	ComputedAt   time.Time      `json:"computed_at"`
}

// segmentRecord — записываемая форма domain.SegmentSnapshot.
type segmentRecord struct {
	ID            string            `json:"id"`
	TenantID      string            `json:"tenant_id"`
	Name          string            `json:"name"`
	ModelVersion  string            `json:"model_version"`
	Criteria      criteriaRecord    `json:"criteria"`
	Scores        []leadScoreRecord `json:"scores"`
	Members       []string          `json:"members"`
	LastChangedAt time.Time         `json:"last_changed_at"`
	Version       int               `json:"version"`
}

// snapshotCriteriaRecord — записываемая форма VO snapshot-criteria.
type snapshotCriteriaRecord struct {
	SegmentID      string         `json:"segment_id"`
	SegmentVersion int            `json:"segment_version"`
	ModelVersion   string         `json:"model_version"`
	Criteria       criteriaRecord `json:"criteria"`
}

// audienceSnapshotRecord — записываемая форма domain.AudienceSnapshotState.
type audienceSnapshotRecord struct {
	ID          string                 `json:"id"`
	TenantID    string                 `json:"tenant_id"`
	Criteria    snapshotCriteriaRecord `json:"criteria"`
	Members     []string               `json:"members"`
	Version     int64                  `json:"version"`
	GeneratedAt time.Time              `json:"generated_at"`
}

// toSegmentRecord переводит снимок сегмента в записываемую форму.
func toSegmentRecord(s domain.SegmentSnapshot) segmentRecord {
	scores := make([]leadScoreRecord, 0, len(s.Scores))
	for _, ls := range s.Scores {
		signals := make([]signalRecord, 0, len(ls.Signals))
		for _, sg := range ls.Signals {
			signals = append(signals, signalRecord{Name: sg.Name(), Strength: sg.Strength()})
		}
		scores = append(scores, leadScoreRecord{
			LeadID:       ls.LeadID.String(),
			Value:        ls.Value.Float64(),
			ModelVersion: ls.ModelVersion.String(),
			Signals:      signals,
			ComputedAt:   ls.ComputedAt,
		})
	}
	members := make([]string, 0, len(s.Members))
	for _, id := range s.Members {
		members = append(members, id.String())
	}
	return segmentRecord{
		ID:            s.ID.String(),
		TenantID:      s.TenantID.String(),
		Name:          s.Name,
		ModelVersion:  s.ModelVersion.String(),
		Criteria:      toCriteriaRecord(s.Criteria),
		Scores:        scores,
		Members:       members,
		LastChangedAt: s.LastChangedAt,
		Version:       s.Version,
	}
}

// fromSegmentRecord собирает снимок сегмента из записываемой формы. Ошибку даёт
// только некорректное сохранённое значение (например, испорченный JSONB): она
// означает, что сохранить сегмент в таком виде было нельзя.
func fromSegmentRecord(r segmentRecord) (domain.SegmentSnapshot, error) {
	criteria, err := fromCriteriaRecord(r.Criteria)
	if err != nil {
		return domain.SegmentSnapshot{}, fmt.Errorf("критерии сегмента %s: %w", r.ID, err)
	}
	scores := make([]domain.LeadScoreSnapshot, 0, len(r.Scores))
	for _, ls := range r.Scores {
		value, err := domain.NewScoreValue(ls.Value)
		if err != nil {
			return domain.SegmentSnapshot{}, fmt.Errorf("оценка лида %s: %w", ls.LeadID, err)
		}
		signals := make([]domain.Signal, 0, len(ls.Signals))
		for _, sg := range ls.Signals {
			signal, err := domain.NewSignal(sg.Name, sg.Strength)
			if err != nil {
				return domain.SegmentSnapshot{}, fmt.Errorf("сигнал лида %s: %w", ls.LeadID, err)
			}
			signals = append(signals, signal)
		}
		scores = append(scores, domain.LeadScoreSnapshot{
			LeadID:       domain.LeadID(ls.LeadID),
			Value:        value,
			ModelVersion: domain.ModelVersion(ls.ModelVersion),
			Signals:      signals,
			ComputedAt:   ls.ComputedAt,
		})
	}
	members := make([]domain.LeadID, 0, len(r.Members))
	for _, id := range r.Members {
		members = append(members, domain.LeadID(id))
	}
	return domain.SegmentSnapshot{
		ID:            domain.SegmentID(r.ID),
		TenantID:      domain.TenantID(r.TenantID),
		Name:          r.Name,
		ModelVersion:  domain.ModelVersion(r.ModelVersion),
		Criteria:      criteria,
		Scores:        scores,
		Members:       members,
		LastChangedAt: r.LastChangedAt,
		Version:       r.Version,
	}, nil
}

// toCriteriaRecord переводит критерии сегмента в записываемую форму.
func toCriteriaRecord(c domain.SegmentCriteria) criteriaRecord {
	return criteriaRecord{
		MinScore:        c.MinScore().Float64(),
		RequiredSignals: c.RequiredSignals(),
		RequireAll:      c.RequireAll(),
	}
}

// fromCriteriaRecord собирает критерии сегмента из записываемой формы.
func fromCriteriaRecord(r criteriaRecord) (domain.SegmentCriteria, error) {
	minScore, err := domain.NewScoreValue(r.MinScore)
	if err != nil {
		return domain.SegmentCriteria{}, err
	}
	return domain.NewSegmentCriteria(minScore, r.RequiredSignals, r.RequireAll)
}

// toAudienceSnapshotRecord переводит снимок аудитории в записываемую форму.
func toAudienceSnapshotRecord(s domain.AudienceSnapshotState) audienceSnapshotRecord {
	members := make([]string, 0, len(s.Members))
	for _, id := range s.Members {
		members = append(members, id.String())
	}
	c := s.Criteria
	return audienceSnapshotRecord{
		ID:       s.ID.String(),
		TenantID: s.TenantID.String(),
		Criteria: snapshotCriteriaRecord{
			SegmentID:      c.SegmentID().String(),
			SegmentVersion: c.SegmentVersion(),
			ModelVersion:   c.ModelVersion().String(),
			Criteria:       toCriteriaRecord(c.Criteria()),
		},
		Members:     members,
		Version:     s.Version,
		GeneratedAt: s.GeneratedAt,
	}
}

// fromAudienceSnapshotRecord собирает снимок аудитории из записываемой формы.
// Выражение снимка восстанавливается из сохранённых частей, а не выводится из
// живого сегмента: сегмент с тех пор мог измениться.
func fromAudienceSnapshotRecord(r audienceSnapshotRecord) (domain.AudienceSnapshotState, error) {
	criteria, err := fromCriteriaRecord(r.Criteria.Criteria)
	if err != nil {
		return domain.AudienceSnapshotState{}, fmt.Errorf("критерии снимка %s: %w", r.ID, err)
	}
	members := make([]domain.LeadID, 0, len(r.Members))
	for _, id := range r.Members {
		members = append(members, domain.LeadID(id))
	}
	return domain.AudienceSnapshotState{
		ID:       domain.SnapshotID(r.ID),
		TenantID: domain.TenantID(r.TenantID),
		Criteria: domain.RehydrateSnapshotCriteria(
			domain.SegmentID(r.Criteria.SegmentID),
			r.Criteria.SegmentVersion,
			domain.ModelVersion(r.Criteria.ModelVersion),
			criteria,
		),
		Members:     members,
		Version:     r.Version,
		GeneratedAt: r.GeneratedAt,
	}, nil
}
