// Package inmemory реализует transactional outbox и unit-of-work контекста
// engagement-observation поверх оперативной памяти: изменение события, отметка
// дедупликации сигнала, регистрация трекинг-ссылки и запись доменных событий
// выполняются в одной транзакции, а публикацию подтверждённых записей выполняет
// relay, читающий журнал платформы (platform/messaging/outbox).
//
// Откат транзакции не оставляет ни события, ни отметки дедупликации, ни ссылки.
// Пакет не знает о брокере: журнал принимает готовые записи outbox, которые relay
// публикует в async/amqp.
package inmemory

import (
	"context"
	"errors"
	"sync"

	persistence "lead-gen/backend/contexts/engagement-observation/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/engagement-observation/internal/domain"
	"lead-gen/backend/contexts/engagement-observation/internal/ports"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
)

// UnitOfWork — in-memory реализация ports.UnitOfWork для тестов и локального
// запуска.
//
// Транзакция работает с копиями хранилища, журнала дедупликации и реестра ссылок
// и буфером outbox. При успешном завершении состояние копий переносится в
// основные хранилища, а накопленные события — в журнал outbox; при ошибке копии и
// буфер отбрасываются. Транзакции сериализуются мьютексом.
type UnitOfWork struct {
	mu      sync.Mutex
	repo    *persistence.Repository
	ledger  *persistence.Ledger
	links   *persistence.Links
	journal platformoutbox.Journal
}

// UnitOfWork реализует порт транзакции.
var _ ports.UnitOfWork = (*UnitOfWork)(nil)

// NewUnitOfWorkWith собирает unit-of-work из готовых хранилищ и журнала. Нужен,
// когда приложение и relay должны разделять одно состояние.
func NewUnitOfWorkWith(repo *persistence.Repository, ledger *persistence.Ledger, links *persistence.Links, journal platformoutbox.Journal) *UnitOfWork {
	if repo == nil {
		repo = persistence.New()
	}
	if ledger == nil {
		ledger = persistence.NewLedger()
	}
	if links == nil {
		links = persistence.NewLinks()
	}
	if journal == nil {
		journal = platformoutbox.NewMemoryJournal()
	}
	return &UnitOfWork{repo: repo, ledger: ledger, links: links, journal: journal}
}

// WithinTransaction выполняет fn в одной транзакции.
//
// ВНИМАНИЕ: in-memory реализация неатомарна между переносом состояния хранилищ
// (ReplaceWith) и добавлением событий в журнал: падение процесса между этими
// шагами сохранит событие без записи outbox. Для реального хранилища оба шага
// обязаны выполняться в одной транзакции БД — иначе transactional outbox теряет
// гарантию атомарности записи агрегата и события.
func (u *UnitOfWork) WithinTransaction(ctx context.Context, fn func(tx ports.Transaction) error) error {
	if fn == nil {
		return errors.New("inmemory: WithinTransaction без функции транзакции")
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	working := &transaction{
		repo:   u.repo.Clone(),
		ledger: u.ledger.Clone(),
		links:  u.links.Clone(),
		outbox: &transactionOutbox{},
	}
	if err := fn(working); err != nil {
		return err
	}

	u.repo.ReplaceWith(working.repo)
	u.ledger.ReplaceWith(working.ledger)
	u.links.ReplaceWith(working.links)
	return working.outbox.flush(ctx, u.journal)
}

// transaction — фасад над транзакцией: копии хранилищ и буфер outbox.
type transaction struct {
	repo   *persistence.Repository
	ledger *persistence.Ledger
	links  *persistence.Links
	outbox *transactionOutbox
}

func (t *transaction) Engagements() ports.EngagementRepository { return t.repo }
func (t *transaction) Signals() ports.SignalLedger             { return t.ledger }
func (t *transaction) Links() ports.TransitionLinks            { return t.links }
func (t *transaction) Outbox() ports.Outbox                    { return t.outbox }

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
