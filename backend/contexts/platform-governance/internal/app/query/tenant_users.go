package query

import (
	"context"
	"strings"

	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/backend/contexts/platform-governance/internal/ports"
)

// TenantUsers — запрос состава пользователей тенанта.
type TenantUsers struct {
	TenantID string
}

// TenantUserView — строка состава пользователей: идентификатор, логин и роли.
//
// Роли приходят из агрегата tenant (владелец модели), а не вычисляются экраном:
// консоль не хранит и не пересобирает RBAC локально.
type TenantUserView struct {
	ID    string
	Login string
	Roles []string
}

// TenantUsersHandler — use case чтения состава пользователей тенанта.
type TenantUsersHandler struct {
	tenants ports.TenantRepository
}

// NewTenantUsersHandler собирает use case из репозитория тенантов.
func NewTenantUsersHandler(tenants ports.TenantRepository) *TenantUsersHandler {
	return &TenantUsersHandler{tenants: tenants}
}

// Handle возвращает состав пользователей тенанта. Пустой идентификатор тенанта —
// ошибка; отсутствующий тенант — ports.ErrTenantNotFound.
func (h *TenantUsersHandler) Handle(ctx context.Context, q TenantUsers) ([]TenantUserView, error) {
	tenantID := domain.TenantID(strings.TrimSpace(q.TenantID))
	if err := tenantID.Validate(); err != nil {
		return nil, err
	}
	tenant, err := h.tenants.FindByID(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	users := tenant.Users()
	views := make([]TenantUserView, 0, len(users))
	for _, user := range users {
		roles := make([]string, 0, user.Roles().Len())
		for _, role := range user.Roles().Roles() {
			roles = append(roles, role.String())
		}
		views = append(views, TenantUserView{ID: user.ID().String(), Login: user.Login(), Roles: roles})
	}
	return views, nil
}
