package command

import (
	"context"

	"lead-gen/backend/contexts/audience/internal/domain"
	"lead-gen/backend/contexts/audience/internal/ports"
)

// CreateSegment — команда создания сегмента целевой аудитории.
type CreateSegment struct {
	SegmentID       domain.SegmentID
	TenantID        domain.TenantID
	Name            string
	ModelVersion    domain.ModelVersion
	MinScore        float64
	RequiredSignals []string
	RequireAll      bool
}

// CreateSegmentResult — результат создания сегмента.
type CreateSegmentResult struct {
	SegmentID domain.SegmentID
	Version   int
}

// CreateSegmentHandler — use case создания сегмента.
type CreateSegmentHandler struct {
	uow ports.UnitOfWork
}

// NewCreateSegmentHandler собирает use case из порта транзакции.
func NewCreateSegmentHandler(uow ports.UnitOfWork) *CreateSegmentHandler {
	return &CreateSegmentHandler{uow: uow}
}

// Handle создаёт сегмент и сохраняет его. Создание сегмента доменного события не
// производит: каталог объявляет для audience-segment только изменение состава.
func (h *CreateSegmentHandler) Handle(ctx context.Context, cmd CreateSegment) (CreateSegmentResult, error) {
	minScore, err := domain.NewScoreValue(cmd.MinScore)
	if err != nil {
		return CreateSegmentResult{}, err
	}
	criteria, err := domain.NewSegmentCriteria(minScore, cmd.RequiredSignals, cmd.RequireAll)
	if err != nil {
		return CreateSegmentResult{}, err
	}
	segment, err := domain.NewSegment(domain.NewSegmentParams{
		ID:           cmd.SegmentID,
		TenantID:     cmd.TenantID,
		Name:         cmd.Name,
		ModelVersion: cmd.ModelVersion,
		Criteria:     criteria,
	})
	if err != nil {
		return CreateSegmentResult{}, err
	}

	if err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		return tx.Segments().Save(ctx, segment, 0)
	}); err != nil {
		return CreateSegmentResult{}, err
	}
	return CreateSegmentResult{SegmentID: segment.ID(), Version: segment.Version()}, nil
}
