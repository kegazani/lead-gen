package query

import (
	"context"

	"lead-gen/backend/contexts/community-intelligence/internal/domain"
	"lead-gen/backend/contexts/community-intelligence/internal/ports"
)

// ListDigests — запрос выжимок сообщества.
type ListDigests struct {
	CommunityID domain.CommunityID
	TenantID    domain.TenantID
}

// ListDigestsHandler — use case чтения списка выжимок сообщества.
type ListDigestsHandler struct {
	digests ports.DigestRepository
}

// NewListDigestsHandler собирает use case из хранилища выжимок.
func NewListDigestsHandler(digests ports.DigestRepository) *ListDigestsHandler {
	return &ListDigestsHandler{digests: digests}
}

// Handle возвращает обезличенные выжимки сообщества в порядке построения. Выжимки
// других тенантов отсеиваются.
func (h *ListDigestsHandler) Handle(ctx context.Context, q ListDigests) ([]DigestView, error) {
	stored, err := h.digests.ListByCommunity(ctx, q.CommunityID)
	if err != nil {
		return nil, err
	}
	views := make([]DigestView, 0, len(stored))
	for _, digest := range stored {
		if q.TenantID != "" && digest.TenantID() != q.TenantID {
			continue
		}
		views = append(views, ToView(digest))
	}
	return views, nil
}
