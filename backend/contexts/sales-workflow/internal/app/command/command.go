// Package command — прикладные сценарии изменения контекста sales-workflow:
// жизненный цикл сделки, приём события CRM и подтверждаемая атрибуция результата.
//
// Сценарий открывает транзакцию локального состояния, применяет доменное действие
// и фиксирует порождённые события в outbox. Правил допустимости сценарий не
// принимает: их обеспечивает aggregate root deal/crm-sync, а политики
// confirmed-attribution и conflict-resolution применяются доменом.
package command

import (
	"context"
	"errors"
	"time"

	"lead-gen/backend/contexts/sales-workflow/internal/domain"
	"lead-gen/backend/contexts/sales-workflow/internal/ports"
)

// ErrTenantMismatch — сделка принадлежит другому тенанту. Сценарий не раскрывает
// существование чужой сделки: вызывающий получает отказ.
var ErrTenantMismatch = errors.New("сценарий: сделка принадлежит другому тенанту")

// CRMActor — актор по умолчанию для действий, инициированных внешней CRM: если
// вызывающий не назвал актора, событие подписывается этой ролью, а не пустой
// строкой.
const CRMActor domain.ActorID = "crm"

// base — общие зависимости сценарных обработчиков: граница внешней CRM,
// транзакция локального состояния, часы и генератор идентификаторов событий.
type base struct {
	deals ports.DealRepository
	syncs ports.CRMSyncRepository
	uow   ports.UnitOfWork
	clock ports.Clock
	ids   ports.IDGenerator
}

// actionInput собирает вход доменного действия, подставляя время часов, если
// вызывающий его не задал, и идентификатор события из генератора.
func (b base) actionInput(
	actor domain.ActorID,
	at time.Time,
	reason string,
	correlation domain.CorrelationID,
	causation domain.CausationID,
) domain.ActionInput {
	if at.IsZero() && b.clock != nil {
		at = b.clock.Now()
	}
	return domain.ActionInput{
		Actor:         actor,
		At:            at,
		Reason:        reason,
		EventID:       b.ids.NewEventID(),
		CorrelationID: correlation,
		CausationID:   causation,
	}
}

// syncInput собирает вход сверки синхронизации.
func (b base) syncInput(
	at time.Time,
	correlation domain.CorrelationID,
	causation domain.CausationID,
) domain.SyncInput {
	if at.IsZero() && b.clock != nil {
		at = b.clock.Now()
	}
	return domain.SyncInput{
		At:            at,
		EventID:       b.ids.NewEventID(),
		CorrelationID: correlation,
		CausationID:   causation,
	}
}

// loadDeal загружает сделку из внешней CRM и сверяет тенант.
func (b base) loadDeal(ctx context.Context, id domain.DealID, tenant domain.TenantID) (*domain.Deal, error) {
	deal, err := b.deals.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if deal.TenantID() != tenant {
		return nil, ErrTenantMismatch
	}
	return deal, nil
}

// commit фиксирует изменение: сохранение сделки во внешней CRM, затем запись
// локального состояния (синхронизации) и событий в одной транзакции.
//
// ВНИМАНИЕ: запись сделки идёт во внешнюю CRM и НЕ входит в локальную транзакцию
// (storage сделки — external). Падение между записью сделки и фиксацией события
// оставит сделку изменённой без опубликованного события; для внешней системы это
// неизбежно и требует сверки/повтора. Локальная же транзакция атомарна: событие
// синхронизации не появится без самой записи синхронизации. Ограничение честно
// названо, а не спрятано.
func (b base) commit(
	ctx context.Context,
	deal *domain.Deal,
	expected int,
	sync *domain.CRMSync,
) ([]domain.DomainEvent, error) {
	var events []domain.DomainEvent
	if deal != nil {
		if err := b.deals.Save(ctx, deal, expected); err != nil {
			return nil, err
		}
		events = append(events, deal.PullDomainEvents()...)
	}
	if sync != nil {
		events = append(events, sync.PullDomainEvents()...)
	}
	if sync == nil && len(events) == 0 {
		return events, nil
	}
	err := b.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		if sync != nil {
			if err := tx.Syncs().Save(ctx, sync); err != nil {
				return err
			}
		}
		if len(events) > 0 {
			return tx.Outbox().Append(ctx, events...)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return events, nil
}

// syncIDFor выводит идентификатор синхронизации из сделки: у сделки ровно одна
// запись синхронизации, поэтому отдельный идентификатор не требуется вызывающему.
func syncIDFor(dealID domain.DealID) domain.CRMSyncID {
	return domain.CRMSyncID("crm-sync:" + string(dealID))
}
