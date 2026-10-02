package query

import (
	"context"
	"time"

	"lead-gen/backend/contexts/data-governance/internal/domain"
	"lead-gen/backend/contexts/data-governance/internal/ports"
)

// AuthorityView — снимок действующего полномочия тенанта для цели и категории.
type AuthorityView struct {
	BasisID       string
	TenantID      string
	Purpose       string
	Category      domain.DataCategory
	Authorized    bool
	RetentionDays int
	ValidFrom     time.Time
	ValidUntil    time.Time
	Status        domain.BasisStatus
}

// CheckAuthority — запрос действующего полномочия.
type CheckAuthority struct {
	TenantID domain.TenantID
	Purpose  string
	Category domain.DataCategory
	At       time.Time
}

// CheckAuthorityHandler — use case проверки полномочия.
type CheckAuthorityHandler struct {
	repo  ports.ProcessingBasisRepository
	clock ports.Clock
}

// NewCheckAuthorityHandler собирает use case.
func NewCheckAuthorityHandler(repo ports.ProcessingBasisRepository, clock ports.Clock) *CheckAuthorityHandler {
	return &CheckAuthorityHandler{repo: repo, clock: clock}
}

// Handle ищет действующее полномочие тенанта для цели и категории. Отсутствие
// полномочия — не ошибка: решение возвращается с Authorized=false.
func (h *CheckAuthorityHandler) Handle(ctx context.Context, q CheckAuthority) (AuthorityView, error) {
	at := q.At
	if at.IsZero() && h.clock != nil {
		at = h.clock.Now()
	}
	purpose, err := domain.NewProcessingPurpose(q.Purpose, q.Category)
	if err != nil {
		return AuthorityView{}, err
	}
	if err := requireRef("tenant_id", q.TenantID.String()); err != nil {
		return AuthorityView{}, err
	}

	bases, err := h.repo.FindByTenant(ctx, q.TenantID)
	if err != nil {
		return AuthorityView{}, err
	}
	for _, basis := range bases {
		if !basis.Authorizes(purpose, at) {
			continue
		}
		return AuthorityView{
			BasisID:       basis.ID().String(),
			TenantID:      basis.TenantID().String(),
			Purpose:       basis.Purpose().Name(),
			Category:      basis.Purpose().Category(),
			Authorized:    true,
			RetentionDays: basis.Retention().Days(),
			ValidFrom:     basis.ValidFrom(),
			ValidUntil:    basis.ValidUntil(),
			Status:        basis.Status(),
		}, nil
	}
	return AuthorityView{
		TenantID:   q.TenantID.String(),
		Purpose:    q.Purpose,
		Category:   q.Category,
		Authorized: false,
	}, nil
}
