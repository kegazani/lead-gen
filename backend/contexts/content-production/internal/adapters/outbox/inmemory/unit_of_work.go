// Package inmemory реализует transactional outbox и unit-of-work поверх
// оперативной памяти: изменение контент-пакета или задания рендеринга и запись
// его событий выполняются в одной транзакции, а публикацию подтверждённых записей
// выполняет relay, читающий журнал платформы (platform/messaging/outbox).
//
// Откат транзакции не оставляет ни агрегата, ни события. Пакет не знает о
// брокере: журнал принимает готовые записи outbox, которые relay публикует в
// async/amqp.
package inmemory

import (
	"context"
	"errors"
	"sync"

	persistence "lead-gen/backend/contexts/content-production/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/content-production/internal/domain"
	"lead-gen/backend/contexts/content-production/internal/ports"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
)

// UnitOfWork — in-memory реализация ports.UnitOfWork для тестов и локального
// запуска.
//
// Транзакция работает с копией хранилища и буфером outbox. При успешном
// завершении функции состояние копии переносится в основное хранилище, а
// накопленные события — в журнал outbox; при ошибке копия и буфер отбрасываются.
// Транзакции сериализуются мьютексом: вложенные WithinTransaction не
// поддерживаются.
type UnitOfWork struct {
	mu      sync.Mutex
	base    *persistence.Store
	journal platformoutbox.Journal
}

// UnitOfWork реализует порт транзакции.
var _ ports.UnitOfWork = (*UnitOfWork)(nil)

// NewUnitOfWorkWith собирает unit-of-work из готового хранилища и журнала. Нужен,
// когда приложение и relay должны разделять одно состояние.
func NewUnitOfWorkWith(base *persistence.Store, journal platformoutbox.Journal) *UnitOfWork {
	if base == nil {
		base = persistence.New()
	}
	if journal == nil {
		journal = platformoutbox.NewMemoryJournal()
	}
	return &UnitOfWork{base: base, journal: journal}
}

// Store возвращает разделяемое хранилище, поверх которого работает транзакция.
// Нужен сборке: репозитории для чтения и воркера берутся из того же состояния.
func (u *UnitOfWork) Store() *persistence.Store { return u.base }

// WithinTransaction выполняет fn в одной транзакции.
//
// ВНИМАНИЕ: in-memory реализация неатомарна между переносом состояния агрегатов
// (ReplaceWith) и добавлением событий в журнал: падение процесса между этими
// шагами сохранит агрегат без события. Для реального хранилища оба шага обязаны
// выполняться в одной транзакции БД — иначе transactional outbox теряет гарантию
// атомарности записи агрегата и события.
func (u *UnitOfWork) WithinTransaction(ctx context.Context, fn func(tx ports.Transaction) error) error {
	if fn == nil {
		return errors.New("inmemory: WithinTransaction без функции транзакции")
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	working := u.base.Clone()
	outbox := &transactionOutbox{}
	if err := fn(&transaction{store: working, outbox: outbox}); err != nil {
		return err
	}

	u.base.ReplaceWith(working)
	return outbox.flush(ctx, u.journal)
}

// transaction — фасад над транзакцией: хранилище-копия и буфер outbox.
type transaction struct {
	store  *persistence.Store
	outbox *transactionOutbox
}

func (t *transaction) Packages() ports.ContentPackageRepository { return t.store.Content() }
func (t *transaction) RenderJobs() ports.RenderJobRepository    { return t.store.RenderJobs() }
func (t *transaction) Outbox() ports.Outbox                     { return t.outbox }

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
