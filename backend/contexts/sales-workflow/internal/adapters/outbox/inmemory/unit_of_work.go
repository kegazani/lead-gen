// Package inmemory реализует transactional outbox и unit-of-work локального
// состояния контекста sales-workflow поверх оперативной памяти: запись
// синхронизации сделки и запись её событий выполняются в одной транзакции, а
// публикацию подтверждённых записей выполняет relay, читающий журнал платформы
// (platform/messaging/outbox).
//
// Откат транзакции не оставляет ни синхронизации, ни события. Пакет не знает о
// брокере: журнал принимает готовые записи outbox, которые relay публикует в
// async/amqp.
//
// Сделка во внешней CRM в эту транзакцию НЕ входит (storage external): её запись
// выполняет порт внешней CRM до открытия транзакции. Поэтому атомарность
// «запись сделки + событие» здесь недостижима и честно названа ограничением
// среза (см. doc сборки и README).
package inmemory

import (
	"context"
	"errors"
	"sync"

	persistence "lead-gen/backend/contexts/sales-workflow/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/sales-workflow/internal/domain"
	"lead-gen/backend/contexts/sales-workflow/internal/ports"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
)

// UnitOfWork — in-memory реализация ports.UnitOfWork для тестов и локального
// запуска.
type UnitOfWork struct {
	mu      sync.Mutex
	repo    *persistence.Repository
	journal platformoutbox.Journal
}

// UnitOfWork реализует порт транзакции.
var _ ports.UnitOfWork = (*UnitOfWork)(nil)

// NewUnitOfWorkWith собирает unit-of-work из готового репозитория синхронизаций и
// журнала. Нужен, когда приложение и relay должны разделять одно состояние.
func NewUnitOfWorkWith(repo *persistence.Repository, journal platformoutbox.Journal) *UnitOfWork {
	if repo == nil {
		repo = persistence.New()
	}
	if journal == nil {
		journal = platformoutbox.NewMemoryJournal()
	}
	return &UnitOfWork{repo: repo, journal: journal}
}

// WithinTransaction выполняет fn в одной транзакции.
//
// Транзакция работает с копией репозитория синхронизаций и буфером outbox. При
// успешном завершении состояние копии переносится в основной репозиторий, а
// накопленные события — в журнал outbox; при ошибке копия и буфер отбрасываются.
// Транзакции сериализуются мьютексом: вложенные WithinTransaction не
// поддерживаются.
func (u *UnitOfWork) WithinTransaction(ctx context.Context, fn func(tx ports.Transaction) error) error {
	if fn == nil {
		return errors.New("inmemory: WithinTransaction без функции транзакции")
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	working := u.repo.Clone()
	outbox := &transactionOutbox{}
	if err := fn(&transaction{repo: working, outbox: outbox}); err != nil {
		return err
	}

	u.repo.ReplaceWith(working)
	return outbox.flush(ctx, u.journal)
}

// transaction — фасад над транзакцией: репозиторий-копия и буфер outbox.
type transaction struct {
	repo   *persistence.Repository
	outbox *transactionOutbox
}

func (t *transaction) Syncs() ports.CRMSyncRepository { return t.repo }
func (t *transaction) Outbox() ports.Outbox           { return t.outbox }

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
