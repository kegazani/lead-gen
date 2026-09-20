// Package transport — синхронный транспорт границы сервиса поверх net/http.
//
// Связи вида command и query идут внутренними HTTP-вызовами: command — POST с
// ключом идемпотентности, query — GET с детерминированным ключом кэша. Конверт
// (contracts.Envelope) переносится заголовками, поэтому correlation_id и
// causation_id сквозные и для тела, и для метаданных, а версия контракта видна
// без разбора тела (design.md, Решения 2 и 8).
//
// Пакет не содержит доменных типов: тело запроса — непрозрачные байты JSON,
// которые сериализует и разбирает сторона-владелец контракта.
package transport

import (
	"net/http"
	"time"

	"lead-gen/backend/platform/contracts"
)

// Заголовки конверта. Имена стабильны: по ним обработчик владельца
// восстанавливает конверт без разбора тела.
const (
	headerEventID        = "X-Event-Id"
	headerOccurredAt     = "X-Occurred-At"
	headerTenantID       = "X-Tenant-Id"
	headerCorrelationID  = "X-Correlation-Id"
	headerCausationID    = "X-Causation-Id"
	headerName           = "X-Contract-Name"
	headerSchemaVersion  = "X-Schema-Version"
	headerIdempotencyKey = "Idempotency-Key"
)

// writeEnvelope переносит заполненные поля конверта в заголовки.
func writeEnvelope(h http.Header, e contracts.Envelope) {
	if e.EventID != "" {
		h.Set(headerEventID, e.EventID)
	}
	if !e.OccurredAt.IsZero() {
		h.Set(headerOccurredAt, e.OccurredAt.UTC().Format(time.RFC3339Nano))
	}
	if e.TenantID != "" {
		h.Set(headerTenantID, e.TenantID)
	}
	if e.CorrelationID != "" {
		h.Set(headerCorrelationID, e.CorrelationID)
	}
	if e.CausationID != "" {
		h.Set(headerCausationID, e.CausationID)
	}
	if e.Name != "" {
		h.Set(headerName, e.Name)
	}
	if e.SchemaVersion != "" {
		h.Set(headerSchemaVersion, e.SchemaVersion)
	}
}

// readEnvelope восстанавливает конверт из заголовков. Некорректный occurred_at
// оставляется нулевым: проверка обязательных полей — на стороне владельца,
// транспорт не подменяет значения.
func readEnvelope(h http.Header) contracts.Envelope {
	e := contracts.Envelope{
		EventID:       h.Get(headerEventID),
		TenantID:      h.Get(headerTenantID),
		CorrelationID: h.Get(headerCorrelationID),
		CausationID:   h.Get(headerCausationID),
		Name:          h.Get(headerName),
		SchemaVersion: h.Get(headerSchemaVersion),
	}
	if v := h.Get(headerOccurredAt); v != "" {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			e.OccurredAt = t
		}
	}
	return e
}
