package domain

import (
	"fmt"
	"strings"
)

// Идентификаторы домена request-fulfillment.
//
// Каждый идентификатор — отдельный именованный тип, чтобы заявку нельзя было
// передать вместо оффера, а актора — вместо тенанта. Значения неизменяемы.
type (
	// RequestID — идентификатор aggregate root request.
	RequestID string

	// OfferID — идентификатор entity offer (версии оффера).
	OfferID string

	// TenantID — идентификатор тенанта-владельца данных.
	TenantID string

	// ActorID — идентификатор пользователя-оператора, совершившего переход.
	ActorID string

	// AudienceSnapshotID — ссылка на снимок аудитории (владение audience).
	AudienceSnapshotID string

	// CampaignID — ссылка на кампанию (владение outreach-orchestration).
	CampaignID string

	// DealID — ссылка на сделку (владение sales-workflow).
	DealID string

	// CallID — ссылка на звонок-уточнение (владение outreach-orchestration).
	CallID string

	// CorrelationID — идентификатор сквозного сценария, к которому относится
	// событие заявки.
	CorrelationID string

	// CausationID — идентификатор команды или события, вызвавшего это событие.
	CausationID string

	// EventID — стабильный идентификатор события домена. Служит ключом
	// идемпотентности публикации и дедупликации у потребителя.
	EventID string
)

func (id RequestID) String() string          { return string(id) }
func (id OfferID) String() string            { return string(id) }
func (id TenantID) String() string           { return string(id) }
func (id ActorID) String() string            { return string(id) }
func (id AudienceSnapshotID) String() string { return string(id) }
func (id CampaignID) String() string         { return string(id) }
func (id DealID) String() string             { return string(id) }
func (id CallID) String() string             { return string(id) }
func (id CorrelationID) String() string      { return string(id) }
func (id CausationID) String() string        { return string(id) }
func (id EventID) String() string            { return string(id) }

func (id RequestID) validate() error { return validateID("request_id", string(id)) }
func (id OfferID) validate() error   { return validateID("offer_id", string(id)) }
func (id TenantID) validate() error  { return validateID("tenant_id", string(id)) }
func (id ActorID) validate() error   { return validateID("actor_id", string(id)) }
func (id EventID) validate() error   { return validateID("event_id", string(id)) }

// validateID отвергает пустой или состоящий из пробелов идентификатор. Ссылки на
// чужие агрегаты (audience, campaign, deal) необязательны и здесь не проверяются:
// их заполняет исполнение по мере продвижения заявки.
func validateID(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: %s", ErrEmptyID, field)
	}
	return nil
}
