package command

import (
	"context"
	"fmt"
	"time"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

// TransitionResult — результат перехода статуса: новая версия агрегата и
// события, записанные в outbox в той же транзакции.
type TransitionResult struct {
	RequestID domain.RequestID
	Status    domain.RequestStatus
	Version   int
	Events    []domain.DomainEvent
}

// commandKey — общая часть команды над существующей заявкой: над какой заявкой и
// в каком тенанте совершается действие.
type commandKey struct {
	RequestID domain.RequestID
	TenantID  domain.TenantID
}

// applyChange выполняет изменение существующей заявки в одной транзакции:
// загружает агрегат, сверяет тенант, фиксирует ожидаемую версию, применяет
// изменение, сохраняет агрегат с оптимистичной блокировкой и пишет события в
// outbox. Ошибка изменения откатывает транзакцию — состояние не меняется.
func applyChange(
	ctx context.Context,
	uow ports.UnitOfWork,
	key commandKey,
	change func(request *domain.Request) ([]domain.DomainEvent, error),
) (TransitionResult, error) {
	var result TransitionResult
	err := uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		request, err := tx.Requests().FindByID(ctx, key.RequestID)
		if err != nil {
			return err
		}
		if request.TenantID() != key.TenantID {
			return fmt.Errorf("%w: заявка %s", ErrTenantMismatch, key.RequestID)
		}
		expected := request.Version()
		events, err := change(request)
		if err != nil {
			return err
		}
		if err := tx.Requests().Save(ctx, request, expected); err != nil {
			return err
		}
		if len(events) > 0 {
			if err := tx.Outbox().Append(ctx, events...); err != nil {
				return err
			}
		}
		request.PullDomainEvents()
		result = TransitionResult{
			RequestID: request.ID(),
			Status:    request.Status(),
			Version:   request.Version(),
			Events:    events,
		}
		return nil
	})
	if err != nil {
		return TransitionResult{}, err
	}
	return result, nil
}

// transitionInput собирает вход перехода из команды, подставляя время часов,
// если команда его не задала, и идентификатор события из генератора.
func transitionInput(
	actor domain.ActorID,
	at time.Time,
	reason string,
	clock ports.Clock,
	ids ports.IDGenerator,
	correlation domain.CorrelationID,
	causation domain.CausationID,
) domain.TransitionInput {
	if at.IsZero() && clock != nil {
		at = clock.Now()
	}
	return domain.TransitionInput{
		Actor:         actor,
		At:            at,
		Reason:        reason,
		EventID:       ids.NewEventID(),
		CorrelationID: correlation,
		CausationID:   causation,
	}
}
