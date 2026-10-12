package query

import (
	"context"
	"time"

	"lead-gen/backend/contexts/platform-governance/internal/ports"
)

// ListTenants — запрос списка тенантов для админ-консоли.
//
// Параметров нет сознательно: список не адресуется конкретному тенанту, иначе
// администратор не обнаружил бы нужный тенант, а увидел бы только уже известный.
type ListTenants struct{}

// TenantView — строка списка тенантов для экрана выбора и проверки заявок.
//
// Платформенные атрибуты и идентифицирующие данные компании: оператору нужно
// проверить заявку на регистрацию и согласовать профиль. Логины и контакты
// пользователей в представление не попадают — экран проверяет компанию, а не её
// людей.
type TenantView struct {
	ID            string
	CompanyName   string
	State         string
	Description   string
	INN           string
	CompanyEmail  string
	Website       string
	LegalData     string
	ProfileStatus string
	CreatedAt     time.Time
}

// ListTenantsHandler — use case чтения списка тенантов.
type ListTenantsHandler struct {
	directory ports.TenantDirectory
}

// NewListTenantsHandler собирает use case из порта чтения списка тенантов.
func NewListTenantsHandler(directory ports.TenantDirectory) *ListTenantsHandler {
	return &ListTenantsHandler{directory: directory}
}

// Handle возвращает тенанты в порядке источника. Пустой список — валидный
// результат (тенантов ещё нет), а не ошибка: экран покажет честное «пока нет», а
// не отказ.
func (h *ListTenantsHandler) Handle(ctx context.Context, _ ListTenants) ([]TenantView, error) {
	summaries, err := h.directory.List(ctx)
	if err != nil {
		return nil, err
	}
	views := make([]TenantView, 0, len(summaries))
	for _, summary := range summaries {
		views = append(views, TenantView{
			ID:            summary.ID.String(),
			CompanyName:   summary.CompanyName,
			State:         string(summary.State),
			Description:   summary.Description,
			INN:           summary.INN,
			CompanyEmail:  summary.CompanyEmail,
			Website:       summary.Website,
			LegalData:     summary.LegalData,
			ProfileStatus: string(summary.ProfileStatus),
			CreatedAt:     summary.CreatedAt,
		})
	}
	return views, nil
}
