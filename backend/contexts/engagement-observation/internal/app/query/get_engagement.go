package query

import (
	"context"
	"time"

	"lead-gen/backend/contexts/engagement-observation/internal/domain"
	"lead-gen/backend/contexts/engagement-observation/internal/ports"
)

// GetEngagement — запрос одного события взаимодействия. TenantID обязателен:
// чтение события другого тенанта завершается «не найдено», и существование
// чужого события этим не раскрывается.
type GetEngagement struct {
	ID       string
	TenantID string
}

// EngagementView — представление события взаимодействия для чтения. Пиксельных
// метрик и признака открытия здесь нет.
type EngagementView struct {
	ID            string
	TenantID      string
	CompanyID     string
	Kind          string
	Confidence    string
	Outcome       string
	LinkRef       string
	Target        string
	OccurredAt    time.Time
	CorrelationID string
	Version       int
}

// GetEngagementHandler — use case чтения одного события.
type GetEngagementHandler struct {
	repo ports.EngagementRepository
}

// NewGetEngagementHandler собирает use case поверх хранилища событий.
func NewGetEngagementHandler(repo ports.EngagementRepository) *GetEngagementHandler {
	return &GetEngagementHandler{repo: repo}
}

// Handle загружает событие и проверяет принадлежность тенанту.
func (h *GetEngagementHandler) Handle(ctx context.Context, q GetEngagement) (EngagementView, error) {
	event, err := h.repo.FindByID(ctx, domain.EngagementEventID(q.ID))
	if err != nil {
		return EngagementView{}, err
	}
	if string(event.TenantID()) != q.TenantID {
		return EngagementView{}, ports.ErrEventNotFound
	}
	return EngagementView{
		ID:            string(event.ID()),
		TenantID:      string(event.TenantID()),
		CompanyID:     string(event.CompanyID()),
		Kind:          string(event.Kind()),
		Confidence:    string(event.Confidence()),
		Outcome:       string(event.Outcome()),
		LinkRef:       string(event.LinkRef()),
		Target:        event.Target(),
		OccurredAt:    event.OccurredAt(),
		CorrelationID: string(event.CorrelationID()),
		Version:       event.Version(),
	}, nil
}
