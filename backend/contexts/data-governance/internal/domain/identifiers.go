package domain

import (
	"fmt"
	"strings"
)

// Идентификаторы домена data-governance.
//
// Каждый идентификатор — отдельный именованный тип, чтобы основание нельзя было
// передать вместо записи стоп-листа, а контакт — вместо тенанта. Значения
// неизменяемы.
type (
	// BasisID — идентификатор aggregate root processing-basis.
	BasisID string

	// SuppressionID — идентификатор entity suppression-entry.
	SuppressionID string

	// TenantID — идентификатор тенанта-владельца данных.
	TenantID string

	// ActorID — идентификатор пользователя, изменившего состояние.
	ActorID string

	// ContactRef — ссылка на контакт (адрес или канал), который подавляется.
	ContactRef string

	// CorrelationID — идентификатор сквозного сценария, к которому относится
	// событие.
	CorrelationID string

	// CausationID — идентификатор команды или события, вызвавшего это событие.
	CausationID string

	// EventID — стабильный идентификатор события домена. Служит ключом
	// идемпотентности публикации и дедупликации у потребителя.
	EventID string
)

func (id BasisID) String() string       { return string(id) }
func (id SuppressionID) String() string { return string(id) }
func (id TenantID) String() string      { return string(id) }
func (id ActorID) String() string       { return string(id) }
func (id ContactRef) String() string    { return string(id) }
func (id CorrelationID) String() string { return string(id) }
func (id CausationID) String() string   { return string(id) }
func (id EventID) String() string       { return string(id) }

func (id BasisID) validate() error       { return validateID("basis_id", string(id)) }
func (id SuppressionID) validate() error { return validateID("suppression_id", string(id)) }
func (id TenantID) validate() error      { return validateID("tenant_id", string(id)) }
func (id ActorID) validate() error       { return validateID("actor_id", string(id)) }
func (id ContactRef) validate() error    { return validateID("contact", string(id)) }
func (id EventID) validate() error       { return validateID("event_id", string(id)) }

// validateID отвергает пустой или состоящий из пробелов идентификатор.
func validateID(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: %s", ErrEmptyID, field)
	}
	return nil
}
