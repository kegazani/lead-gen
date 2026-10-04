package query

import (
	"context"
	"strings"
	"time"

	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/backend/contexts/platform-governance/internal/ports"
)

// AuditJournal — запрос журнала аудита тенанта.
type AuditJournal struct {
	TenantID string
}

// AuditEntry — строка журнала аудита для показа: субъект, действие, объект,
// время и результат.
type AuditEntry struct {
	ID           string
	Actor        string
	Action       string
	Object       string
	At           time.Time
	Outcome      string
	CorrectionOf string
}

// AuditJournalHandler — use case чтения журнала аудита.
type AuditJournalHandler struct {
	tenants ports.TenantRepository
}

// NewAuditJournalHandler собирает use case из репозитория тенантов.
func NewAuditJournalHandler(tenants ports.TenantRepository) *AuditJournalHandler {
	return &AuditJournalHandler{tenants: tenants}
}

// Handle возвращает журнал аудита указанного тенанта. Запрос адресован по
// tenant_id, поэтому журнал другого тенанта недоступен: чужая граница не
// пересекается. Пустой идентификатор тенанта — ошибка.
func (h *AuditJournalHandler) Handle(ctx context.Context, q AuditJournal) ([]AuditEntry, error) {
	tenantID := domain.TenantID(strings.TrimSpace(q.TenantID))
	if err := tenantID.Validate(); err != nil {
		return nil, err
	}
	tenant, err := h.tenants.FindByID(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	records := tenant.AuditJournal()
	entries := make([]AuditEntry, 0, len(records))
	for _, record := range records {
		entries = append(entries, AuditEntry{
			ID:           record.ID().String(),
			Actor:        record.Actor().String(),
			Action:       string(record.Action()),
			Object:       record.Object(),
			At:           record.At(),
			Outcome:      string(record.Outcome()),
			CorrectionOf: record.CorrectionOf().String(),
		})
	}
	return entries, nil
}
