package query

import (
	"context"
	"time"

	"lead-gen/backend/contexts/publishing/internal/domain"
	"lead-gen/backend/contexts/publishing/internal/ports"
)

// PublicationView — проекция публикации для чтения.
type PublicationView struct {
	ID            domain.PublicationID
	TenantID      domain.TenantID
	Digest        domain.DigestRef
	Platform      domain.Platform
	Account       string
	Title         string
	Body          string
	Shortened     bool
	Status        domain.PublicationStatus
	Address       string
	FailureReason string
	ApprovedBy    domain.ActorID
	ApprovedAt    time.Time
	PublishedAt   time.Time
	Version       int
}

// viewOf строит проекцию из агрегата.
func viewOf(p *domain.Publication) PublicationView {
	target := p.Target()
	view := PublicationView{
		ID:        p.ID(),
		TenantID:  p.TenantID(),
		Digest:    p.Digest(),
		Platform:  target.Platform(),
		Account:   target.Account(),
		Title:     p.Material().Title(),
		Body:      p.Material().Body(),
		Shortened: p.Material().Shortened(),
		Status:    p.Status(),
		Version:   p.Version(),
	}
	if approval, ok := p.Approval(); ok {
		view.ApprovedBy = approval.Actor
		view.ApprovedAt = approval.At
	}
	if outcome, ok := p.Outcome(); ok {
		view.Address = outcome.Address
		view.FailureReason = outcome.FailureReason
		view.PublishedAt = outcome.At
	}
	return view
}

// GetPublication — запрос публикации по идентификатору.
type GetPublication struct {
	PublicationID domain.PublicationID
	TenantID      domain.TenantID
}

// GetPublicationHandler — сценарий чтения публикации.
type GetPublicationHandler struct {
	repo ports.PublicationRepository
}

// NewGetPublicationHandler собирает сценарий чтения.
func NewGetPublicationHandler(repo ports.PublicationRepository) *GetPublicationHandler {
	return &GetPublicationHandler{repo: repo}
}

// Handle возвращает проекцию публикации. Публикация чужого тенанта неотличима от
// отсутствующей: возвращается ErrPublicationNotFound.
func (h *GetPublicationHandler) Handle(ctx context.Context, q GetPublication) (PublicationView, error) {
	publication, err := h.repo.FindByID(ctx, q.PublicationID)
	if err != nil {
		return PublicationView{}, err
	}
	if publication.TenantID() != q.TenantID {
		return PublicationView{}, ports.ErrPublicationNotFound
	}
	return viewOf(publication), nil
}
