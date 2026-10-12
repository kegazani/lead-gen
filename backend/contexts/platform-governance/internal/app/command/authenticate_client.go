package command

import (
	"context"
	"strings"

	"lead-gen/backend/contexts/platform-governance/internal/app"
	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/backend/contexts/platform-governance/internal/ports"
)

// AuthenticateClient — команда входа клиента: email учётной записи и пароль.
type AuthenticateClient struct {
	Email    string
	Password string
}

// AuthenticateClientResult — результат входа: тенант и пользователь учётной
// записи.
type AuthenticateClientResult struct {
	TenantID string
	UserID   string
}

// AuthenticateClientHandler — use case входа клиента.
type AuthenticateClientHandler struct {
	tenants ports.TenantRepository
	hasher  ports.PasswordHasher
}

// NewAuthenticateClientHandler собирает use case из репозитория тенантов и
// хешера паролей.
func NewAuthenticateClientHandler(tenants ports.TenantRepository, hasher ports.PasswordHasher) *AuthenticateClientHandler {
	return &AuthenticateClientHandler{tenants: tenants, hasher: hasher}
}

// Handle проверяет учётные данные и возвращает тенант с пользователем. Причина
// отказа не различается: неверный email, пароль и заявка на проверке одинаково
// дают app.ErrInvalidCredentials, чтобы не раскрывать существование учётной
// записи.
func (h *AuthenticateClientHandler) Handle(ctx context.Context, cmd AuthenticateClient) (AuthenticateClientResult, error) {
	email := strings.TrimSpace(cmd.Email)
	if email == "" {
		return AuthenticateClientResult{}, app.ErrInvalidCredentials
	}
	tenant, userID, err := h.tenants.FindUserByLogin(ctx, email)
	if err != nil {
		return AuthenticateClientResult{}, app.ErrInvalidCredentials
	}

	hash := ""
	for _, user := range tenant.Users() {
		if user.ID() == userID {
			hash = user.PasswordHash()
			break
		}
	}
	if hash == "" || !h.hasher.Verify(cmd.Password, hash) {
		return AuthenticateClientResult{}, app.ErrInvalidCredentials
	}
	if tenant.State() != domain.TenantActive {
		return AuthenticateClientResult{}, app.ErrInvalidCredentials
	}
	return AuthenticateClientResult{TenantID: tenant.ID().String(), UserID: userID.String()}, nil
}
