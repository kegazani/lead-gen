package query

import (
	"context"
	"fmt"
	"time"

	"lead-gen/backend/contexts/audience/internal/domain"
	"lead-gen/backend/contexts/audience/internal/ports"
)

// SegmentMembers — запрос состава сегмента с оценками.
type SegmentMembers struct {
	SegmentID domain.SegmentID
	TenantID  domain.TenantID
}

// MemberView — оценённый лид сегмента.
type MemberView struct {
	LeadID       string
	Score        float64
	Eligible     bool
	ModelVersion string
	ComputedAt   string
}

// SegmentView — представление сегмента: идентификация, версия и состав с оценками.
type SegmentView struct {
	SegmentID     string
	TenantID      string
	Name          string
	ModelVersion  string
	Version       int
	MemberCount   int
	LastChangedAt time.Time
	Members       []MemberView
}

// SegmentMembersHandler — use case чтения состава сегмента.
type SegmentMembersHandler struct {
	segments ports.SegmentRepository
}

// NewSegmentMembersHandler собирает use case чтения.
func NewSegmentMembersHandler(segments ports.SegmentRepository) *SegmentMembersHandler {
	return &SegmentMembersHandler{segments: segments}
}

// Handle загружает сегмент и строит представление его состава. Сегмент другого
// тенанта неотличим от отсутствующего.
func (h *SegmentMembersHandler) Handle(ctx context.Context, q SegmentMembers) (SegmentView, error) {
	segment, err := h.segments.FindByID(ctx, q.SegmentID)
	if err != nil {
		return SegmentView{}, err
	}
	if segment.TenantID() != q.TenantID {
		return SegmentView{}, fmt.Errorf("%w: %s", ports.ErrSegmentNotFound, q.SegmentID)
	}

	members := make([]MemberView, 0)
	for _, score := range segment.LeadScores() {
		if !segment.IsMember(score.LeadID()) {
			continue
		}
		members = append(members, MemberView{
			LeadID:       score.LeadID().String(),
			Score:        score.Value().Float64(),
			Eligible:     true,
			ModelVersion: score.ModelVersion().String(),
			ComputedAt:   score.ComputedAt().UTC().Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	return SegmentView{
		SegmentID:     segment.ID().String(),
		TenantID:      segment.TenantID().String(),
		Name:          segment.Name(),
		ModelVersion:  segment.ModelVersion().String(),
		Version:       segment.Version(),
		MemberCount:   len(members),
		LastChangedAt: segment.LastChangedAt().UTC(),
		Members:       members,
	}, nil
}
