package domain

import (
	"fmt"
	"strings"
)

// Идентификаторы домена publishing.
//
// Каждый идентификатор — отдельный именованный тип, чтобы публикацию нельзя было
// передать вместо тенанта, а актора — вместо площадки. Значения неизменяемы.
type (
	// PublicationID — идентификатор aggregate root publication.
	PublicationID string

	// TenantID — идентификатор тенанта-владельца записи публикации.
	TenantID string

	// ActorID — идентификатор человека-оператора: автора команды или утвердившего.
	ActorID string

	// EventID — стабильный идентификатор события домена. Служит ключом
	// идемпотентности публикации и дедупликации у потребителя.
	EventID string

	// CorrelationID — идентификатор сквозного сценария, к которому относится
	// событие публикации.
	CorrelationID string

	// CausationID — идентификатор команды или события, вызвавшего это событие.
	CausationID string

	// IdempotencyKey — ключ идемпотентности публикации. Повтор с тем же ключом не
	// создаёт вторую публикацию (инвариант агрегата publication).
	IdempotencyKey string
)

func (id PublicationID) String() string  { return string(id) }
func (id TenantID) String() string       { return string(id) }
func (id ActorID) String() string        { return string(id) }
func (id EventID) String() string        { return string(id) }
func (id CorrelationID) String() string  { return string(id) }
func (id CausationID) String() string    { return string(id) }
func (id IdempotencyKey) String() string { return string(id) }

func (id PublicationID) validate() error  { return validateID("publication_id", string(id)) }
func (id TenantID) validate() error       { return validateID("tenant_id", string(id)) }
func (id ActorID) validate() error        { return validateID("actor_id", string(id)) }
func (id EventID) validate() error        { return validateID("event_id", string(id)) }
func (id IdempotencyKey) validate() error { return validateID("idempotency_key", string(id)) }

// validateID отвергает пустой или состоящий из пробелов идентификатор.
func validateID(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: %s", ErrEmptyID, field)
	}
	return nil
}
