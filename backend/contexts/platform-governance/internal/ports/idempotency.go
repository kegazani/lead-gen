package ports

import (
	"context"

	"lead-gen/backend/contexts/platform-governance/internal/domain"
)

// CommandKey — ключ идемпотентности изменяющей команды: пара
// (идентификатор тенанта, клиентский идентификатор команды), предписанная
// docs/architecture.md §6.1.
type CommandKey struct {
	TenantID  domain.TenantID
	CommandID domain.CommandID
}

// Valid сообщает, что ключ заполнен. Пустой ключ не защищает от повтора.
func (k CommandKey) Valid() bool {
	return k.TenantID != "" && k.CommandID != ""
}

// Idempotency — хранилище применённых ключей команд в рамках транзакции.
//
// Ключ записывается атомарно с эффектом команды: и агрегат, и ключ коммитятся
// одной транзакцией. Повтор команды с тем же ключом эффекта не создаёт.
type Idempotency interface {
	// Seen сообщает, применён ли ключ (в этой транзакции или ранее).
	Seen(ctx context.Context, key CommandKey) (bool, error)

	// Remember фиксирует ключ в текущей транзакции.
	Remember(ctx context.Context, key CommandKey) error
}
