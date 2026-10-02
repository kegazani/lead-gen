package domain

import (
	"fmt"
	"strings"
)

// Идентификаторы домена engagement-observation.
//
// Каждый идентификатор — отдельный именованный тип, чтобы сырой сигнал нельзя
// было передать вместо квалифицированного события, а ссылку перехода — вместо
// тенанта. Значения неизменяемы.
type (
	// EngagementEventID — идентификатор aggregate root engagement-event.
	EngagementEventID string

	// SignalID — идентификатор сырого сигнала у источника (провайдера или
	// письма). Служит ключом дедупликации входящего webhook.
	SignalID string

	// TenantID — идентификатор тенанта-владельца данных.
	TenantID string

	// CompanyID — ссылка на компанию, к которой отнесено взаимодействие
	// (владение lead-management).
	CompanyID string

	// EventID — стабильный идентификатор доменного события. Служит ключом
	// идемпотентности публикации и дедупликации у потребителя.
	EventID string

	// LinkRef — непрозрачная ссылка трекинг-перехода. Это не адрес: адрес
	// живёт отдельно и виден только получателю перехода.
	LinkRef string

	// CorrelationID — идентификатор сквозного сценария, к которому относится
	// событие взаимодействия.
	CorrelationID string

	// CausationID — идентификатор команды или события, вызвавшего это событие.
	CausationID string
)

func (id EngagementEventID) String() string { return string(id) }
func (id SignalID) String() string          { return string(id) }
func (id TenantID) String() string          { return string(id) }
func (id CompanyID) String() string         { return string(id) }
func (id EventID) String() string           { return string(id) }
func (id LinkRef) String() string           { return string(id) }
func (id CorrelationID) String() string     { return string(id) }
func (id CausationID) String() string       { return string(id) }

func (id EngagementEventID) validate() error { return validateID("engagement_event_id", string(id)) }
func (id SignalID) validate() error          { return validateID("signal_id", string(id)) }
func (id TenantID) validate() error          { return validateID("tenant_id", string(id)) }
func (id CompanyID) validate() error         { return validateID("company_id", string(id)) }
func (id EventID) validate() error           { return validateID("event_id", string(id)) }
func (id LinkRef) validate() error           { return validateID("link_ref", string(id)) }

// validateID отвергает пустой или состоящий из пробелов идентификатор.
func validateID(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: %s", ErrEmptyID, field)
	}
	return nil
}
