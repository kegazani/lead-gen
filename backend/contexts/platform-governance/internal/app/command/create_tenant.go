package command

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"lead-gen/backend/contexts/platform-governance/internal/app"
	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/backend/contexts/platform-governance/internal/ports"
)

// CreateTenant — команда создания тенанта уполномоченным актором. Она же
// регистрирует первого пользователя тенанта, если он задан.
type CreateTenant struct {
	// Actor — субъект команды (проверенный на границе аутентификации).
	Actor string
	// CommandID — клиентский идентификатор команды (ключ идемпотентности).
	CommandID string
	// TenantID — идентификатор создаваемого тенанта. Пусто — назначает генератор.
	TenantID string
	// CompanyName, Description — данные компании клиента.
	CompanyName string
	Description string
	// UserID, Login, Roles — необязательный первый пользователь тенанта.
	UserID string
	Login  string
	Roles  []string
}

// CreateTenantResult — результат создания тенанта.
type CreateTenantResult struct {
	TenantID string
	// Created — true, если тенант создан этим вызовом; false при повторной
	// команде с тем же ключом (эффект не создан).
	Created bool
}

// CreateTenantHandler — use case создания тенанта.
type CreateTenantHandler struct {
	uow    ports.UnitOfWork
	clock  ports.Clock
	ids    ports.IDGenerator
	grants ports.Grants
}

// NewCreateTenantHandler собирает use case из транзакции, часов, генератора
// идентификаторов и платформенных прав.
func NewCreateTenantHandler(uow ports.UnitOfWork, clock ports.Clock, ids ports.IDGenerator, grants ports.Grants) *CreateTenantHandler {
	return &CreateTenantHandler{uow: uow, clock: clock, ids: ids, grants: grants}
}

// Handle проверяет право актора, идемпотентность по паре (tenant_id, command_id)
// и создаёт активный тенант вместе с записью аудита в одной транзакции. Повтор
// команды с тем же ключом второй тенант не создаёт.
func (h *CreateTenantHandler) Handle(ctx context.Context, cmd CreateTenant) (CreateTenantResult, error) {
	actor := domain.ActorID(strings.TrimSpace(cmd.Actor))
	if err := actor.Validate(); err != nil {
		return CreateTenantResult{}, fmt.Errorf("%w: %v", app.ErrMissingActor, err)
	}
	if err := app.Authorize(ctx, h.grants, actor, domain.RightManageTenants); err != nil {
		return CreateTenantResult{}, err
	}
	commandID := domain.CommandID(strings.TrimSpace(cmd.CommandID))
	if commandID == "" {
		return CreateTenantResult{}, app.ErrMissingCommandID
	}
	tenantID := domain.TenantID(strings.TrimSpace(cmd.TenantID))
	if tenantID == "" {
		tenantID = h.ids.NewTenantID()
	}
	user, err := newUser(cmd.UserID, cmd.Login, cmd.Roles)
	if err != nil {
		return CreateTenantResult{}, err
	}

	at := h.clock.Now()
	result := CreateTenantResult{TenantID: tenantID.String()}
	err = h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		key := ports.CommandKey{TenantID: tenantID, CommandID: commandID}
		seen, err := tx.Idempotency().Seen(ctx, key)
		if err != nil {
			return err
		}
		if seen {
			return nil
		}
		if _, err := tx.Tenants().FindByID(ctx, tenantID); err == nil {
			// Тенант с таким идентификатором уже существует: второй не создаётся,
			// существующий остаётся без изменений.
			return tx.Idempotency().Remember(ctx, key)
		} else if !errors.Is(err, ports.ErrTenantNotFound) {
			return err
		}

		tenant, err := domain.RegisterTenant(domain.RegisterTenantParams{
			ID:          tenantID,
			CompanyName: cmd.CompanyName,
			Description: cmd.Description,
			At:          at,
			Actor:       actor,
			EventID:     h.ids.NewEventID(),
			AuditID:     h.ids.NewAuditRecordID(),
			User:        user,
		})
		if err != nil {
			return err
		}
		if err := tx.Tenants().Save(ctx, tenant); err != nil {
			return err
		}
		if err := tx.Outbox().Append(ctx, app.ChangeMessages(tenant, tenant.PullDomainEvents(), at)...); err != nil {
			return err
		}
		if err := tx.Idempotency().Remember(ctx, key); err != nil {
			return err
		}
		result.Created = true
		return nil
	})
	if err != nil {
		return CreateTenantResult{}, err
	}
	return result, nil
}

// newUser собирает необязательного первого пользователя тенанта из строк команды.
// Пустой идентификатор означает отсутствие пользователя.
func newUser(userID, login string, roles []string) (*domain.NewUser, error) {
	id := strings.TrimSpace(userID)
	if id == "" {
		return nil, nil
	}
	set, err := newRoleSet(roles)
	if err != nil {
		return nil, err
	}
	return &domain.NewUser{ID: domain.UserID(id), Login: login, Roles: set}, nil
}

// newRoleSet разбирает роли команды в доменный набор.
func newRoleSet(roles []string) (domain.RoleSet, error) {
	parsed := make([]domain.Role, 0, len(roles))
	for _, role := range roles {
		trimmed := strings.TrimSpace(role)
		if trimmed == "" {
			continue
		}
		parsed = append(parsed, domain.Role(trimmed))
	}
	if len(parsed) == 0 {
		return domain.RoleSet{}, domain.ErrRolesRequired
	}
	return domain.NewRoleSet(parsed...)
}
