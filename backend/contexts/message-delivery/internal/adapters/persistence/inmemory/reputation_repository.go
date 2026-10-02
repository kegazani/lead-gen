package inmemory

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"lead-gen/backend/contexts/message-delivery/internal/domain"
	"lead-gen/backend/contexts/message-delivery/internal/ports"
)

// Reputations — in-memory реализация ports.SenderReputationRepository.
//
// Состояние защищено мьютексом. Clone и ReplaceWith служат unit-of-work так же,
// как в репозитории сообщений.
type Reputations struct {
	mu       sync.RWMutex
	byDomain map[domain.SenderDomain]domain.SenderReputationSnapshot
}

// Reputations реализует порт репозитория репутаций.
var _ ports.SenderReputationRepository = (*Reputations)(nil)

// NewReputations создаёт пустой репозиторий репутаций.
func NewReputations() *Reputations {
	return &Reputations{byDomain: make(map[domain.SenderDomain]domain.SenderReputationSnapshot)}
}

// Save сохраняет репутацию домена как снимок (upsert по домену).
func (r *Reputations) Save(_ context.Context, reputation *domain.SenderReputation) error {
	if reputation == nil {
		return errors.New("inmemory: сохранение nil-репутации")
	}
	snapshot := reputation.Snapshot()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byDomain[snapshot.Domain] = snapshot
	return nil
}

// FindByDomain загружает репутацию по отправляющему домену через
// domain.RehydrateSenderReputation.
func (r *Reputations) FindByDomain(_ context.Context, sender domain.SenderDomain) (*domain.SenderReputation, error) {
	r.mu.RLock()
	snapshot, ok := r.byDomain[sender]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ports.ErrSenderReputationNotFound, sender)
	}
	reputation, err := domain.RehydrateSenderReputation(snapshot)
	if err != nil {
		return nil, fmt.Errorf("inmemory: восстановление репутации %s: %w", sender, err)
	}
	return reputation, nil
}

// Clone возвращает независимую копию репозитория для одной транзакции.
func (r *Reputations) Clone() *Reputations {
	r.mu.RLock()
	defer r.mu.RUnlock()
	clone := NewReputations()
	for sender, snapshot := range r.byDomain {
		clone.byDomain[sender] = snapshot
	}
	return clone
}

// ReplaceWith заменяет состояние репозитория состоянием другого при commit.
func (r *Reputations) ReplaceWith(other *Reputations) {
	other.mu.RLock()
	byDomain := make(map[domain.SenderDomain]domain.SenderReputationSnapshot, len(other.byDomain))
	for sender, snapshot := range other.byDomain {
		byDomain[sender] = snapshot
	}
	other.mu.RUnlock()

	r.mu.Lock()
	r.byDomain = byDomain
	r.mu.Unlock()
}
