package inmemory

import (
	"context"
	"errors"
	"sync"

	persistence "lead-gen/backend/contexts/platform-governance/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/platform-governance/internal/ports"
)

// UnitOfWork — in-memory реализация ports.UnitOfWork для тестов и локального
// запуска.
//
// Транзакция работает с копией репозитория тенантов, буфером outbox и копией
// множества ключей идемпотентности. При успешном завершении функции состояние
// копии переносится в основной репозиторий, накопленные сообщения — в журнал
// outbox, а ключи — в основное множество; при ошибке копия и буфер
// отбрасываются, поэтому ни агрегат, ни сообщение, ни ключ не сохраняются.
//
// Транзакции сериализуются мьютексом: вложенные WithinTransaction не
// поддерживаются и привели бы к взаимной блокировке.
type UnitOfWork struct {
	mu      sync.Mutex
	repo    *persistence.Repository
	journal *Journal
	keys    map[ports.CommandKey]bool
}

// UnitOfWork реализует порт транзакции.
var _ ports.UnitOfWork = (*UnitOfWork)(nil)

// NewUnitOfWork создаёт unit-of-work с собственным репозиторием и журналом.
func NewUnitOfWork() *UnitOfWork {
	return NewUnitOfWorkWith(persistence.New(), NewJournal())
}

// NewUnitOfWorkWith собирает unit-of-work из готовых репозитория и журнала.
// Нужен, когда приложение и публикация должны разделять одно состояние.
func NewUnitOfWorkWith(repo *persistence.Repository, journal *Journal) *UnitOfWork {
	if repo == nil {
		repo = persistence.New()
	}
	if journal == nil {
		journal = NewJournal()
	}
	return &UnitOfWork{repo: repo, journal: journal, keys: make(map[ports.CommandKey]bool)}
}

// Repository возвращает репозиторий, состояние которого меняется коммитами
// транзакций. Чтение вне транзакции видит последнее закоммиченное состояние.
func (u *UnitOfWork) Repository() *persistence.Repository { return u.repo }

// Journal возвращает журнал outbox, который читает публикация.
func (u *UnitOfWork) Journal() *Journal { return u.journal }

// WithinTransaction выполняет fn в одной транзакции.
//
// ВНИМАНИЕ: in-memory реализация неатомарна между переносом агрегата
// (ReplaceWith), добавлением сообщений в журнал и ключей идемпотентности:
// падение процесса между этими шагами может оставить агрегат без события или
// без ключа. Для реального хранилища шаги обязаны выполняться в одной транзакции
// БД — иначе transactional outbox теряет гарантию атомарности.
func (u *UnitOfWork) WithinTransaction(_ context.Context, fn func(tx ports.Transaction) error) error {
	if fn == nil {
		return errors.New("inmemory: WithinTransaction без функции транзакции")
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	working := u.repo.Clone()
	workingKeys := make(map[ports.CommandKey]bool, len(u.keys))
	for key := range u.keys {
		workingKeys[key] = true
	}
	outbox := &transactionOutbox{}
	tx := &transaction{
		repo:   working,
		outbox: outbox,
		idem:   &transactionIdempotency{keys: workingKeys},
	}
	if err := fn(tx); err != nil {
		return err
	}

	u.repo.ReplaceWith(working)
	u.journal.appendMessages(outbox.messages...)
	u.keys = workingKeys
	return nil
}

// transaction — фасад над транзакцией: репозиторий-копия, буфер outbox и
// множество ключей идемпотентности.
type transaction struct {
	repo   *persistence.Repository
	outbox *transactionOutbox
	idem   *transactionIdempotency
}

func (t *transaction) Tenants() ports.TenantRepository { return t.repo }
func (t *transaction) Outbox() ports.Outbox            { return t.outbox }
func (t *transaction) Idempotency() ports.Idempotency  { return t.idem }

// transactionOutbox копит сообщения до commit. Ни один метод не публикует
// сообщения сам: перенос в журнал делает UnitOfWork после успешного завершения.
type transactionOutbox struct {
	messages []ports.Message
}

func (o *transactionOutbox) Append(_ context.Context, messages ...ports.Message) error {
	o.messages = append(o.messages, messages...)
	return nil
}

// transactionIdempotency — множество ключей команд в рамках транзакции.
type transactionIdempotency struct {
	keys map[ports.CommandKey]bool
}

func (i *transactionIdempotency) Seen(_ context.Context, key ports.CommandKey) (bool, error) {
	return i.keys[key], nil
}

func (i *transactionIdempotency) Remember(_ context.Context, key ports.CommandKey) error {
	i.keys[key] = true
	return nil
}
