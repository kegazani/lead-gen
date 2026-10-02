// Package inmemory реализует transactional outbox и unit-of-work поверх
// оперативной памяти: изменение публикации и запись её событий выполняются в
// одной транзакции, а публикацию подтверждённых записей выполняет relay, читающий
// журнал платформы (platform/messaging/outbox).
//
// Откат транзакции не оставляет ни публикации, ни события. Пакет не знает о
// брокере: журнал принимает готовые записи outbox, которые relay публикует в
// async/amqp.
package inmemory

import (
	"context"
	"errors"
	"sync"

	persistence "lead-gen/backend/contexts/publishing/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/publishing/internal/domain"
	"lead-gen/backend/contexts/publishing/internal/ports"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
)

// UnitOfWork — in-memory реализация ports.UnitOfWork для тестов и локального
// запуска.
//
// Транзакция работает с копией репозитория публикаций и буфером outbox. При
// успешном завершении функции состояние копии переносится в основной репозиторий,
// а накопленные события — в журнал outbox; при ошибке копия и буфер отбрасываются.
// Транзакции сериализуются мьютексом: вложенные WithinTransaction не
// поддерживаются.
type UnitOfWork struct {
	mu      sync.Mutex
	repo    *persistence.Repository
	journal platformoutbox.Journal
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
		journal = platformoutbox.NewMemoryJournal()
	}
	return &UnitOfWork{repo: repo, journal: journal}
}

// WithinTransaction выполняет fn в одной транзакции.
//
// ВНИМАНИЕ: in-memory реализация неатомарна между переносом состояния агрегата
// (ReplaceWith) и добавлением событий в журнал: падение процесса между этими
// шагами сохранит публикацию без события. Для реального хранилища оба шага
// обязаны выполняться в одной транзакции БД — иначе transactional outbox теряет
// гарантию атомарности записи агрегата и события.
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

func (t *transaction) Publications() ports.PublicationRepository { return t.repo }
func (t *transaction) Outbox() ports.Outbox                      { return t.outbox }

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
