package query

import (
	"context"
	"sort"

	"lead-gen/backend/contexts/publishing/internal/domain"
	"lead-gen/backend/contexts/publishing/internal/ports"
)

// PublicationQueue — запрос очереди публикаций в заданном статусе. Обслуживает
// экран утверждения (draft) и очередь исполнения (approved).
type PublicationQueue struct {
	TenantID domain.TenantID
	Status   domain.PublicationStatus
}

// PublicationQueueHandler — сценарий чтения очереди.
type PublicationQueueHandler struct {
	repo ports.PublicationRepository
}

// NewPublicationQueueHandler собирает сценарий чтения очереди.
func NewPublicationQueueHandler(repo ports.PublicationRepository) *PublicationQueueHandler {
	return &PublicationQueueHandler{repo: repo}
}

// Handle возвращает проекции публикаций тенанта в заданном статусе, упорядоченные
// по идентификатору.
func (h *PublicationQueueHandler) Handle(ctx context.Context, q PublicationQueue) ([]PublicationView, error) {
	publications, err := h.repo.ListByStatus(ctx, q.Status)
	if err != nil {
		return nil, err
	}
	views := make([]PublicationView, 0, len(publications))
	for _, publication := range publications {
		if publication.TenantID() != q.TenantID {
			continue
		}
		views = append(views, viewOf(publication))
	}
	sort.Slice(views, func(i, j int) bool { return views[i].ID < views[j].ID })
	return views, nil
}
