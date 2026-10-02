package query

import (
	"context"
	"time"

	"lead-gen/backend/contexts/data-governance/internal/domain"
	"lead-gen/backend/contexts/data-governance/internal/ports"
)

// ZoneView — результат проверки политики зон для тенанта: действует ли
// полномочие и как обращаться с данными категории.
type ZoneView struct {
	// Authorized — у тенанта есть действующее полномочие для цели и категории.
	Authorized bool
	// Category — категория данных запроса.
	Category domain.DataCategory
	// Handling — требуемый политикой режим обращения с данными.
	Handling domain.Handling
	// SharedStorageAllowed — допустим ли вынос в общее хранилище.
	SharedStorageAllowed bool
	// Minimize — требуется ли минимизация передаваемых данных.
	Minimize bool
	// Reason — основание правила.
	Reason string
	// BasisID — основание, давшее полномочие (пусто, если полномочия нет).
	BasisID string
}

// CheckZone — запрос проверки политики зон и действия полномочия.
type CheckZone struct {
	TenantID domain.TenantID
	Purpose  string
	Category domain.DataCategory
	// At — момент проверки; пусто — текущее время часов.
	At time.Time
}

// CheckZoneHandler — use case проверки политики зон.
type CheckZoneHandler struct {
	repo  ports.ProcessingBasisRepository
	clock ports.Clock
}

// NewCheckZoneHandler собирает use case.
func NewCheckZoneHandler(repo ports.ProcessingBasisRepository, clock ports.Clock) *CheckZoneHandler {
	return &CheckZoneHandler{repo: repo, clock: clock}
}

// Handle проверяет, действует ли полномочие тенанта для цели и категории, и
// применяет чистую политику data-zone-policy. Отсутствие действующего полномочия
// — не ошибка: решение возвращается с Authorized=false.
func (h *CheckZoneHandler) Handle(ctx context.Context, q CheckZone) (ZoneView, error) {
	at := q.At
	if at.IsZero() && h.clock != nil {
		at = h.clock.Now()
	}
	purpose, err := domain.NewProcessingPurpose(q.Purpose, q.Category)
	if err != nil {
		return ZoneView{}, err
	}
	rule, err := domain.DataZonePolicy(purpose)
	if err != nil {
		return ZoneView{}, err
	}

	view := ZoneView{
		Category:             rule.Category,
		Handling:             rule.Handling,
		SharedStorageAllowed: rule.SharedStorageAllowed,
		Minimize:             rule.Minimize,
		Reason:               rule.Reason,
	}

	bases, err := h.repo.FindByTenant(ctx, q.TenantID)
	if err != nil {
		return ZoneView{}, err
	}
	for _, basis := range bases {
		if basis.Authorizes(purpose, at) {
			view.Authorized = true
			view.BasisID = basis.ID().String()
			return view, nil
		}
	}
	return view, nil
}
