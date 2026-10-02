package inmemory

import (
	"context"
	"errors"
	"sync"

	persistence "lead-gen/backend/contexts/message-delivery/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/message-delivery/internal/domain"
	"lead-gen/backend/contexts/message-delivery/internal/ports"
)

// UnitOfWork — in-memory реализация ports.UnitOfWork для тестов и локального
// запуска.
//
// Транзакция работает с копиями репозиториев и буфером outbox. При успешном
// завершении функции состояние копий переносится в основные репозитории, а
// накопленные события — в журнал outbox; при ошибке копии и буфер отбрасываются,
// поэтому ни агрегат, ни событие не сохраняются.
//
// Транзакции сериализуются мьютексом: вложенные WithinTransaction не
// поддерживаются и привели бы к взаимной блокировке.
type UnitOfWork struct {
	mu      sync.Mutex
	msgs    *persistence.Messages
	reps    *persistence.Reputations
	journal *Journal
}

// UnitOfWork реализует порт транзакции.
var _ ports.UnitOfWork = (*UnitOfWork)(nil)

// NewUnitOfWork создаёт unit-of-work с собственными репозиториями и журналом.
func NewUnitOfWork() *UnitOfWork {
	return &UnitOfWork{
		msgs:    persistence.NewMessages(),
		reps:    persistence.NewReputations(),
		journal: NewJournal(),
	}
}

// NewUnitOfWorkWith собирает unit-of-work из готовых репозиториев и журнала.
// Нужен, когда приложение и relay должны разделять одно состояние.
func NewUnitOfWorkWith(msgs *persistence.Messages, reps *persistence.Reputations, journal *Journal) *UnitOfWork {
	if msgs == nil {
		msgs = persistence.NewMessages()
	}
	if reps == nil {
		reps = persistence.NewReputations()
	}
	if journal == nil {
		journal = NewJournal()
	}
	return &UnitOfWork{msgs: msgs, reps: reps, journal: journal}
}

// Messages возвращает репозиторий сообщений, состояние которого меняется
// коммитами транзакций. Чтение вне транзакции видит последнее закоммиченное
// состояние.
func (u *UnitOfWork) Messages() *persistence.Messages { return u.msgs }

// Reputations возвращает репозиторий репутаций, который читает relay и сценарии.
func (u *UnitOfWork) Reputations() *persistence.Reputations { return u.reps }

// Journal возвращает журнал outbox, который читает relay.
func (u *UnitOfWork) Journal() *Journal { return u.journal }

// WithinTransaction выполняет fn в одной транзакции.
//
// ВНИМАНИЕ: in-memory реализация неатомарна между переносом агрегатов
// (ReplaceWith) и добавлением событий в журнал (appendEvents): падение процесса
// между этими шагами сохранит агрегат без события. Для реального хранилища оба
// шага обязаны выполняться в одной транзакции БД — иначе transactional outbox
// теряет гарантию атомарности записи агрегата и события.
func (u *UnitOfWork) WithinTransaction(_ context.Context, fn func(tx ports.Transaction) error) error {
	if fn == nil {
		return errors.New("inmemory: WithinTransaction без функции транзакции")
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	workingMessages := u.msgs.Clone()
	workingReputations := u.reps.Clone()
	outbox := &transactionOutbox{}
	tx := &transaction{
		messages:    workingMessages,
		reputations: workingReputations,
		outbox:      outbox,
	}
	if err := fn(tx); err != nil {
		return err
	}

	u.msgs.ReplaceWith(workingMessages)
	u.reps.ReplaceWith(workingReputations)
	u.journal.appendEvents(outbox.events...)
	return nil
}

// transaction — фасад над транзакцией: репозитории-копии и буфер outbox.
type transaction struct {
	messages    *persistence.Messages
	reputations *persistence.Reputations
	outbox      *transactionOutbox
}

func (t *transaction) Messages() ports.MessageRepository { return t.messages }
func (t *transaction) Reputations() ports.SenderReputationRepository {
	return t.reputations
}
func (t *transaction) Outbox() ports.Outbox { return t.outbox }

// transactionOutbox копит события до commit. Ни один метод не публикует события
// сам: перенос в журнал делает UnitOfWork после успешного завершения.
type transactionOutbox struct {
	events []domain.DomainEvent
}

func (o *transactionOutbox) Append(_ context.Context, events ...domain.DomainEvent) error {
	o.events = append(o.events, events...)
	return nil
}
