package command

import (
	"context"
	"errors"
	"fmt"
	"time"

	"lead-gen/backend/contexts/billing/internal/domain"
	"lead-gen/backend/contexts/billing/internal/ports"
)

// CalculateSuccessFee — команда расчёта вознаграждения от подтверждённого
// результата (контракт attributed-result, доставляется асинхронно). Политика
// success-fee-eligibility решает, становится ли сделка кандидатом; при отказе
// расчёт не выполняется, и это не ошибка.
type CalculateSuccessFee struct {
	SuccessFeeID domain.SuccessFeeID
	TenantID     domain.TenantID
	// SubscriptionID — подписка, к которой относится результат.
	SubscriptionID domain.SubscriptionID
	// ResultRef — непрозрачная ссылка на подтверждённый результат.
	ResultRef domain.ResultRef
	DealID    domain.DealID
	// Amount — сумма сделки (база расчёта).
	Amount domain.Money
	// Confirmed — закрытие сделки подтверждено.
	Confirmed bool
	// ClosedInPlatformCRM — сделка ведётся в CRM платформы.
	ClosedInPlatformCRM bool
	// CommunicationRecorded — коммуникация системы зафиксирована.
	CommunicationRecorded bool
	At                    time.Time
	CorrelationID         domain.CorrelationID
	CausationID           domain.CausationID
}

// CalculateSuccessFeeResult — результат расчёта вознаграждения.
type CalculateSuccessFeeResult struct {
	Eligible     bool
	SuccessFeeID domain.SuccessFeeID
	Amount       domain.Money
	Calculation  domain.CalculationVersion
	Reason       string
	Version      int
	Events       []domain.DomainEvent
}

// CalculateSuccessFeeHandler — use case расчёта вознаграждения.
type CalculateSuccessFeeHandler struct {
	uow   ports.UnitOfWork
	clock ports.Clock
	ids   ports.IDGenerator
}

// NewCalculateSuccessFeeHandler собирает use case.
func NewCalculateSuccessFeeHandler(uow ports.UnitOfWork, clock ports.Clock, ids ports.IDGenerator) *CalculateSuccessFeeHandler {
	return &CalculateSuccessFeeHandler{uow: uow, clock: clock, ids: ids}
}

// Handle применяет политику применимости и при положительном решении создаёт
// вознаграждение и фиксирует результат биллинга в подписке в одной транзакции.
func (h *CalculateSuccessFeeHandler) Handle(ctx context.Context, cmd CalculateSuccessFee) (CalculateSuccessFeeResult, error) {
	decision, err := domain.SuccessFeeEligibility(domain.AttributedResult{
		ResultRef:             cmd.ResultRef,
		TenantID:              cmd.TenantID,
		DealID:                cmd.DealID,
		Confirmed:             cmd.Confirmed,
		ClosedInPlatformCRM:   cmd.ClosedInPlatformCRM,
		CommunicationRecorded: cmd.CommunicationRecorded,
		Amount:                cmd.Amount,
	})
	if err != nil {
		return CalculateSuccessFeeResult{}, err
	}
	if !decision.Eligible {
		return CalculateSuccessFeeResult{Eligible: false, Reason: decision.Reason}, nil
	}

	at := atOrNow(cmd.At, h.clock)
	var result CalculateSuccessFeeResult
	err = h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		if existing, err := tx.SuccessFees().FindByResultRef(ctx, cmd.TenantID, cmd.ResultRef); err == nil {
			result = CalculateSuccessFeeResult{
				Eligible:     true,
				SuccessFeeID: existing.ID(),
				Amount:       existing.Amount(),
				Calculation:  existing.Calculation(),
				Reason:       decision.Reason,
				Version:      existing.Version(),
			}
			return nil
		} else if !errors.Is(err, ports.ErrSuccessFeeNotFound) {
			return err
		}

		subscription, err := tx.Subscriptions().FindByID(ctx, cmd.SubscriptionID)
		if err != nil {
			return err
		}
		if subscription.TenantID() != cmd.TenantID {
			return fmt.Errorf("%w: подписка %s", ErrTenantMismatch, cmd.SubscriptionID)
		}

		fee, err := domain.NewSuccessFee(domain.NewSuccessFeeParams{
			ID:             cmd.SuccessFeeID,
			TenantID:       cmd.TenantID,
			SubscriptionID: cmd.SubscriptionID,
			ResultRef:      cmd.ResultRef,
			DealID:         cmd.DealID,
			Base:           cmd.Amount,
			RateBps:        decision.RateBps,
			Calculation:    decision.Calculation,
			Confirmed:      true,
			At:             at,
			EventID:        h.ids.NewEventID(),
			CorrelationID:  cmd.CorrelationID,
			CausationID:    cmd.CausationID,
		})
		if err != nil {
			return err
		}
		if err := tx.SuccessFees().Save(ctx, fee, 0); err != nil {
			return err
		}

		expected := subscription.Version()
		recorded, err := subscription.RecordBillingResult(domain.BillingResultInput{
			ResultRef:     cmd.ResultRef,
			Kind:          domain.BillingResultSuccessFee,
			Amount:        fee.Amount(),
			At:            at,
			EventID:       h.ids.NewEventID(),
			CorrelationID: cmd.CorrelationID,
			CausationID:   cmd.CausationID,
		})
		if err != nil {
			return err
		}
		if err := tx.Subscriptions().Save(ctx, subscription, expected); err != nil {
			return err
		}

		events := append(fee.DomainEvents(), recorded...)
		if len(events) > 0 {
			if err := tx.Outbox().Append(ctx, events...); err != nil {
				return err
			}
		}
		fee.PullDomainEvents()
		subscription.PullDomainEvents()
		result = CalculateSuccessFeeResult{
			Eligible:     true,
			SuccessFeeID: fee.ID(),
			Amount:       fee.Amount(),
			Calculation:  fee.Calculation(),
			Reason:       decision.Reason,
			Version:      fee.Version(),
			Events:       events,
		}
		return nil
	})
	if err != nil {
		return CalculateSuccessFeeResult{}, err
	}
	return result, nil
}

// DisputeSuccessFee — команда оспаривания начисления: спор приостанавливает
// списание до разрешения.
type DisputeSuccessFee struct {
	SuccessFeeID domain.SuccessFeeID
	TenantID     domain.TenantID
	At           time.Time
}

// DisputeSuccessFeeResult — результат оспаривания.
type DisputeSuccessFeeResult struct {
	SuccessFeeID domain.SuccessFeeID
	Status       domain.FeeStatus
	Version      int
}

// DisputeSuccessFeeHandler — use case оспаривания начисления.
type DisputeSuccessFeeHandler struct {
	uow   ports.UnitOfWork
	clock ports.Clock
}

// NewDisputeSuccessFeeHandler собирает use case.
func NewDisputeSuccessFeeHandler(uow ports.UnitOfWork, clock ports.Clock) *DisputeSuccessFeeHandler {
	return &DisputeSuccessFeeHandler{uow: uow, clock: clock}
}

// Handle переводит начисленное вознаграждение в статус «оспорено».
func (h *DisputeSuccessFeeHandler) Handle(ctx context.Context, cmd DisputeSuccessFee) (DisputeSuccessFeeResult, error) {
	at := atOrNow(cmd.At, h.clock)
	var result DisputeSuccessFeeResult
	err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		fee, err := tx.SuccessFees().FindByID(ctx, cmd.SuccessFeeID)
		if err != nil {
			return err
		}
		if fee.TenantID() != cmd.TenantID {
			return fmt.Errorf("%w: вознаграждение %s", ErrTenantMismatch, cmd.SuccessFeeID)
		}
		expected := fee.Version()
		if err := fee.Dispute(at); err != nil {
			return err
		}
		if err := tx.SuccessFees().Save(ctx, fee, expected); err != nil {
			return err
		}
		fee.PullDomainEvents()
		result = DisputeSuccessFeeResult{
			SuccessFeeID: fee.ID(),
			Status:       fee.Status(),
			Version:      fee.Version(),
		}
		return nil
	})
	if err != nil {
		return DisputeSuccessFeeResult{}, err
	}
	return result, nil
}
