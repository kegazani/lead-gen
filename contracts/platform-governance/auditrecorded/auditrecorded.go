// Package auditrecorded содержит опубликованный контракт события аудита,
// принадлежащий bounded context platform-governance.
package auditrecorded

import (
	"fmt"
	"strings"
	"time"
)

const (
	// ContractName — стабильное имя контракта.
	ContractName = "audit-recorded"
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

// AuditRecord — неизменяемый факт аудита. Коррекция — новая запись со ссылкой
// CorrectionOf на исходную запись; секреты и свободные пользовательские данные
// не являются частью контракта.
type AuditRecord struct {
	Envelope
	AuditRecordID string `json:"audit_record_id"`
	Actor         string `json:"actor"`
	Action        string `json:"action"`
	Object        string `json:"object"`
	Outcome       string `json:"outcome"`
	CorrectionOf  string `json:"correction_of,omitempty"`
}

// Validate проверяет поля конверта и факт, не включая значения полей в ошибку.
func (e AuditRecord) Validate() error {
	var invalid []string
	for field, value := range map[string]string{
		"event_id": e.EventID, "tenant_id": e.TenantID, "correlation_id": e.CorrelationID,
		"name": e.Name, "schema_version": e.SchemaVersion, "audit_record_id": e.AuditRecordID,
		"actor": e.Actor, "action": e.Action, "object": e.Object, "outcome": e.Outcome,
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
		return fmt.Errorf("auditrecorded: некорректные обязательные поля: %s", strings.Join(invalid, ", "))
	}
	return nil
}
