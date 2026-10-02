package query

import (
	"context"
	"fmt"
	"time"

	"lead-gen/backend/contexts/audience/internal/domain"
	"lead-gen/backend/contexts/audience/internal/ports"
)

// GetAudienceSnapshot — запрос снимка аудитории по идентификатору.
type GetAudienceSnapshot struct {
	SnapshotID domain.SnapshotID
	TenantID   domain.TenantID
}

// SnapshotView — представление снимка аудитории для чтения. Members — ссылки на
// лидов: персональные данные через границу не проходят, состав разрешается у
// владельца.
type SnapshotView struct {
	SnapshotID     string
	SegmentID      string
	TenantID       string
	SegmentVersion int
	ModelVersion   string
	MemberCount    int
	Version        int64
	GeneratedAt    time.Time
	Members        []string
}

// GetAudienceSnapshotHandler — use case чтения снимка аудитории.
type GetAudienceSnapshotHandler struct {
	snapshots ports.AudienceSnapshotRepository
}

// NewGetAudienceSnapshotHandler собирает use case чтения.
func NewGetAudienceSnapshotHandler(snapshots ports.AudienceSnapshotRepository) *GetAudienceSnapshotHandler {
	return &GetAudienceSnapshotHandler{snapshots: snapshots}
}

// Handle загружает снимок и строит представление. Снимок другого тенанта
// неотличим от отсутствующего.
func (h *GetAudienceSnapshotHandler) Handle(ctx context.Context, q GetAudienceSnapshot) (SnapshotView, error) {
	snapshot, err := h.snapshots.FindByID(ctx, q.SnapshotID)
	if err != nil {
		return SnapshotView{}, err
	}
	if snapshot.TenantID() != q.TenantID {
		return SnapshotView{}, fmt.Errorf("%w: %s", ports.ErrSnapshotNotFound, q.SnapshotID)
	}

	members := snapshot.MemberIDs()
	refs := make([]string, 0, len(members))
	for _, id := range members {
		refs = append(refs, id.String())
	}
	return SnapshotView{
		SnapshotID:     snapshot.ID().String(),
		SegmentID:      snapshot.SegmentID().String(),
		TenantID:       snapshot.TenantID().String(),
		SegmentVersion: snapshot.SegmentVersion(),
		ModelVersion:   snapshot.ModelVersion().String(),
		MemberCount:    snapshot.MemberCount(),
		Version:        snapshot.Version(),
		GeneratedAt:    snapshot.GeneratedAt().UTC(),
		Members:        refs,
	}, nil
}
