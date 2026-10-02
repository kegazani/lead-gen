package query

import (
	"context"
	"time"

	"lead-gen/backend/contexts/data-governance/internal/domain"
	"lead-gen/backend/contexts/data-governance/internal/ports"
)

// SuppressionView — результат проверки подавления контакта.
type SuppressionView struct {
	// Suppressed — для контакта действует активная запись стоп-листа.
	Suppressed bool
	// Reason — причина внесения в стоп-лист.
	Reason string
	// SuppressionID — идентификатор действующей записи.
	SuppressionID string
	// BasisID — основание, в составе которого хранится запись.
	BasisID string
	// Since — момент внесения в стоп-лист.
	Since time.Time
}

// CheckSuppression — запрос проверки подавления контакта в пределах тенанта.
type CheckSuppression struct {
	TenantID domain.TenantID
	Contact  domain.ContactRef
}

// CheckSuppressionHandler — use case проверки подавления.
type CheckSuppressionHandler struct {
	repo ports.ProcessingBasisRepository
}

// NewCheckSuppressionHandler собирает use case.
func NewCheckSuppressionHandler(repo ports.ProcessingBasisRepository) *CheckSuppressionHandler {
	return &CheckSuppressionHandler{repo: repo}
}

// Handle ищет активную запись стоп-листа для контакта по всем основаниям тенанта.
// Записи стоп-листа входят в состав оснований по каталогу, поэтому проверка
// собирается по тенанту; первая найденная активная запись и есть решение —
// инвариант I2 делает их равнозначными.
func (h *CheckSuppressionHandler) Handle(ctx context.Context, q CheckSuppression) (SuppressionView, error) {
	if err := requireRef("tenant_id", q.TenantID.String()); err != nil {
		return SuppressionView{}, err
	}
	if err := requireRef("contact", q.Contact.String()); err != nil {
		return SuppressionView{}, err
	}
	bases, err := h.repo.FindByTenant(ctx, q.TenantID)
	if err != nil {
		return SuppressionView{}, err
	}
	for _, basis := range bases {
		entry, ok := basis.IsSuppressed(q.Contact)
		if !ok {
			continue
		}
		return SuppressionView{
			Suppressed:    true,
			Reason:        entry.Reason(),
			SuppressionID: entry.ID().String(),
			BasisID:       basis.ID().String(),
			Since:         entry.CreatedAt(),
		}, nil
	}
	return SuppressionView{Suppressed: false}, nil
}
