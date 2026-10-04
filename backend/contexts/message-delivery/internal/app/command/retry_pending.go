package command

import (
	"context"
	"fmt"

	"lead-gen/backend/contexts/message-delivery/internal/domain"
	"lead-gen/backend/contexts/message-delivery/internal/ports"
)

// RetrySummary — итог одного прохода повторных окон.
type RetrySummary struct {
	// Attempted — число повторных отправок, инициированных проходом.
	Attempted int

	// Delivered — сколько повторных попыток завершилось доставкой.
	Delivered int

	// Failed — сколько повторных попыток снова завершилось отказом.
	Failed int

	// Skipped — сколько выбранных сообщений не удалось взять в повтор
	// (например, состояние изменилось между выборкой и фиксацией).
	Skipped int
}

// RetryPendingHandler — use case повторных окон доставки.
//
// Роль schedule периодически вызывает RetryDue: он выбирает сообщения, у
// которых последняя попытка завершилась временным исходом (недоступность
// провайдера), и повторяет отправку. Число попыток ограничено: сообщение с
// исчерпанным лимитом в повтор не берётся.
//
// Порядок шагов тот же, что и при первичной доставке: сначала сообщение
// возвращается в состояние sending и фиксируется (claim до отправки), затем
// выполняется отправка, затем исход записывается в отдельной транзакции. Так
// падение процесса между шагами не приводит к повторной отправке уже принятого
// провайдером письма.
type RetryPendingHandler struct {
	retryables  ports.RetryableMessages
	mailer      ports.Mailer
	clock       ports.Clock
	uow         ports.UnitOfWork
	maxAttempts int
}

// NewRetryPendingHandler собирает use case повторных окон. maxAttempts <= 0
// отключает повторные попытки.
func NewRetryPendingHandler(
	retryables ports.RetryableMessages,
	mailer ports.Mailer,
	clock ports.Clock,
	uow ports.UnitOfWork,
	maxAttempts int,
) *RetryPendingHandler {
	return &RetryPendingHandler{
		retryables:  retryables,
		mailer:      mailer,
		clock:       clock,
		uow:         uow,
		maxAttempts: maxAttempts,
	}
}

// RetryDue выполняет один проход: выбирает не более limit сообщений, требующих
// повтора, и повторяет отправку каждого. Ошибка возвращается только при отмене
// контекста или сбое выборки; отказ по отдельному сообщению не останавливает
// проход.
func (h *RetryPendingHandler) RetryDue(ctx context.Context, limit int) (RetrySummary, error) {
	var summary RetrySummary
	if h.maxAttempts <= 0 {
		return summary, nil
	}

	due, err := h.retryables.ListRetryable(ctx, h.maxAttempts, limit)
	if err != nil {
		return summary, fmt.Errorf("выбор сообщений для повтора: %w", err)
	}

	for _, message := range due {
		if ctx.Err() != nil {
			return summary, ctx.Err()
		}

		reopened, claimed, claimErr := h.reopen(ctx, message.ID())
		if claimErr != nil {
			if ctx.Err() != nil {
				return summary, ctx.Err()
			}
			// Сообщение остаётся в хранилище и будет рассмотрено следующей
			// попыткой; один сбой не останавливает проход.
			continue
		}
		if !claimed {
			summary.Skipped++
			continue
		}

		summary.Attempted++
		outcome, providerCode, sendErr := sendMessage(ctx, h.mailer, reopened)
		if sendErr != nil {
			return summary, sendErr
		}

		result, recordErr := recordDelivery(ctx, h.uow, h.clock, reopened.ID(), outcome, providerCode)
		if recordErr != nil {
			return summary, recordErr
		}
		if result.Status == domain.MessageStatusDelivered {
			summary.Delivered++
		} else {
			summary.Failed++
		}
	}
	return summary, nil
}

// reopen возвращает сообщение в состояние sending и фиксирует это до отправки
// (claim). Второе значение равно false, если сообщение больше не подлежит
// повтору (состояние изменилось или лимит попыток исчерпан): тогда оно
// пропускается без отправки.
func (h *RetryPendingHandler) reopen(ctx context.Context, id domain.MessageID) (*domain.Message, bool, error) {
	var (
		reopened *domain.Message
		claimed  bool
	)
	err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		message, findErr := tx.Messages().FindByID(ctx, id)
		if findErr != nil {
			return fmt.Errorf("загрузка сообщения %s: %w", id, findErr)
		}
		if !message.IsRetryable() {
			return nil
		}
		if h.maxAttempts > 0 && len(message.Attempts()) >= h.maxAttempts {
			return nil
		}
		if reopenErr := message.ReopenForRetry(h.clock.Now()); reopenErr != nil {
			return reopenErr
		}
		if saveErr := tx.Messages().Save(ctx, message); saveErr != nil {
			return fmt.Errorf("сохранение сообщения %s: %w", id, saveErr)
		}
		reopened = message
		claimed = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return reopened, claimed, nil
}
