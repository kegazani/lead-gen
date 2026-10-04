package command

import (
	"context"
	"strings"
	"time"

	"lead-gen/backend/contexts/platform-governance/internal/app"
	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/backend/contexts/platform-governance/internal/ports"
)

// changeTenant — общий сценарий изменяющей команды над существующим тенантом:
// проверка идемпотентности по (tenant_id, command_id), загрузка агрегата,
// мутация, сохранение агрегата, его событий и снимка правил в одной транзакции.
//
// Если ключ команды уже применён, эффект не выполняется. Ошибка мутации ведёт к
// откату транзакции: ни агрегат, ни события, ни ключ не сохраняются.
func changeTenant(
	ctx context.Context,
	tx ports.Transaction,
	tenantID domain.TenantID,
	commandID domain.CommandID,
	at time.Time,
	mutate func(*domain.Tenant) error,
) error {
	key := ports.CommandKey{TenantID: tenantID, CommandID: commandID}
	seen, err := tx.Idempotency().Seen(ctx, key)
	if err != nil {
		return err
	}
	if seen {
		return nil
	}
	tenant, err := tx.Tenants().FindByID(ctx, tenantID)
	if err != nil {
		return err
	}
	if err := mutate(tenant); err != nil {
		return err
	}
	if err := tx.Tenants().Save(ctx, tenant); err != nil {
		return err
	}
	if err := tx.Outbox().Append(ctx, app.ChangeMessages(tenant, tenant.PullDomainEvents(), at)...); err != nil {
		return err
	}
	return tx.Idempotency().Remember(ctx, key)
}

// changeContext собирает контекст изменения доступа с новыми идентификаторами
// события и записи аудита.
func changeContext(at time.Time, actor domain.ActorID, ids ports.IDGenerator) domain.ChangeContext {
	return domain.ChangeContext{
		At:      at,
		Actor:   actor,
		EventID: ids.NewEventID(),
		AuditID: ids.NewAuditRecordID(),
	}
}

// commandIDs разбирает и проверяет идентификаторы тенанта и команды,
// обязательные для изменяющих команд.
func commandIDs(rawTenant, rawCommand string) (domain.TenantID, domain.CommandID, error) {
	tenantID := domain.TenantID(strings.TrimSpace(rawTenant))
	if tenantID == "" {
		return "", "", domain.ErrEmptyID
	}
	commandID := domain.CommandID(strings.TrimSpace(rawCommand))
	if commandID == "" {
		return "", "", app.ErrMissingCommandID
	}
	return tenantID, commandID, nil
}
