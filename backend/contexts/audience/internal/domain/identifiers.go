package domain

import (
	"fmt"
	"strings"
)

// Идентификаторы и версии домена audience.
//
// Каждый идентификатор — отдельный именованный тип, чтобы сегмент нельзя было
// передать вместо снимка, а лида — вместо тенанта. Значения неизменяемы.
type (
	// TenantID — идентификатор тенанта-владельца данных.
	TenantID string

	// ActorID — идентификатор актора, инициировавшего операцию.
	ActorID string

	// SegmentID — идентификатор aggregate root segment.
	SegmentID string

	// LeadID — идентификатор лида (владение lead-management). Внутри контекста
	// audience это ссылка: аудитория хранит свои оценки, а не редактирует лид.
	LeadID string

	// SnapshotID — идентификатор aggregate root audience-snapshot.
	SnapshotID string

	// ModelVersion — версия модели скоринга. Инвариант audience-segment требует
	// воспроизводимости оценки по версии модели, поэтому она входит в оценку.
	ModelVersion string

	// CorrelationID — идентификатор сквозного сценария, к которому относится
	// событие.
	CorrelationID string

	// CausationID — идентификатор команды или события, вызвавшего это событие.
	CausationID string

	// EventID — стабильный идентификатор события домена. Служит ключом
	// идемпотентности публикации и дедупликации у потребителя.
	EventID string
)

func (id TenantID) String() string      { return string(id) }
func (id ActorID) String() string       { return string(id) }
func (id SegmentID) String() string     { return string(id) }
func (id LeadID) String() string        { return string(id) }
func (id SnapshotID) String() string    { return string(id) }
func (v ModelVersion) String() string   { return string(v) }
func (id CorrelationID) String() string { return string(id) }
func (id CausationID) String() string   { return string(id) }
func (id EventID) String() string       { return string(id) }

func (id TenantID) validate() error   { return validateID("tenant_id", string(id)) }
func (id ActorID) validate() error    { return validateID("actor_id", string(id)) }
func (id SegmentID) validate() error  { return validateID("segment_id", string(id)) }
func (id LeadID) validate() error     { return validateID("lead_id", string(id)) }
func (id SnapshotID) validate() error { return validateID("snapshot_id", string(id)) }
func (id EventID) validate() error    { return validateID("event_id", string(id)) }
func (v ModelVersion) validate() error {
	return validateID("model_version", string(v))
}

// validateID отвергает пустой или состоящий из пробелов идентификатор.
func validateID(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: %s", ErrEmptyID, field)
	}
	return nil
}
