// Package inmemory реализует transactional outbox и unit-of-work billing поверх
// оперативной памяти: изменение агрегатов и запись их событий выполняются в одной
// транзакции, а публикацию подтверждённых записей выполняет relay, читающий журнал
// платформы (platform/messaging/outbox).
//
// Откат транзакции не оставляет ни агрегатов, ни событий. Пакет не знает о
// брокере: журнал принимает готовые записи outbox, которые relay публикует в
// async/amqp.
package inmemory

import (
	"context"
	"errors"
	"sync"

	persistence "lead-gen/backend/contexts/billing/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/billing/internal/domain"
	"lead-gen/backend/contexts/billing/internal/ports"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
)

// UnitOfWork — in-memory реализация ports.UnitOfWork для тестов и локального
// запуска.
//
// Транзакция работает с копиями репозиториев и буфером outbox. При успешном
// завершении состояние копий переносится в основные репозитории, а накопленные
// события — в журнал outbox; при ошибке копии и буфер отбрасываются. Транзакции
// сериализуются мьютексом: вложенные WithinTransaction не поддерживаются.
type UnitOfWork struct {
	mu      sync.Mutex
	subs    *persistence.SubscriptionRepository
	fees    *persistence.SuccessFeeRepository
	journal platformoutbox.Journal
}

// UnitOfWork реализует порт транзакции.
var _ ports.UnitOfWork = (*UnitOfWork)(nil)

// NewUnitOfWorkWith собирает unit-of-work из готовых репозиториев и журнала.
// Нужен, когда приложение и relay должны разделять одно состояние.
func NewUnitOfWorkWith(
	subscriptions *persistence.SubscriptionRepository,
	fees *persistence.SuccessFeeRepository,
	journal platformoutbox.Journal,
) *UnitOfWork {
	if subscriptions == nil {
		subscriptions = persistence.NewSubscriptionRepository()
	}
	if fees == nil {
		fees = persistence.NewSuccessFeeRepository()
	}
	if journal == nil {
		journal = platformoutbox.NewMemoryJournal()
	}
	return &UnitOfWork{subs: subscriptions, fees: fees, journal: journal}
}

// WithinTransaction выполняет fn в одной транзакции.
//
// ВНИМАНИЕ: in-memory реализация неатомарна между переносом состояния агрегатов и
// добавлением событий в журнал: падение процесса между этими шагами сохранит
// агрегаты без событий. Для реального хранилища оба шага обязаны выполняться в
// одной транзакции БД.
func (u *UnitOfWork) WithinTransaction(ctx context.Context, fn func(tx ports.Transaction) error) error {
	if fn == nil {
		return errors.New("inmemory: WithinTransaction без функции транзакции")
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	workingSubs := u.subs.Clone()
	workingFees := u.fees.Clone()
	outbox := &transactionOutbox{}
	if err := fn(&transaction{subs: workingSubs, fees: workingFees, outbox: outbox}); err != nil {
		return err
	}

	u.subs.ReplaceWith(workingSubs)
	u.fees.ReplaceWith(workingFees)
	return outbox.flush(ctx, u.journal)
}

// transaction — фасад над транзакцией: копии репозиториев и буфер outbox.
type transaction struct {
	subs   *persistence.SubscriptionRepository
	fees   *persistence.SuccessFeeRepository
	outbox *transactionOutbox
}

func (t *transaction) Subscriptions() ports.SubscriptionRepository { return t.subs }
func (t *transaction) SuccessFees() ports.SuccessFeeRepository     { return t.fees }
func (t *transaction) Outbox() ports.Outbox                        { return t.outbox }

// transactionOutbox копит события до commit. Ни один метод не публикует события
// сам: перенос в журнал делает UnitOfWork после успешного завершения.
type transactionOutbox struct {
	events []domain.DomainEvent
}

func (o *transactionOutbox) Append(_ context.Context, events ...domain.DomainEvent) error {
	o.events = append(o.events, events...)
	return nil
}

// flush переносит накопленные события в журнал outbox, кодируя каждое.
func (o *transactionOutbox) flush(ctx context.Context, journal platformoutbox.Journal) error {
	for _, event := range o.events {
		message, err := EncodeMessage(event)
		if err != nil {
			return err
		}
		if err := journal.Append(ctx, message); err != nil {
			return err
		}
	}
	return nil
}
