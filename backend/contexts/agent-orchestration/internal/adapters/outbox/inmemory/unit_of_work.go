package inmemory

import (
	"context"
	"errors"
	"sync"

	"lead-gen/backend/contexts/agent-orchestration/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
)

// UnitOfWork — in-memory реализация ports.UnitOfWork для тестов и локального
// запуска.
//
// Транзакция работает с копией хранилища и буфером outbox. При успешном завершении
// функции состояние копии переносится в основное хранилище, а накопленные события —
// в журнал outbox; при ошибке копия и буфер отбрасываются. Транзакции
// сериализуются мьютексом: вложенные WithinTransaction не поддерживаются.
type UnitOfWork struct {
	mu      sync.Mutex
	store   *inmemory.Store
	journal platformoutbox.Journal
}

// UnitOfWork реализует порт транзакции.
var _ ports.UnitOfWork = (*UnitOfWork)(nil)

// NewUnitOfWork собирает unit-of-work из хранилища и журнала. nil-хранилище —
// новое пустое, nil-журнал — журнал в памяти.
func NewUnitOfWork(store *inmemory.Store, journal platformoutbox.Journal) *UnitOfWork {
	if store == nil {
		store = inmemory.NewStore()
	}
	if journal == nil {
		journal = platformoutbox.NewMemoryJournal()
	}
	return &UnitOfWork{store: store, journal: journal}
}

// Store возвращает основное хранилище unit-of-work: приложение и relay должны
// разделять одно состояние с транзакцией.
func (u *UnitOfWork) Store() *inmemory.Store { return u.store }

// WithinTransaction выполняет fn в одной транзакции.
//
// ВНИМАНИЕ: in-memory реализация неатомарна между переносом состояния хранилища
// (ReplaceWith) и добавлением событий в журнал: падение процесса между этими шагами
// сохранит агрегат без события. Для реального хранилища оба шага обязаны
// выполняться в одной транзакции БД — иначе transactional outbox теряет гарантию
// атомарности записи агрегата и события.
func (u *UnitOfWork) WithinTransaction(ctx context.Context, fn func(tx ports.Transaction) error) error {
	if fn == nil {
		return errors.New("inmemory: WithinTransaction без функции транзакции")
	}
	u.mu.Lock()
	defer u.mu.Unlock()

	working := u.store.Clone()
	outbox := &transactionOutbox{}
	tx := &transaction{
		pipelines: inmemory.NewPipelineRepository(working),
		runs:      inmemory.NewRunRepository(working),
		outbox:    outbox,
	}
	if err := fn(tx); err != nil {
		return err
	}
	u.store.ReplaceWith(working)
	return outbox.flush(ctx, u.journal)
}

// transaction — фасад над транзакцией: репозитории-копии и буфер outbox.
type transaction struct {
	pipelines *inmemory.PipelineRepository
	runs      *inmemory.RunRepository
	outbox    *transactionOutbox
}

func (t *transaction) Pipelines() ports.PipelineRepository { return t.pipelines }
func (t *transaction) Runs() ports.RunRepository           { return t.runs }
func (t *transaction) Outbox() ports.Outbox                { return t.outbox }

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
