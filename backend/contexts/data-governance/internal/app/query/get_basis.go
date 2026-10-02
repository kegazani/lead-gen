package query

import (
	"context"
	"fmt"
	"time"

	"lead-gen/backend/contexts/data-governance/internal/domain"
	"lead-gen/backend/contexts/data-governance/internal/ports"
)

// SuppressionEntryView — представление записи стоп-листа в составе основания.
type SuppressionEntryView struct {
	SuppressionID string
	Contact       string
	Reason        string
	Active        bool
	CreatedAt     string
	RevokedAt     string
}

// BasisView — представление основания обработки.
type BasisView struct {
	BasisID       string
	TenantID      string
	Purpose       string
	Category      domain.DataCategory
	RetentionDays int
	ValidFrom     string
	ValidUntil    string
	Status        domain.BasisStatus
	Version       int
	Suppressions  []SuppressionEntryView
}

// GetBasis — запрос основания по идентификатору.
type GetBasis struct {
	BasisID  domain.BasisID
	TenantID domain.TenantID
}

// GetBasisHandler — use case чтения основания.
type GetBasisHandler struct {
	repo ports.ProcessingBasisRepository
}

// NewGetBasisHandler собирает use case чтения.
func NewGetBasisHandler(repo ports.ProcessingBasisRepository) *GetBasisHandler {
	return &GetBasisHandler{repo: repo}
}

// Handle загружает основание и строит представление. Основание другого тенанта
// неотличимо от отсутствующего: его существование не раскрывается.
func (h *GetBasisHandler) Handle(ctx context.Context, q GetBasis) (BasisView, error) {
	basis, err := h.repo.FindByID(ctx, q.BasisID)
	if err != nil {
		return BasisView{}, err
	}
	if basis.TenantID() != q.TenantID {
		return BasisView{}, fmt.Errorf("%w: %s", ports.ErrBasisNotFound, q.BasisID)
	}
	return buildBasisView(basis), nil
}

// buildBasisView переводит агрегат в представление чтения.
func buildBasisView(basis *domain.ProcessingBasis) BasisView {
	view := BasisView{
		BasisID:       basis.ID().String(),
		TenantID:      basis.TenantID().String(),
		Purpose:       basis.Purpose().Name(),
		Category:      basis.Purpose().Category(),
		RetentionDays: basis.Retention().Days(),
		ValidFrom:     formatTime(basis.ValidFrom()),
		ValidUntil:    formatTime(basis.ValidUntil()),
		Status:        basis.Status(),
		Version:       basis.Version(),
	}
	for _, entry := range basis.Suppressions() {
		suppression := SuppressionEntryView{
			SuppressionID: entry.ID().String(),
			Contact:       entry.Contact().String(),
			Reason:        entry.Reason(),
			Active:        entry.IsActive(),
			CreatedAt:     formatTime(entry.CreatedAt()),
		}
		if revoked := entry.RevokedAt(); !revoked.IsZero() {
			suppression.RevokedAt = formatTime(revoked)
		}
		view.Suppressions = append(view.Suppressions, suppression)
	}
	return view
}

// formatTime приводит момент к формату RFC3339 в UTC.
func formatTime(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	return at.UTC().Format(time.RFC3339)
}
