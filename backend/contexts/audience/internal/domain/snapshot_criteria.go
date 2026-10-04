package domain

import "fmt"

// SnapshotCriteria — неизменяемое выражение условий, зафиксированное в снимке
// аудитории (VO snapshot-criteria): критерии сегмента вместе с версией сегмента и
// версией модели, по которым снимок построен. Снимок воспроизводим: по нему
// видно, какое именно выражение и какая модель дали состав.
type SnapshotCriteria struct {
	segmentID      SegmentID
	segmentVersion int
	modelVersion   ModelVersion
	criteria       SegmentCriteria
}

// newSnapshotCriteria фиксирует выражение снимка из состояния сегмента.
func newSnapshotCriteria(segment *Segment) SnapshotCriteria {
	return SnapshotCriteria{
		segmentID:      segment.id,
		segmentVersion: segment.version,
		modelVersion:   segment.modelVersion,
		criteria:       segment.criteria,
	}
}

// RehydrateSnapshotCriteria собирает зафиксированное выражение снимка из
// сохранённых частей. В отличие от newSnapshotCriteria, не выводит выражение из
// живого сегмента, а восстанавливает уже зафиксированное — сегмент мог с тех пор
// измениться, поэтому снимок нельзя пересобрать по нему. Согласованность частей
// проверяет вызывающий (RehydrateAudienceSnapshot) — восстановление не эмитит
// событий.
func RehydrateSnapshotCriteria(segmentID SegmentID, segmentVersion int, modelVersion ModelVersion, criteria SegmentCriteria) SnapshotCriteria {
	return SnapshotCriteria{
		segmentID:      segmentID,
		segmentVersion: segmentVersion,
		modelVersion:   modelVersion,
		criteria:       criteria,
	}
}

// SegmentID возвращает сегмент, выражение которого зафиксировано.
func (c SnapshotCriteria) SegmentID() SegmentID { return c.segmentID }

// SegmentVersion возвращает версию сегмента на момент фиксации.
func (c SnapshotCriteria) SegmentVersion() int { return c.segmentVersion }

// ModelVersion возвращает версию модели, по которой получен состав.
func (c SnapshotCriteria) ModelVersion() ModelVersion { return c.modelVersion }

// Criteria возвращает выражение условий сегмента.
func (c SnapshotCriteria) Criteria() SegmentCriteria { return c.criteria }

// validate проверяет внутреннюю согласованность зафиксированного выражения.
func (c SnapshotCriteria) validate() error {
	if err := c.segmentID.validate(); err != nil {
		return err
	}
	if c.segmentVersion <= 0 {
		return fmt.Errorf("%w: версия сегмента %d", ErrInvalidCriteria, c.segmentVersion)
	}
	if err := c.modelVersion.validate(); err != nil {
		return err
	}
	return c.criteria.validate()
}
