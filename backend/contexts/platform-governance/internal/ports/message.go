package ports

import (
	"time"

	"lead-gen/backend/contexts/platform-governance/internal/domain"
)

// Message — запись transactional outbox: опубликованный контракт владельца,
// конверт события и полезная нагрузка.
//
// Контракт назван именем из каталога (tenant-access-changed, audit-recorded,
// access-decision). Payload — структурный факт (доменное событие или снимок
// правил); его сериализацию в wire-формат выполняет адаптер транспорта, а не
// прикладной слой. Общие поля конверта — event_id, occurred_at, tenant_id,
// correlation_id, causation_id (docs/domain-model.md).
type Message struct {
	// Contract — имя публикуемого контракта.
	Contract string
	// SchemaVersion — версия схемы контракта (semver).
	SchemaVersion string
	// EventID — стабильный идентификатор сообщения; ключ дедупликации.
	EventID domain.EventID
	// TenantID — тенант, к которому относится сообщение.
	TenantID domain.TenantID
	// OccurredAt — время события.
	OccurredAt time.Time
	// SnapshotVersion — версия снимка правил (для access-decision); 0 для
	// событий, не несущих снимок.
	SnapshotVersion uint64
	// CorrelationID, CausationID — необязательные поля сквозного сценария.
	CorrelationID string
	CausationID   string
	// Payload — структурная полезная нагрузка контракта.
	Payload any
}
