package app

import (
	"context"
	"fmt"
	"time"

	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/backend/contexts/platform-governance/internal/ports"
)

// ContractSchemaVersion — текущая версия схемы публикуемых контрактов контекста
// (semver): MAJOR несовместимо, MINOR добавляет опциональное поле, PATCH
// уточняет смысл.
const ContractSchemaVersion = "1.0.0"

// ContractAccessDecision — имя контракта-снимка правил тенанта.
const ContractAccessDecision = "access-decision"

// Authorize проверяет право актора по политике least-privilege. Пустой актор —
// ошибка вызывающего; отсутствие права — ErrForbidden. Проверка идёт на
// прикладном слое до изменения состояния, чтобы запрещённая команда не создавала
// эффекта.
func Authorize(ctx context.Context, grants ports.Grants, actor domain.ActorID, right domain.Right) error {
	if grants == nil {
		return fmt.Errorf("%w: %s", ErrForbidden, right)
	}
	roles, err := grants.Roles(ctx, actor)
	if err != nil {
		return fmt.Errorf("проверка права актора: %w", err)
	}
	if !domain.RoleSetHasRight(roles, right) {
		return fmt.Errorf("%w: %s", ErrForbidden, right)
	}
	return nil
}

// EventMessages оборачивает доменные события в записи outbox. Имя контракта
// совпадает с именем события каталога; payload — само событие, конверт заполнен
// его обязательными полями.
func EventMessages(events []domain.DomainEvent) []ports.Message {
	messages := make([]ports.Message, 0, len(events))
	for _, event := range events {
		header := event.Header()
		messages = append(messages, ports.Message{
			Contract:      string(event.Name()),
			SchemaVersion: ContractSchemaVersion,
			EventID:       header.EventID,
			TenantID:      header.TenantID,
			OccurredAt:    header.OccurredAt,
			CorrelationID: header.CorrelationID,
			CausationID:   header.CausationID,
			Payload:       event,
		})
	}
	return messages
}

// AccessDecisionMessage формирует публикацию снимка правил тенанта. Версия
// снимка монотонна, поэтому потребитель применяет только более новые снимки;
// ключ идемпотентности — (tenant_id, snapshot_version).
func AccessDecisionMessage(tenant *domain.Tenant, at time.Time) ports.Message {
	snapshot := tenant.AccessDecisionSnapshot(at)
	return ports.Message{
		Contract:        ContractAccessDecision,
		SchemaVersion:   ContractSchemaVersion,
		EventID:         domain.EventID(fmt.Sprintf("%s:%s:%d", tenant.ID(), ContractAccessDecision, snapshot.Version)),
		TenantID:        tenant.ID(),
		OccurredAt:      at,
		SnapshotVersion: snapshot.Version,
		Payload:         snapshot,
	}
}

// ChangeMessages собирает полный набор публикаций для изменения доступа:
// доменные события (tenant-access-changed, audit-recorded) и снимок правил
// (access-decision).
func ChangeMessages(tenant *domain.Tenant, events []domain.DomainEvent, at time.Time) []ports.Message {
	messages := EventMessages(events)
	messages = append(messages, AccessDecisionMessage(tenant, at))
	return messages
}
