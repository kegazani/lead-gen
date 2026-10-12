package inmemory

import (
	"context"
	"errors"
	"sync"

	persistence "lead-gen/backend/contexts/outreach-orchestration/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

// UnitOfWork — in-memory реализация ports.UnitOfWork для тестов и локального
// запуска.
//
// Транзакция работает с копией репозитория кампаний и буфером outbox. При
// успешном завершении функции состояние копии переносится в основной
// репозиторий, а накопленные события — в журнал outbox; при ошибке копия и
// буфер отбрасываются, поэтому ни агрегат, ни событие не сохраняются.
//
// Транзакции сериализуются мьютексом: вложенные WithinTransaction не
// поддерживаются и привели бы к взаимной блокировке.
type UnitOfWork struct {
	mu      sync.Mutex
	repo    *persistence.Repository
	journal *Journal
	fences  map[string]memoryFence
}

type memoryFence struct {
	tenantID   string
	requestID  string
	campaignID string
	cancelled  bool
}

// UnitOfWork реализует порт транзакции.
var _ ports.UnitOfWork = (*UnitOfWork)(nil)

// NewUnitOfWork создаёт unit-of-work с собственным репозиторием и журналом.
func NewUnitOfWork() *UnitOfWork {
	return &UnitOfWork{repo: persistence.New(), journal: NewJournal(), fences: make(map[string]memoryFence)}
}

// NewUnitOfWorkWith собирает unit-of-work из готовых репозитория и журнала.
// Нужен, когда приложение и relay должны разделять одно состояние.
func NewUnitOfWorkWith(repo *persistence.Repository, journal *Journal) *UnitOfWork {
	if repo == nil {
		repo = persistence.New()
	}
	if journal == nil {
		journal = NewJournal()
	}
	return &UnitOfWork{repo: repo, journal: journal, fences: make(map[string]memoryFence)}
}

// Repository возвращает репозиторий, состояние которого меняется коммитами
// транзакций. Чтение вне транзакции видит последнее закоммиченное состояние.
func (u *UnitOfWork) Repository() *persistence.Repository { return u.repo }

// Journal возвращает журнал outbox, который читает relay.
func (u *UnitOfWork) Journal() *Journal { return u.journal }

// WithinTransaction выполняет fn в одной транзакции.
//
// ВНИМАНИЕ: in-memory реализация неатомарна между переносом агрегата
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

	working := u.repo.Clone()
	outbox := &transactionOutbox{}
	fences := cloneFences(u.fences)
	if err := fn(&transaction{repo: working, outbox: outbox, fences: fences}); err != nil {
		return err
	}

	u.repo.ReplaceWith(working)
	u.fences = fences
	u.journal.appendEvents(outbox.events...)
	return nil
}

// transaction — фасад над транзакцией: репозиторий-копия и буфер outbox.
type transaction struct {
	repo   *persistence.Repository
	outbox *transactionOutbox
	fences map[string]memoryFence
}

func (t *transaction) Campaigns() ports.CampaignRepository { return t.repo }
func (t *transaction) Outbox() ports.Outbox                { return t.outbox }
func (t *transaction) RequestCancellationFence() ports.RequestCancellationFence {
	return memoryRequestFence{fences: t.fences}
}

func cloneFences(source map[string]memoryFence) map[string]memoryFence {
	copy := make(map[string]memoryFence, len(source))
	for key, fence := range source {
		copy[key] = fence
	}
	return copy
}

type memoryRequestFence struct{ fences map[string]memoryFence }

func fenceKey(tenantID, requestID string) string { return tenantID + "\x00" + requestID }

func (f memoryRequestFence) Cancel(_ context.Context, tenantID, requestID string) error {
	key := fenceKey(tenantID, requestID)
	entry := f.fences[key]
	entry.tenantID, entry.requestID, entry.cancelled = tenantID, requestID, true
	f.fences[key] = entry
	return nil
}

func (f memoryRequestFence) RegisterHandoff(_ context.Context, tenantID, requestID, campaignID string) (bool, error) {
	key := fenceKey(tenantID, requestID)
	entry := f.fences[key]
	entry.tenantID, entry.requestID, entry.campaignID = tenantID, requestID, campaignID
	f.fences[key] = entry
	return entry.cancelled, nil
}

func (f memoryRequestFence) CampaignCancelled(_ context.Context, tenantID, campaignID string) (bool, error) {
	for _, entry := range f.fences {
		if entry.tenantID == tenantID && entry.campaignID == campaignID && entry.cancelled {
			return true, nil
		}
	}
	return false, nil
}

// transactionOutbox копит события до commit. Ни один метод не публикует
// события сам: перенос в журнал делает UnitOfWork после успешного завершения.
type transactionOutbox struct {
	events []domain.DomainEvent
}

func (o *transactionOutbox) Append(_ context.Context, events ...domain.DomainEvent) error {
	o.events = append(o.events, events...)
	return nil
}
