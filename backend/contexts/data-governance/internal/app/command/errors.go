package command

import (
	"context"
	"errors"
	"fmt"

	"lead-gen/backend/contexts/data-governance/internal/domain"
	"lead-gen/backend/contexts/data-governance/internal/ports"
)

// ErrTenantMismatch — основание принадлежит другому тенанту. Сценарий не
// раскрывает существование чужого основания: вызывающий получает отказ.
var ErrTenantMismatch = errors.New("сценарий: основание принадлежит другому тенанту")

// BasisResult — результат команды над основанием: новая версия агрегата и
// события, произведённые изменением.
type BasisResult struct {
	BasisID domain.BasisID
	Status  domain.BasisStatus
	Version int
	Events  []domain.DomainEvent
}

// loadForChange загружает основание и сверяет тенант, возвращая ожидаемую версию
// для оптимистичной блокировки. Ошибка чужого тенанта неотличима от отсутствия
// основания по смыслу, но различается типом для HTTP-слоя.
func loadForChange(
	ctx context.Context,
	repo ports.ProcessingBasisRepository,
	id domain.BasisID,
	tenant domain.TenantID,
) (*domain.ProcessingBasis, int, error) {
	basis, err := repo.FindByID(ctx, id)
	if err != nil {
		return nil, 0, err
	}
	if basis.TenantID() != tenant {
		return nil, 0, fmt.Errorf("%w: основание %s", ErrTenantMismatch, id)
	}
	return basis, basis.Version(), nil
}

// result собирает результат команды, вычитывая буфер событий агрегата.
func result(basis *domain.ProcessingBasis, events []domain.DomainEvent) BasisResult {
	basis.PullDomainEvents()
	return BasisResult{
		BasisID: basis.ID(),
		Status:  basis.Status(),
		Version: basis.Version(),
		Events:  events,
	}
}
