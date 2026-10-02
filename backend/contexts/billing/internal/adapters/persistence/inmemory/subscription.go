package inmemory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"lead-gen/backend/contexts/billing/internal/domain"
	"lead-gen/backend/contexts/billing/internal/ports"
)

// SubscriptionRepository — in-memory реализация ports.SubscriptionRepository.
//
// Состояние защищено мьютексом, поэтому репозиторий пригоден для `go test -race`
// и параллельных читателей. Методы Clone и ReplaceWith существуют для
// unit-of-work: транзакция работает с копией, а commit переносит её состояние.
type SubscriptionRepository struct {
	mu            sync.RWMutex
	subscriptions map[domain.SubscriptionID]domain.SubscriptionSnapshot
}

// SubscriptionRepository реализует порт репозитория подписок.
var _ ports.SubscriptionRepository = (*SubscriptionRepository)(nil)

// NewSubscriptionRepository создаёт пустой репозиторий.
func NewSubscriptionRepository() *SubscriptionRepository {
	return &SubscriptionRepository{subscriptions: make(map[domain.SubscriptionID]domain.SubscriptionSnapshot)}
}

// Save сохраняет агрегат целиком как снимок с оптимистичной блокировкой.
func (r *SubscriptionRepository) Save(_ context.Context, subscription *domain.SubscriptionInvoice, expectedVersion int) error {
	if subscription == nil {
		return errors.New("inmemory: сохранение nil-подписки")
	}
	snapshot := subscription.Snapshot()
	r.mu.Lock()
	defer r.mu.Unlock()

	if stored, ok := r.subscriptions[snapshot.ID]; ok {
		if stored.Version != expectedVersion {
			return fmt.Errorf(
				"%w: подписка %s: сохранено %d, ожидалось %d",
				ports.ErrVersionConflict, snapshot.ID, stored.Version, expectedVersion,
			)
		}
	} else if expectedVersion != 0 {
		return fmt.Errorf(
			"%w: подписка %s: нет сохранённой версии, ожидалось %d",
			ports.ErrVersionConflict, snapshot.ID, expectedVersion,
		)
	}

	r.subscriptions[snapshot.ID] = snapshot
	return nil
}

// FindByID загружает подписку по идентификатору через
// domain.RehydrateSubscription.
func (r *SubscriptionRepository) FindByID(_ context.Context, id domain.SubscriptionID) (*domain.SubscriptionInvoice, error) {
	r.mu.RLock()
	snapshot, ok := r.subscriptions[id]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ports.ErrSubscriptionNotFound, id)
	}
	subscription, err := domain.RehydrateSubscription(snapshot)
	if err != nil {
		return nil, fmt.Errorf("inmemory: восстановление подписки %s: %w", id, err)
	}
	return subscription, nil
}

// FindByTenant загружает подписку тенанта; при нескольких подписках возвращается
// с наименьшим идентификатором.
func (r *SubscriptionRepository) FindByTenant(ctx context.Context, tenantID domain.TenantID) (*domain.SubscriptionInvoice, error) {
	r.mu.RLock()
	var found domain.SubscriptionID
	ok := false
	for id, snapshot := range r.subscriptions {
		if snapshot.TenantID != tenantID {
			continue
		}
		if !ok || id < found {
			found = id
			ok = true
		}
	}
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: тенант %s", ports.ErrSubscriptionNotFound, tenantID)
	}
	return r.FindByID(ctx, found)
}

// List возвращает все подписки, упорядоченные по идентификатору.
func (r *SubscriptionRepository) List(_ context.Context) ([]*domain.SubscriptionInvoice, error) {
	r.mu.RLock()
	ids := make([]domain.SubscriptionID, 0, len(r.subscriptions))
	for id := range r.subscriptions {
		ids = append(ids, id)
	}
	r.mu.RUnlock()
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	out := make([]*domain.SubscriptionInvoice, 0, len(ids))
	for _, id := range ids {
		subscription, err := r.FindByID(context.Background(), id)
		if err != nil {
			return nil, err
		}
		out = append(out, subscription)
	}
	return out, nil
}

// Clone возвращает независимую копию репозитория для одной транзакции.
func (r *SubscriptionRepository) Clone() *SubscriptionRepository {
	r.mu.RLock()
	defer r.mu.RUnlock()
	clone := NewSubscriptionRepository()
	for id, snapshot := range r.subscriptions {
		clone.subscriptions[id] = snapshot
	}
	return clone
}

// ReplaceWith заменяет состояние репозитория состоянием другого. Вызывается
// unit-of-work в момент commit.
func (r *SubscriptionRepository) ReplaceWith(other *SubscriptionRepository) {
	other.mu.RLock()
	snapshots := make(map[domain.SubscriptionID]domain.SubscriptionSnapshot, len(other.subscriptions))
	for id, snapshot := range other.subscriptions {
		snapshots[id] = snapshot
	}
	other.mu.RUnlock()

	r.mu.Lock()
	r.subscriptions = snapshots
	r.mu.Unlock()
}
