package watch

import (
	"context"
	"sync"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

// UnitOfWork оборачивает порт транзакции и уведомляет подписчиков тенантов после
// успешной фиксации изменений. Обёртка не знает о драйвере хранилища: она
// работает с портом, поэтому пригодна и для in-memory, и для PostgreSQL. Это
// честный механизм подписки, а не подмена хранилища: на откат уведомление не
// рассылается.
type UnitOfWork struct {
	inner    ports.UnitOfWork
	notifier *Notifier
}

// UnitOfWork реализует порт транзакции.
var _ ports.UnitOfWork = (*UnitOfWork)(nil)

// Notifier реализует порт подписки на изменения заявок тенанта.
var _ ports.RequestWatcher = (*Notifier)(nil)

// NewUnitOfWork собирает уведомляющую транзакцию поверх готовой.
func NewUnitOfWork(inner ports.UnitOfWork, notifier *Notifier) *UnitOfWork {
	if notifier == nil {
		notifier = NewNotifier()
	}
	return &UnitOfWork{inner: inner, notifier: notifier}
}

// WithinTransaction выполняет fn в транзакции и после успешного коммита
// уведомляет тенантов, чьи заявки были сохранены. Ошибка fn или коммита
// откатывает транзакцию, и уведомление не рассылается.
func (u *UnitOfWork) WithinTransaction(ctx context.Context, fn func(txCtx context.Context, tx ports.Transaction) error) error {
	tracking := newTrackingRepository()
	err := u.inner.WithinTransaction(ctx, func(txCtx context.Context, tx ports.Transaction) error {
		tracking.RequestRepository = tx.Requests()
		return fn(txCtx, &trackedTransaction{Transaction: tx, requests: tracking})
	})
	if err != nil {
		return err
	}
	for _, tenant := range tracking.saved() {
		u.notifier.Notify(tenant)
	}
	return nil
}

// trackedTransaction — фасад транзакции: репозиторий обёрнут для учёта сохранённых
// тенантов, outbox наследуется без изменений.
type trackedTransaction struct {
	ports.Transaction
	requests *trackingRepository
}

func (t *trackedTransaction) Requests() ports.RequestRepository { return t.requests }

// trackingRepository запоминает тенанты, заявки которых сохранялись в транзакции.
// Остальные методы репозитория наследуются от встроенного порта.
type trackingRepository struct {
	ports.RequestRepository
	mu    sync.Mutex
	dirty map[domain.TenantID]struct{}
}

func newTrackingRepository() *trackingRepository {
	return &trackingRepository{dirty: make(map[domain.TenantID]struct{})}
}

// Save сохраняет заявку и отмечает её тенант для уведомления после коммита.
func (r *trackingRepository) Save(ctx context.Context, request *domain.Request, expectedVersion int) error {
	if err := r.RequestRepository.Save(ctx, request, expectedVersion); err != nil {
		return err
	}
	if request != nil {
		r.mu.Lock()
		r.dirty[request.TenantID()] = struct{}{}
		r.mu.Unlock()
	}
	return nil
}

// saved возвращает тенанты, заявки которых сохранялись в этой транзакции.
func (r *trackingRepository) saved() []domain.TenantID {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]domain.TenantID, 0, len(r.dirty))
	for tenant := range r.dirty {
		out = append(out, tenant)
	}
	return out
}
