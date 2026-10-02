// Package inmemory реализует transactional outbox и unit-of-work контекста
// audience поверх оперативной памяти: изменение сегмента или фиксация снимка и
// запись их событий выполняются в одной транзакции, а публикацию подтверждённых
// записей выполняет relay, читающий журнал платформы
// (platform/messaging/outbox).
//
// Откат транзакции не оставляет ни агрегата, ни события. Пакет не знает о
// брокере: журнал принимает готовые записи outbox, которые роль worker публикует
// транспортом.
package inmemory

import (
	"context"
	"errors"
	"sync"

	persistence "lead-gen/backend/contexts/audience/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/audience/internal/domain"
	"lead-gen/backend/contexts/audience/internal/ports"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
)

// UnitOfWork — in-memory реализация ports.UnitOfWork для тестов и локального
// запуска.
//
// Транзакция работает с копиями репозиториев и буфером outbox. При успешном
// завершении функции состояние копий переносится в основные репозитории, а
// накопленные события — в журнал outbox; при ошибке копии и буфер отбрасываются.
// Транзакции сериализуются мьютексом: вложенные WithinTransaction не
// поддерживаются.
type UnitOfWork struct {
	mu        sync.Mutex
	segments  *persistence.SegmentRepository
	snapshots *persistence.SnapshotRepository
	journal   platformoutbox.Journal
}

// UnitOfWork реализует порт транзакции.
var _ ports.UnitOfWork = (*UnitOfWork)(nil)

// NewUnitOfWorkWith собирает unit-of-work из готовых репозиториев и журнала.
// Нужен, когда приложение и worker должны разделять одно состояние.
func NewUnitOfWorkWith(
	segments *persistence.SegmentRepository,
	snapshots *persistence.SnapshotRepository,
	journal platformoutbox.Journal,
) *UnitOfWork {
	if segments == nil {
		segments = persistence.NewSegmentRepository()
	}
	if snapshots == nil {
		snapshots = persistence.NewSnapshotRepository()
	}
	if journal == nil {
		journal = platformoutbox.NewMemoryJournal()
	}
	return &UnitOfWork{segments: segments, snapshots: snapshots, journal: journal}
}

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

	working := &transaction{
		segments:  u.segments.Clone(),
		snapshots: u.snapshots.Clone(),
		outbox:    &transactionOutbox{},
	}
	if err := fn(working); err != nil {
		return err
	}

	u.segments.ReplaceWith(working.segments)
	u.snapshots.ReplaceWith(working.snapshots)
	return working.outbox.flush(ctx, u.journal)
}

// transaction — фасад над транзакцией: копии репозиториев и буфер outbox.
type transaction struct {
	segments  *persistence.SegmentRepository
	snapshots *persistence.SnapshotRepository
	outbox    *transactionOutbox
}

func (t *transaction) Segments() ports.SegmentRepository           { return t.segments }
func (t *transaction) Snapshots() ports.AudienceSnapshotRepository { return t.snapshots }
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
