package command

import (
	"context"
	"errors"
	"strings"

	"lead-gen/backend/contexts/platform-governance/internal/app"
	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/backend/contexts/platform-governance/internal/ports"
)

// RegisterClient — команда самообслуживания: подача заявки на регистрацию
// компании с идентифицирующими данными и учётной записью клиента.
type RegisterClient struct {
	CompanyName string
	Description string
	// Email — email учётной записи; он же логин клиента.
	Email string
	// Password — сырой пароль; хешируется в use case и в домен не попадает.
	Password string
	// INN, CompanyEmail, Website, LegalData — идентифицирующие данные компании.
	INN          string
	CompanyEmail string
	Website      string
	LegalData    string
}

// RegisterClientResult — результат подачи заявки на регистрацию.
type RegisterClientResult struct {
	TenantID string
	UserID   string
}

// RegisterClientHandler — use case подачи заявки на регистрацию клиента.
type RegisterClientHandler struct {
	uow    ports.UnitOfWork
	clock  ports.Clock
	ids    ports.IDGenerator
	hasher ports.PasswordHasher
}

// NewRegisterClientHandler собирает use case из транзакции, часов, генератора
// идентификаторов и хешера паролей.
func NewRegisterClientHandler(uow ports.UnitOfWork, clock ports.Clock, ids ports.IDGenerator, hasher ports.PasswordHasher) *RegisterClientHandler {
	return &RegisterClientHandler{uow: uow, clock: clock, ids: ids, hasher: hasher}
}

// Handle подаёт заявку на регистрацию: создаёт проверяемый (pending) тенант с
// пользователем роли client и хешем пароля, сохраняет его вместе с событиями в
// одной транзакции. Повторная заявка с теми же идентифицирующими данными компании
// (ИНН или email компании) отклоняется.
func (h *RegisterClientHandler) Handle(ctx context.Context, cmd RegisterClient) (RegisterClientResult, error) {
	email := strings.TrimSpace(cmd.Email)
	if email == "" {
		return RegisterClientResult{}, domain.ErrUserEmailRequired
	}
	password := cmd.Password
	if password == "" {
		return RegisterClientResult{}, domain.ErrUserPasswordRequired
	}
	inn := strings.TrimSpace(cmd.INN)
	if inn == "" {
		return RegisterClientResult{}, domain.ErrINNRequired
	}
	companyEmail := strings.TrimSpace(cmd.CompanyEmail)
	if companyEmail == "" {
		return RegisterClientResult{}, domain.ErrCompanyEmailRequired
	}

	userID := h.ids.NewUserID()
	roles, err := domain.NewRoleSet(domain.RoleClient)
	if err != nil {
		return RegisterClientResult{}, err
	}
	hash, err := h.hasher.Hash(password)
	if err != nil {
		return RegisterClientResult{}, err
	}
	at := h.clock.Now()
	result := RegisterClientResult{UserID: userID.String()}

	err = h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		if _, err := tx.Tenants().FindByIdentity(ctx, inn, companyEmail); err == nil {
			return domain.ErrDuplicateCompany
		} else if !errors.Is(err, ports.ErrTenantNotFound) {
			return err
		}

		tenant, err := domain.RegisterTenant(domain.RegisterTenantParams{
			ID:           h.ids.NewTenantID(),
			CompanyName:  cmd.CompanyName,
			Description:  cmd.Description,
			INN:          inn,
			CompanyEmail: companyEmail,
			Website:      strings.TrimSpace(cmd.Website),
			LegalData:    strings.TrimSpace(cmd.LegalData),
			Pending:      true,
			At:           at,
			Actor:        domain.ActorID(userID),
			EventID:      h.ids.NewEventID(),
			AuditID:      h.ids.NewAuditRecordID(),
			User:         &domain.NewUser{ID: userID, Login: email, PasswordHash: hash, Roles: roles},
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
		result.TenantID = tenant.ID().String()
		return nil
	})
	if err != nil {
		return RegisterClientResult{}, err
	}
	return result, nil
}
