package requestfulfillment

import (
	"context"
	"errors"
	"fmt"

	"lead-gen/backend/contexts/request-fulfillment/internal/app/command"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/query"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/contracts/request-fulfillment/requestintent"
)

// NewRequestIntent собирает опубликованный контракт request-intent поверх
// собранного среза заявки: команду подачи и чтение состояния.
//
// Это публичная фабрика владельца заявки: точка сборки (cmd/lead-gen) не имеет
// доступа к внутренним пакетам контекста, поэтому прямой адаптер контракта
// объявлен здесь. При co-location он вызывается в процессе; при разделении
// сервисов та же форма даётся сетевым клиентом.
func NewRequestIntent(slice *Slice) requestintent.Port {
	if slice == nil {
		return requestintent.InProc(requestintent.InProcHandler{})
	}
	return requestintent.InProc(requestintent.InProcHandler{
		SubmitFunc: func(ctx context.Context, in requestintent.SubmitIntent) (requestintent.SubmitResult, error) {
			result, err := slice.SubmitRequest.Handle(ctx, command.SubmitRequest{
				RequestID:     domain.RequestID(in.RequestID),
				OfferID:       domain.OfferID(in.OfferID),
				TenantID:      domain.TenantID(in.TenantID),
				Content:       in.Content,
				Target:        in.Target,
				Actor:         domain.ActorID(in.Actor),
				CorrelationID: domain.CorrelationID(in.CorrelationID),
			})
			if err != nil {
				return requestintent.SubmitResult{}, requestIntentError(err)
			}
			return requestintent.SubmitResult{
				RequestID: result.RequestID.String(),
				Status:    string(result.Status),
			}, nil
		},
		StatusFunc: func(ctx context.Context, q requestintent.StatusQuery) (requestintent.StatusResult, error) {
			view, err := slice.GetRequest.Handle(ctx, query.GetRequest{
				RequestID: domain.RequestID(q.RequestID),
				TenantID:  domain.TenantID(q.TenantID),
			})
			if err != nil {
				return requestintent.StatusResult{}, requestIntentError(err)
			}
			return requestintent.StatusResult{
				RequestID:     view.RequestID,
				Status:        string(view.Status),
				OfferContent:  view.Offer.Content,
				OfferTarget:   view.Offer.Target,
				OfferRevision: view.Offer.Revision,
			}, nil
		},
	})
}

// requestIntentError переводит ошибку сценария заявки в исход опубликованного
// контракта: отсутствие заявки (в том числе чужого тенанта) — ErrNotFound,
// доменный отказ — ErrRejected, прочее — ErrUnavailable. Ошибка не выдаётся за
// успех и не подменяется выдуманным состоянием.
func requestIntentError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ports.ErrRequestNotFound):
		return fmt.Errorf("%w: %v", requestintent.ErrNotFound, err)
	case errors.Is(err, domain.ErrOfferRequired),
		errors.Is(err, domain.ErrEmptyOfferContent),
		errors.Is(err, domain.ErrEmptyID),
		errors.Is(err, domain.ErrMissingActor),
		errors.Is(err, domain.ErrMissingTime):
		return fmt.Errorf("%w: %v", requestintent.ErrRejected, err)
	default:
		return fmt.Errorf("%w: %v", requestintent.ErrUnavailable, err)
	}
}
