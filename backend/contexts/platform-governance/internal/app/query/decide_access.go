package query

import (
	"context"
	"fmt"
	"strings"

	"lead-gen/backend/contexts/platform-governance/internal/app"
	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/backend/contexts/platform-governance/internal/ports"
)

// DecideAccess — запрос решения доступа: вправе ли актор действовать в тенанте.
type DecideAccess struct {
	Actor    string
	TenantID string
}

// DecideAccessHandler — use case решения доступа.
//
// Он же — реализация порта AccessDecision владельца: при co-location потребитель
// связывается с ним напрямую, не воспроизводя RBAC. Пустой актор или тенант —
// ошибка, а не отказ; отключённый тенант и отсутствие ролей дают отрицательное
// решение.
type DecideAccessHandler struct {
	tenants ports.TenantRepository
}

// DecideAccessHandler реализует порт проверки доступа.
var _ ports.AccessDecision = (*DecideAccessHandler)(nil)

// NewDecideAccessHandler собирает use case из репозитория тенантов.
func NewDecideAccessHandler(tenants ports.TenantRepository) *DecideAccessHandler {
	return &DecideAccessHandler{tenants: tenants}
}

// Handle выдаёт решение доступа по ролям актора в тенанте.
func (h *DecideAccessHandler) Handle(ctx context.Context, q DecideAccess) (bool, error) {
	actor := domain.ActorID(strings.TrimSpace(q.Actor))
	if err := actor.Validate(); err != nil {
		return false, fmt.Errorf("%w: %v", app.ErrMissingActor, err)
	}
	tenantID := domain.TenantID(strings.TrimSpace(q.TenantID))
	if err := tenantID.Validate(); err != nil {
		return false, err
	}
	tenant, err := h.tenants.FindByID(ctx, tenantID)
	if err != nil {
		return false, err
	}
	return tenant.DecideAccess(actor)
}

// CanAct реализует ports.AccessDecision — форму порта потребителя
// (outreach-orchestration): CanAct(ctx, actor, tenant) (bool, error).
func (h *DecideAccessHandler) CanAct(ctx context.Context, actor domain.ActorID, tenant domain.TenantID) (bool, error) {
	return h.Handle(ctx, DecideAccess{Actor: actor.String(), TenantID: tenant.String()})
}

// Roles возвращает набор ролей актора в тенанте — ту часть снимка
// access-decision, по которой консоль управления применяет матрицу «роль →
// раздел → действие». Роли не вычисляются и не хранятся потребителем: источник —
// агрегат tenant.
//
// Отключённый тенант ролей не выдаёт (пустой набор), как и в DecideAccess.
// Пустой актор — ошибка вызывающего, а не пустой набор.
func (h *DecideAccessHandler) Roles(ctx context.Context, tenant domain.TenantID, actor domain.ActorID) (domain.RoleSet, error) {
	if err := actor.Validate(); err != nil {
		return domain.RoleSet{}, fmt.Errorf("%w: %v", app.ErrMissingActor, err)
	}
	if err := tenant.Validate(); err != nil {
		return domain.RoleSet{}, err
	}
	t, err := h.tenants.FindByID(ctx, tenant)
	if err != nil {
		return domain.RoleSet{}, err
	}
	if t.State() != domain.TenantActive {
		return domain.RoleSet{}, nil
	}
	for _, user := range t.Users() {
		if user.ID() == domain.UserID(actor) {
			return user.Roles(), nil
		}
	}
	return domain.RoleSet{}, nil
}
