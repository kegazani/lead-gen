package domain

import (
	"fmt"
	"strings"
)

// Идентификаторы домена community-intelligence.
//
// Каждый идентификатор — отдельный именованный тип, чтобы сообщество нельзя было
// передать вместо выжимки, а сообщение — вместо автора. Значения неизменяемы.
type (
	// DigestID — идентификатор aggregate root community-digest.
	DigestID string

	// CommunityID — идентификатор разрешённого Telegram-сообщества.
	CommunityID string

	// TenantID — идентификатор тенанта-владельца данных.
	TenantID string

	// ActorID — идентификатор оператора, инициировавшего чтение.
	ActorID string

	// MessageID — идентификатор сообщения внутри сообщества. Служит ссылкой на
	// источник пункта выжимки; автора не несёт.
	MessageID string

	// CorrelationID — идентификатор сквозного сценария чтения.
	CorrelationID string

	// CausationID — идентификатор команды или события, вызвавшего чтение.
	CausationID string

	// EventID — стабильный идентификатор события домена. Служит ключом
	// идемпотентности публикации и дедупликации у потребителя.
	EventID string
)

func (id DigestID) String() string      { return string(id) }
func (id CommunityID) String() string   { return string(id) }
func (id TenantID) String() string      { return string(id) }
func (id ActorID) String() string       { return string(id) }
func (id MessageID) String() string     { return string(id) }
func (id CorrelationID) String() string { return string(id) }
func (id CausationID) String() string   { return string(id) }
func (id EventID) String() string       { return string(id) }

func (id DigestID) validate() error    { return validateID("digest_id", string(id)) }
func (id CommunityID) validate() error { return validateID("community_id", string(id)) }
func (id TenantID) validate() error    { return validateID("tenant_id", string(id)) }
func (id MessageID) validate() error   { return validateID("message_id", string(id)) }
func (id EventID) validate() error     { return validateID("event_id", string(id)) }

// validateID отвергает пустой или состоящий из пробелов идентификатор.
func validateID(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: %s", ErrEmptyID, field)
	}
	return nil
}
