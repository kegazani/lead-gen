package query

import (
	"context"
	"strings"

	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/backend/contexts/platform-governance/internal/ports"
)

// TenantProfile — запрос профиля компании для портала.
type TenantProfile struct {
	TenantID string
}

// ServiceView — услуга профиля для экрана: краткое описание (свёрнутый блок) и
// полное (развёрнутый).
type ServiceView struct {
	Name  string
	Short string
	Full  string
}

// TenantProfileView — проекция профиля компании для портала.
type TenantProfileView struct {
	TenantID      string
	CompanyName   string
	Website       string
	ProfileStatus string
	Description   string
	Target        string
	Services      []ServiceView
}

// TenantProfileHandler — use case чтения профиля компании.
type TenantProfileHandler struct {
	tenants ports.TenantRepository
}

// NewTenantProfileHandler собирает use case из репозитория тенантов.
func NewTenantProfileHandler(tenants ports.TenantRepository) *TenantProfileHandler {
	return &TenantProfileHandler{tenants: tenants}
}

// Handle возвращает профиль компании тенанта. Пустой идентификатор — ошибка;
// отсутствующий тенант — ports.ErrTenantNotFound.
func (h *TenantProfileHandler) Handle(ctx context.Context, q TenantProfile) (TenantProfileView, error) {
	tenantID := domain.TenantID(strings.TrimSpace(q.TenantID))
	if err := tenantID.Validate(); err != nil {
		return TenantProfileView{}, err
	}
	tenant, err := h.tenants.FindByID(ctx, tenantID)
	if err != nil {
		return TenantProfileView{}, err
	}
	if tenant.State() != domain.TenantActive {
		return TenantProfileView{}, domain.ErrTenantDisabled
	}
	profile := tenant.Profile()
	services := make([]ServiceView, 0, len(profile.Services))
	for _, service := range profile.Services {
		services = append(services, ServiceView{Name: service.Name, Short: service.Short, Full: service.Full})
	}
	return TenantProfileView{
		TenantID:      tenant.ID().String(),
		CompanyName:   tenant.CompanyName(),
		Website:       tenant.Website(),
		ProfileStatus: string(tenant.ProfileStatus()),
		Description:   profile.Description,
		Target:        profile.Target,
		Services:      services,
	}, nil
}
