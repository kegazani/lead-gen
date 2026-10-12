// Package inmemory реализует transactional outbox и unit-of-work поверх
// оперативной памяти: изменение заявки и запись её событий выполняются в одной
// транзакции, а публикацию подтверждённых записей выполняет relay, читающий
// журнал платформы (platform/messaging/outbox).
//
// Откат транзакции не оставляет ни заявки, ни события. Пакет не знает о брокере:
// журнал принимает готовые записи outbox, которые relay публикует в async/amqp.
package inmemory

import (
	"context"
	"errors"

	persistence "lead-gen/backend/contexts/request-fulfillment/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
)

// UnitOfWork — in-memory реализация ports.UnitOfWork для тестов и локального
// запуска.
//
// Общий context-aware repository gate удерживается от clone до commit и
// разделяется всеми прямыми writers/readers, journal и UoW одного repository.
// Callback работает только локально с transaction ports; внешний I/O запрещён.
type UnitOfWork struct {
	repo       *persistence.Repository
	journal    atomicJournal
	gate       *persistence.Gate
	journalErr error
}

type atomicJournal interface {
	platformoutbox.Journal
	BindVisibilityGate(*persistence.Gate) error
	CommitBatchUnderGate(context.Context, []platformoutbox.Message, func()) error
}

// UnitOfWork реализует порт транзакции.
var _ ports.UnitOfWork = (*UnitOfWork)(nil)

// NewUnitOfWorkWith собирает unit-of-work из готовых репозитория и журнала.
// Нужен, когда приложение и relay должны разделять одно состояние.
func NewUnitOfWorkWith(repo *persistence.Repository, journal platformoutbox.Journal) *UnitOfWork {
	if repo == nil {
		repo = persistence.New()
	}
	if journal == nil {
		journal = NewTransactionalJournal()
	}
	atomicJournal, ok := journal.(atomicJournal)
	u := &UnitOfWork{repo: repo, journal: atomicJournal, gate: repo.VisibilityGate()}
	if !ok {
		u.journalErr = errors.New("inmemory: для атомарной UoW требуется request-fulfillment TransactionalJournal")
		return u
	}
	if err := atomicJournal.BindVisibilityGate(u.gate); err != nil {
		u.journalErr = err
	}
	return u
}

// WithinTransaction выполняет fn в одной транзакции.
func (u *UnitOfWork) WithinTransaction(ctx context.Context, fn func(txCtx context.Context, tx ports.Transaction) error) error {
	if ports.HasTransactionScope(ctx) {
		return ports.ErrNestedTransaction
	}
	if u.journalErr != nil {
		return u.journalErr
	}
	if fn == nil {
		return errors.New("inmemory: WithinTransaction без функции транзакции")
	}

	if err := u.gate.Acquire(ctx); err != nil {
		return err
	}
	defer u.gate.Release()
	if err := ctx.Err(); err != nil {
		return err
	}
	working := u.repo.CloneUnderGate()
	outbox := &transactionOutbox{}
	txCtx := ports.WithTransactionScope(ctx)
	if err := txCtx.Err(); err != nil {
		return err
	}
	if err := fn(txCtx, &transaction{repo: working, outbox: outbox}); err != nil {
		return err
	}

	if err := txCtx.Err(); err != nil {
		return err
	}
	messages, err := outbox.encode(txCtx)
	if err != nil {
		return err
	}
	prepared := u.repo.PrepareReplacement(working)
	if err := txCtx.Err(); err != nil {
		return err
	}
	return u.journal.CommitBatchUnderGate(txCtx, messages, func() { u.repo.PublishPreparedUnderGate(prepared) })
}

// transaction — фасад над транзакцией: репозиторий-копия и буфер outbox.
type transaction struct {
	repo   *persistence.Repository
	outbox *transactionOutbox
}

func (t *transaction) Requests() ports.RequestRepository                         { return t.repo }
func (t *transaction) Outbox() ports.Outbox                                      { return t.outbox }
func (t *transaction) ExecutionAttempts() ports.ExecutionAttemptRepository       { return t.repo }
func (t *transaction) ExecutionResolutions() ports.ExecutionResolutionRepository { return t.repo }
func (t *transaction) VerifiedRuntimeResolutions() ports.VerifiedRuntimeResolutionWriter {
	return t.repo
}

// transactionOutbox копит события до commit. Ни один метод не публикует события
// сам: перенос в журнал делает UnitOfWork после успешного завершения.
type transactionOutbox struct {
	events []domain.DomainEvent
}

func (o *transactionOutbox) Append(_ context.Context, events ...domain.DomainEvent) error {
	o.events = append(o.events, events...)
	return nil
}

// flush переносит накопленные события в журнал outbox, кодируя каждое в запись с
// ключом идемпотентности event_id.

func (o *transactionOutbox) encode(ctx context.Context) ([]platformoutbox.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	messages := make([]platformoutbox.Message, 0, len(o.events))
	for _, event := range o.events {
		message, err := EncodeMessage(event)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, nil
}
