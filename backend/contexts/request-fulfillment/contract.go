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
				RequestID:        domain.RequestID(in.RequestID),
				OfferID:          domain.OfferID(in.OfferID),
				TenantID:         domain.TenantID(in.TenantID),
				Title:            in.Title,
				Content:          in.Content,
				Target:           in.Target,
				ExecutionPurpose: in.ExecutionPurpose,
				SenderCompany:    senderCompanyDomain(in.SenderCompany),
				Actor:            domain.ActorID(in.Actor),
				CorrelationID:    domain.CorrelationID(in.CorrelationID),
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
				Title:         view.Title,
				OfferContent:  view.Offer.Content,
				OfferTarget:   view.Offer.Target,
				OfferRevision: view.Offer.Revision,
				Phase:         view.Phase,
				ErrorReason:   view.ErrorReason,
				ErrorMessage:  view.ErrorMessage,
			}, nil
		},
		ListFunc: func(ctx context.Context, q requestintent.ListQuery) ([]requestintent.ListEntry, error) {
			items, err := slice.ListRequests.Handle(ctx, query.ListRequests{
				TenantID: domain.TenantID(q.TenantID),
			})
			if err != nil {
				return nil, requestIntentError(err)
			}
			entries := make([]requestintent.ListEntry, 0, len(items))
			for _, item := range items {
				entries = append(entries, requestintent.ListEntry{
					RequestID: item.RequestID,
					Title:     item.Title,
					Status:    string(item.Status),
				})
			}
			return entries, nil
		},
	})
}

func senderCompanyDomain(snapshot *requestintent.SenderCompanySnapshot) *domain.SenderCompanySnapshot {
	if snapshot == nil {
		return nil
	}
	result := &domain.SenderCompanySnapshot{
		CompanyName: snapshot.CompanyName, Website: snapshot.Website,
		Description: snapshot.Description,
		Services:    make([]domain.SenderServiceSnapshot, len(snapshot.Services)),
	}
	for i, service := range snapshot.Services {
		result.Services[i] = domain.SenderServiceSnapshot{Name: service.Name, Short: service.Short, Full: service.Full}
	}
	return result
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
		errors.Is(err, domain.ErrRequestSubmissionConflict),
		errors.Is(err, domain.ErrInvalidRequestTitle),
		errors.Is(err, domain.ErrEmptyOfferContent),
		errors.Is(err, domain.ErrEmptyID),
		errors.Is(err, domain.ErrMissingActor),
		errors.Is(err, domain.ErrMissingTime),
		errors.Is(err, domain.ErrRequestLimitReached),
		errors.Is(err, domain.ErrActiveRequestLimitReached):
		return fmt.Errorf("%w: %v", requestintent.ErrRejected, err)
	default:
		return fmt.Errorf("%w: %v", requestintent.ErrUnavailable, err)
	}
}
