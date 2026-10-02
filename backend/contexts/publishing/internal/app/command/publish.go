package command

import (
	"context"
	"errors"
	"fmt"
	"time"

	"lead-gen/backend/contexts/publishing/internal/domain"
	"lead-gen/backend/contexts/publishing/internal/ports"
)

// Publish — команда исполнения публикации утверждённого материала через
// официальный интерфейс площадки.
type Publish struct {
	PublicationID domain.PublicationID
	TenantID      domain.TenantID
	Actor         domain.ActorID
	At            time.Time
	CorrelationID domain.CorrelationID
	CausationID   domain.CausationID
	// EventID — идентификатор события material-published. Пустое значение
	// заменяется сгенерированным.
	EventID domain.EventID
}

// PublishResult — исход попытки публикации. Успех несёт адрес материала, отказ
// площадки — причину; события возвращаются подтверждёнными outbox'ом.
type PublishResult struct {
	State         PublicationState
	Address       string
	FailureReason string
	Events        []domain.DomainEvent
}

// PublishHandler — use case исполнения публикации.
type PublishHandler struct {
	uow      ports.UnitOfWork
	clock    ports.Clock
	ids      ports.IDGenerator
	platform ports.PlatformPublisher
}

// NewPublishHandler собирает use case из порта транзакции, часов, генератора
// идентификаторов и порта публикации.
func NewPublishHandler(uow ports.UnitOfWork, clock ports.Clock, ids ports.IDGenerator, platform ports.PlatformPublisher) *PublishHandler {
	return &PublishHandler{uow: uow, clock: clock, ids: ids, platform: platform}
}

// Handle публикует материал. Порядок шагов: проверка канала, внешний вызов,
// фиксация результата. Отказ площадки фиксируется как failed без автоматического
// повтора; сбой транспорта оставляет публикацию утверждённой — повтор допустим.
func (h *PublishHandler) Handle(ctx context.Context, cmd Publish) (PublishResult, error) {
	at := cmd.At
	if at.IsZero() && h.clock != nil {
		at = h.clock.Now()
	}
	eventID := cmd.EventID
	if eventID == "" && h.ids != nil {
		eventID = h.ids.NewEventID()
	}

	// Чтение и проверка состояния до внешнего вызова: неподготовленную или
	// терминальную публикацию наружу не отправляем.
	var prepared *domain.Publication
	err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		publication, err := loadForTenant(ctx, tx, cmd.PublicationID, cmd.TenantID)
		if err != nil {
			return err
		}
		if err := ensurePublishable(publication); err != nil {
			return err
		}
		prepared = publication
		return nil
	})
	if err != nil {
		return PublishResult{}, err
	}

	target := prepared.Target()
	if err := h.platform.Ready(ctx, string(target.Platform()), target.Account()); err != nil {
		return PublishResult{}, err
	}

	published, publishErr := h.platform.Publish(ctx, ports.PublishRequest{
		PublicationID: string(prepared.ID()),
		TenantID:      string(prepared.TenantID()),
		Platform:      string(target.Platform()),
		Account:       target.Account(),
		Title:         prepared.Material().Title(),
		Body:          prepared.Material().Body(),
	})

	if publishErr != nil {
		if errors.Is(publishErr, ports.ErrPublicationRejected) {
			return h.recordFailure(ctx, cmd, at, publishErr)
		}
		// Сбой транспорта: состояние не меняем, повтор возможен.
		return PublishResult{}, publishErr
	}

	return h.recordPublished(ctx, cmd, at, eventID, published.Address)
}

// recordPublished фиксирует успех и записывает событие material-published в
// одной транзакции.
func (h *PublishHandler) recordPublished(ctx context.Context, cmd Publish, at time.Time, eventID domain.EventID, address string) (PublishResult, error) {
	var result PublishResult
	err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		publication, err := loadForTenant(ctx, tx, cmd.PublicationID, cmd.TenantID)
		if err != nil {
			return err
		}
		expected := publication.Version()
		if err := publication.MarkPublished(domain.PublishParams{
			Actor:         cmd.Actor,
			At:            at,
			Address:       address,
			EventID:       eventID,
			CorrelationID: cmd.CorrelationID,
			CausationID:   cmd.CausationID,
		}); err != nil {
			return err
		}
		events := publication.DomainEvents()
		if err := tx.Publications().Save(ctx, publication, expected); err != nil {
			return err
		}
		if err := tx.Outbox().Append(ctx, events...); err != nil {
			return err
		}
		publication.PullDomainEvents()
		result = PublishResult{State: stateOf(publication), Address: address, Events: events}
		return nil
	})
	if err != nil {
		return PublishResult{}, err
	}
	return result, nil
}

// recordFailure фиксирует отказ площадки как failed. Автоматического повтора нет:
// повтор — отдельное решение оператора.
func (h *PublishHandler) recordFailure(ctx context.Context, cmd Publish, at time.Time, cause error) (PublishResult, error) {
	reason := failureReason(cause)
	var result PublishResult
	err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		publication, err := loadForTenant(ctx, tx, cmd.PublicationID, cmd.TenantID)
		if err != nil {
			return err
		}
		expected := publication.Version()
		if err := publication.MarkFailed(domain.FailParams{
			Actor:       cmd.Actor,
			At:          at,
			Reason:      reason,
			CausationID: cmd.CausationID,
		}); err != nil {
			return err
		}
		if err := tx.Publications().Save(ctx, publication, expected); err != nil {
			return err
		}
		result = PublishResult{State: stateOf(publication), FailureReason: reason}
		return nil
	})
	if err != nil {
		return PublishResult{}, err
	}
	return result, nil
}

// ensurePublishable отвергает публикацию из неподготовленного или терминального
// состояния, различая отсутствие утверждения и прочий запрет.
func ensurePublishable(p *domain.Publication) error {
	status := p.Status()
	if status.IsTerminal() {
		return fmt.Errorf("%w: %s", domain.ErrTerminalState, status)
	}
	if status != domain.StatusApproved && status != domain.StatusQueued {
		return fmt.Errorf("%w: %s", domain.ErrApprovalRequired, status)
	}
	return nil
}

// failureReason извлекает причину отказа площадки из ошибки порта.
func failureReason(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
