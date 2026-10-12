// Package tenantaccesschanged описывает опубликованное событие изменения
// доступа, принадлежащее bounded context platform-governance.
package tenantaccesschanged

import (
	"fmt"
	"strings"
	"time"
)

const (
	// ContractName — стабильное имя контракта.
	ContractName = "tenant-access-changed"
	// SchemaVersion — текущая версия схемы.
	SchemaVersion = "1.0.0"
)

// Envelope — общий конверт опубликованного события.
type Envelope struct {
	EventID       string    `json:"event_id"`
	OccurredAt    time.Time `json:"occurred_at"`
	TenantID      string    `json:"tenant_id"`
	CorrelationID string    `json:"correlation_id"`
	CausationID   string    `json:"causation_id,omitempty"`
	Name          string    `json:"name"`
	SchemaVersion string    `json:"schema_version"`
}

// AccessChange — опубликованный факт изменения доступа. Роли относятся только
// к UserID в TenantID; пустой UserID допустим для изменения состояния тенанта.
type AccessChange struct {
	Envelope
	UserID string   `json:"user_id,omitempty"`
	Roles  []string `json:"roles,omitempty"`
	Change string   `json:"change"`
}

// Validate проверяет конверт и payload без вывода значений в ошибку.
func (e AccessChange) Validate() error {
	var invalid []string
	for field, value := range map[string]string{
		"event_id": e.EventID, "tenant_id": e.TenantID, "correlation_id": e.CorrelationID,
		"name": e.Name, "schema_version": e.SchemaVersion, "change": e.Change,
	} {
		if strings.TrimSpace(value) == "" {
			invalid = append(invalid, field)
		}
	}
	if e.OccurredAt.IsZero() {
		invalid = append(invalid, "occurred_at")
	}
	if e.Name != ContractName {
		invalid = append(invalid, "name")
	}
	if e.SchemaVersion != SchemaVersion {
		invalid = append(invalid, "schema_version")
	}
	if len(invalid) != 0 {
		return fmt.Errorf("tenantaccesschanged: некорректные обязательные поля: %s", strings.Join(invalid, ", "))
	}
	return nil
}
