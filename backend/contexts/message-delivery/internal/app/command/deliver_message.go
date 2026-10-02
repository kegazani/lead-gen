package command

import (
	"context"
	"errors"
	"fmt"

	"lead-gen/backend/contexts/message-delivery/internal/domain"
	"lead-gen/backend/contexts/message-delivery/internal/ports"
)

// DeliverMessageResult — исход сценария доставки.
type DeliverMessageResult struct {
	// MessageID — идентификатор сообщения во владении контекста.
	MessageID domain.MessageID

	// Status — терминальное состояние сообщения либо состояние незавершённой
	// попытки.
	Status domain.MessageStatus

	// AttemptNumber — номер последней попытки.
	AttemptNumber int

	// ProviderCode — нормализованный код последнего ответа (если был).
	ProviderCode string

	// Replayed равно true, если команда повторена с тем же ключом
	// идемпотентности: повтор не создаёт второго сообщения и второго эффекта.
	Replayed bool
}

// DeliverMessageHandler — use case доставки утверждённого почтового действия.
//
// Порядок шагов отражает природу отправки как необратимого внешнего действия:
//
//  1. Резервирование в транзакции: проверка стоп-листа и репутации, создание
//     сообщения в состоянии sending и его сохранение. Резерв коммитится до
//     отправки, поэтому повтор команды с тем же ключом идемпотентности не
//     приводит ко второй отправке (инвариант message).
//  2. Отправка через порт почтового провайдера (SMTP за портом).
//  3. Фиксация исхода в транзакции: запись попытки, терминальное состояние,
//     обновление репутации по нормализованному ответу и запись события в outbox.
//
// Допустимость отправки приходит из политик домена (approved-action-only,
// deliverability-policy); прикладной слой их не дублирует.
type DeliverMessageHandler struct {
	suppressions ports.SuppressionList
	mailer       ports.Mailer
	clock        ports.Clock
	uow          ports.UnitOfWork
}

// NewDeliverMessageHandler собирает use case из портов.
func NewDeliverMessageHandler(
	suppressions ports.SuppressionList,
	mailer ports.Mailer,
	clock ports.Clock,
	uow ports.UnitOfWork,
) *DeliverMessageHandler {
	return &DeliverMessageHandler{
		suppressions: suppressions,
		mailer:       mailer,
		clock:        clock,
		uow:          uow,
	}
}

// Handle доставляет сообщение по команде.
func (h *DeliverMessageHandler) Handle(ctx context.Context, cmd domain.DeliveryCommand) (DeliverMessageResult, error) {
	if err := domain.ApprovedActionOnly(cmd); err != nil {
		return DeliverMessageResult{}, err
	}

	key := cmd.IdempotencyKey()

	reserved, replayed, err := h.reserve(ctx, cmd, key)
	if err != nil {
		return DeliverMessageResult{}, err
	}
	if replayed {
		return resultFromMessage(reserved, true), nil
	}

	outcome, providerCode, err := h.send(ctx, reserved)
	if err != nil {
		return DeliverMessageResult{}, err
	}

	return h.record(ctx, reserved.ID(), outcome, providerCode)
}

// reserve выполняет резервирование сообщения в транзакции. Возвращённое
// replayed == true означает, что команда уже была исполнена с этим ключом.
func (h *DeliverMessageHandler) reserve(
	ctx context.Context,
	cmd domain.DeliveryCommand,
	key string,
) (reserved *domain.Message, replayed bool, err error) {
	err = h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		existing, findErr := tx.Messages().FindByIdempotencyKey(ctx, key)
		if findErr == nil {
			reserved = existing
			replayed = true
			return nil
		}
		if !errors.Is(findErr, ports.ErrMessageNotFound) {
			return fmt.Errorf("поиск сообщения по ключу идемпотентности: %w", findErr)
		}

		reputation, repErr := h.loadReputation(ctx, tx, cmd.SenderDomain)
		if repErr != nil {
			return repErr
		}

		// Стоп-лист проверяется непосредственно перед попыткой доставки
		// (инвариант message).
		suppressed, supErr := h.suppressions.IsSuppressed(ctx, cmd.Address)
		if supErr != nil {
			return fmt.Errorf("проверка стоп-листа получателя: %w", supErr)
		}

		decision, polErr := domain.DeliverabilityPolicy(cmd, reputation, suppressed)
		if polErr != nil {
			return polErr
		}
		if !decision.Allowed {
			return rejection(decision, cmd)
		}

		message, buildErr := domain.NewMessage(domain.MessageParams{
			ID:               domain.MessageID(cmd.CommandID),
			TenantID:         cmd.TenantID,
			ApprovedActionID: cmd.ApprovedActionID,
			RecipientRef:     cmd.RecipientRef,
			Address:          cmd.Address,
			SenderDomain:     cmd.SenderDomain,
			IdempotencyKey:   key,
			CreatedAt:        h.clock.Now(),
		})
		if buildErr != nil {
			return buildErr
		}
		if startErr := message.StartDelivery(h.clock.Now()); startErr != nil {
			return startErr
		}
		if saveErr := tx.Messages().Save(ctx, message); saveErr != nil {
			return fmt.Errorf("сохранение сообщения %s: %w", message.ID(), saveErr)
		}
		reserved = message
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return reserved, replayed, nil
}

// send выполняет отправку через порт провайдера. Недоступность провайдера не
// прерывает сценарий: она фиксируется как временный исход, чтобы сообщение
// получило терминальное состояние и событие.
func (h *DeliverMessageHandler) send(
	ctx context.Context,
	message *domain.Message,
) (domain.AttemptOutcome, string, error) {
	response, err := h.mailer.Send(ctx, ports.OutgoingMessage{
		MessageID:    message.ID(),
		TenantID:     message.TenantID(),
		SenderDomain: message.SenderDomain(),
		To:           message.Address(),
		RecipientRef: message.RecipientRef(),
	})
	if err != nil {
		if ctx.Err() != nil {
			// Отменённый или просроченный вызов не выдаётся за исход доставки.
			return "", "", fmt.Errorf("отправка сообщения %s: %w", message.ID(), err)
		}
		if !errors.Is(err, ports.ErrMailerUnavailable) {
			return "", "", fmt.Errorf("отправка сообщения %s: %w", message.ID(), err)
		}
		return domain.OutcomeTemporary, "mailer-unavailable", nil
	}
	return response.Outcome, response.ProviderCode, nil
}

// record фиксирует исход попытки в транзакции: терминальное состояние,
// обновление репутации по нормализованному ответу и запись события в outbox.
func (h *DeliverMessageHandler) record(
	ctx context.Context,
	id domain.MessageID,
	outcome domain.AttemptOutcome,
	providerCode string,
) (DeliverMessageResult, error) {
	var result DeliverMessageResult
	err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		message, findErr := tx.Messages().FindByID(ctx, id)
		if findErr != nil {
			return fmt.Errorf("загрузка сообщения %s: %w", id, findErr)
		}
		if message.Status().IsTerminal() {
			// Сообщение уже завершено (например, повторное исполнение): второго
			// эффекта и второго события не создаём.
			result = resultFromMessage(message, false)
			return nil
		}

		if recErr := message.RecordDelivery(outcome, providerCode, h.clock.Now()); recErr != nil {
			return recErr
		}
		if saveErr := tx.Messages().Save(ctx, message); saveErr != nil {
			return fmt.Errorf("сохранение сообщения %s: %w", id, saveErr)
		}
		if outErr := tx.Outbox().Append(ctx, message.DomainEvents()...); outErr != nil {
			return fmt.Errorf("запись событий сообщения %s в outbox: %w", id, outErr)
		}
		message.PullDomainEvents()

		if normalized, ok := domain.NormalizeProviderResponse(outcome, providerCode); ok {
			reputation, repErr := h.loadReputation(ctx, tx, message.SenderDomain())
			if repErr != nil {
				return repErr
			}
			if applyErr := reputation.ApplyNormalizedOutcome(normalized, h.clock.Now()); applyErr != nil {
				return applyErr
			}
			if saveErr := tx.Reputations().Save(ctx, reputation); saveErr != nil {
				return fmt.Errorf("сохранение репутации домена %s: %w", message.SenderDomain(), saveErr)
			}
		}

		result = resultFromMessage(message, false)
		return nil
	})
	if err != nil {
		return DeliverMessageResult{}, err
	}
	return result, nil
}

// loadReputation загружает репутацию домена в транзакции. Отсутствие истории —
// не ошибка: домен без ответов провайдера считается неограниченным.
func (h *DeliverMessageHandler) loadReputation(
	ctx context.Context,
	tx ports.Transaction,
	sender domain.SenderDomain,
) (*domain.SenderReputation, error) {
	reputation, err := tx.Reputations().FindByDomain(ctx, sender)
	if err == nil {
		return reputation, nil
	}
	if !errors.Is(err, ports.ErrSenderReputationNotFound) {
		return nil, fmt.Errorf("загрузка репутации домена %s: %w", sender, err)
	}
	reputation, buildErr := domain.NewSenderReputation(sender, h.clock.Now())
	if buildErr != nil {
		return nil, buildErr
	}
	return reputation, nil
}

// rejection переводит отрицательное решение политики в доменную ошибку.
func rejection(decision domain.DeliverabilityDecision, cmd domain.DeliveryCommand) error {
	switch {
	case decision.Suppressed:
		return fmt.Errorf("%w: %s", domain.ErrRecipientSuppressed, cmd.Address)
	case decision.Suspended:
		return fmt.Errorf("%w: %s", domain.ErrSenderSuspended, cmd.SenderDomain)
	default:
		return fmt.Errorf("%w: %s", domain.ErrApprovedActionRequired, decision.Reason)
	}
}

// resultFromMessage собирает результат сценария из агрегата.
func resultFromMessage(message *domain.Message, replayed bool) DeliverMessageResult {
	result := DeliverMessageResult{
		MessageID: message.ID(),
		Status:    message.Status(),
		Replayed:  replayed,
	}
	attempts := message.Attempts()
	if len(attempts) > 0 {
		last := attempts[len(attempts)-1]
		result.AttemptNumber = last.Number
		result.ProviderCode = last.ProviderCode
	}
	return result
}
