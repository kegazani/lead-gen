package command

import (
	"context"

	"lead-gen/backend/contexts/publishing/internal/domain"
	"lead-gen/backend/contexts/publishing/internal/ports"
)

// PublicationState — краткое состояние публикации, возвращаемое сценариями
// изменения. Полное чтение даёт сценарий query.
type PublicationState struct {
	ID        domain.PublicationID
	Target    domain.PublicationTarget
	Status    domain.PublicationStatus
	Shortened bool
}

// stateOf строит состояние из агрегата.
func stateOf(p *domain.Publication) PublicationState {
	return PublicationState{
		ID:        p.ID(),
		Target:    p.Target(),
		Status:    p.Status(),
		Shortened: p.Material().Shortened(),
	}
}

// loadForTenant загружает публикацию и сверяет тенанта. Отсутствие публикации
// даёт ошибку порта, чужой тенант — ErrTenantMismatch; существование чужой
// записи не раскрывается вызывающему.
func loadForTenant(ctx context.Context, tx ports.Transaction, id domain.PublicationID, tenant domain.TenantID) (*domain.Publication, error) {
	publication, err := tx.Publications().FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if publication.TenantID() != tenant {
		return nil, ErrTenantMismatch
	}
	return publication, nil
}
