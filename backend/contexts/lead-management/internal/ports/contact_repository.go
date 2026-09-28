package ports

import (
	"context"

	"lead-gen/backend/contexts/lead-management/internal/domain"
)

// ContactRepository — репозиторий контактов (красная зона).
type ContactRepository interface {
	// Save сохраняет контакт (создаёт или дополняет).
	Save(ctx context.Context, contact *domain.Contact) error
	// FindByID возвращает контакт по идентификатору.
	FindByID(ctx context.Context, id domain.ContactID) (*domain.Contact, error)
	// ListByCompany возвращает контакты компании.
	ListByCompany(ctx context.Context, companyID domain.CompanyID) ([]*domain.Contact, error)
	// ListByCompanies возвращает контакты набора компаний одним пакетным чтением
	// (без запроса на каждую компанию). Пустой набор даёт пустой список.
	ListByCompanies(ctx context.Context, companyIDs []domain.CompanyID) ([]*domain.Contact, error)
}
