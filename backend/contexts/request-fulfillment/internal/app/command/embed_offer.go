package command

import (
	"context"
	"fmt"
	"strings"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/contracts/integration-platform/vector"
)

// EmbedOffer — команда формирования эмбеддинга оффера заявки.
type EmbedOffer struct {
	RequestID domain.RequestID
	TenantID  domain.TenantID
}

// EmbedOfferResult — результат формирования эмбеддинга.
type EmbedOfferResult struct {
	RequestID     domain.RequestID
	OfferRevision int
	Dimensions    int
}

// EmbedOfferHandler — use case эмбеддинга оффера заявки.
//
// Вектор считается по текущему офферу и фиксируется в агрегате: это вход recall
// (semantic-lead-search), а не вердикт релевантности — близость векторов не
// присваивает заявке или компании вердикт «Подходит/Гипотеза/Не подходит».
//
// Текст оффера читается до расчёта, а фиксация идёт отдельной транзакцией с
// проверкой ревизии: если оффер успели изменить, устаревший вектор не записывается.
type EmbedOfferHandler struct {
	uow    ports.UnitOfWork
	vector vector.Port
}

// NewEmbedOfferHandler собирает use case из транзакции и векторного хранилища.
func NewEmbedOfferHandler(uow ports.UnitOfWork, vectorPort vector.Port) *EmbedOfferHandler {
	return &EmbedOfferHandler{uow: uow, vector: vectorPort}
}

// Handle формирует и фиксирует вектор оффера заявки.
func (h *EmbedOfferHandler) Handle(ctx context.Context, cmd EmbedOffer) (EmbedOfferResult, error) {
	if h.vector == nil {
		return EmbedOfferResult{}, ports.ErrVectorUnavailable
	}
	text, revision, err := h.offerText(ctx, cmd)
	if err != nil {
		return EmbedOfferResult{}, err
	}
	vec, err := h.vector.Embed(ctx, text)
	if err != nil {
		return EmbedOfferResult{}, fmt.Errorf("%w: %v", ports.ErrVectorUnavailable, err)
	}
	if len(vec) == 0 {
		return EmbedOfferResult{}, ports.ErrVectorUnavailable
	}

	transition, err := applyChange(ctx, h.uow, commandKey{RequestID: cmd.RequestID, TenantID: cmd.TenantID},
		nil,
		func(r *domain.Request) ([]domain.DomainEvent, error) {
			if r.Offer().Revision() != revision {
				return nil, fmt.Errorf("%w: оффер заявки %s изменён во время расчёта", ports.ErrVersionConflict, cmd.RequestID)
			}
			return nil, r.SetOfferEmbedding(vec)
		})
	if err != nil {
		return EmbedOfferResult{}, err
	}
	return EmbedOfferResult{
		RequestID:     transition.RequestID,
		OfferRevision: revision,
		Dimensions:    len(vec),
	}, nil
}

// offerText читает текст оффера и его ревизию, проверяя тенант. Изменений не
// вносит.
func (h *EmbedOfferHandler) offerText(ctx context.Context, cmd EmbedOffer) (string, int, error) {
	var text string
	var revision int
	err := h.uow.WithinTransaction(ctx, func(txCtx context.Context, tx ports.Transaction) error {
		request, err := tx.Requests().FindByID(txCtx, cmd.RequestID)
		if err != nil {
			return err
		}
		if request.TenantID() != cmd.TenantID {
			return fmt.Errorf("%w: заявка %s", ErrTenantMismatch, cmd.RequestID)
		}
		text = strings.TrimSpace(strings.Join([]string{
			request.Offer().Content().Text(),
			request.Offer().Target().Description(),
		}, " "))
		revision = request.Offer().Revision()
		return nil
	})
	if err != nil {
		return "", 0, err
	}
	return text, revision, nil
}
