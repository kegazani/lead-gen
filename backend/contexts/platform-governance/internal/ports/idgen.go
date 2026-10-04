package ports

import "lead-gen/backend/contexts/platform-governance/internal/domain"

// IDGenerator — источник идентификаторов тенанта, пользователя, события,
// записи аудита и идентификаторов заявки, передаваемых владельцу по контракту
// request-intent. Инъекция генератора делает идентификаторы детерминированными в
// тестах.
type IDGenerator interface {
	// NewTenantID возвращает новый идентификатор тенанта.
	NewTenantID() domain.TenantID

	// NewUserID возвращает новый идентификатор пользователя.
	NewUserID() domain.UserID

	// NewEventID возвращает новый идентификатор события домена.
	NewEventID() domain.EventID

	// NewAuditRecordID возвращает новый идентификатор записи аудита.
	NewAuditRecordID() domain.AuditRecordID

	// NewRequestID возвращает новый идентификатор заявки для контракта
	// request-intent. Идентификатор назначает потребитель, поэтому его порождает
	// портал.
	NewRequestID() string

	// NewOfferID возвращает новый идентификатор версии оффера для контракта
	// request-intent.
	NewOfferID() string
}
