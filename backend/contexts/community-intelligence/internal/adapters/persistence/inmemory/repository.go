package inmemory

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"lead-gen/backend/contexts/community-intelligence/internal/domain"
	"lead-gen/backend/contexts/community-intelligence/internal/ports"
)

// Repository — in-memory реализация ports.DigestRepository.
//
// Состояние защищено мьютексом, поэтому репозиторий пригоден для
// `go test -race` и параллельных читателей.
type Repository struct {
	mu      sync.RWMutex
	digests map[domain.DigestID]domain.CommunityDigestSnapshot
}

// Repository реализует порт хранилища выжимок.
var _ ports.DigestRepository = (*Repository)(nil)

// New создаёт пустое хранилище.
func New() *Repository {
	return &Repository{digests: make(map[domain.DigestID]domain.CommunityDigestSnapshot)}
}

// Save сохраняет агрегат целиком как снимок. Повторный вызов для той же выжимки
// заменяет прежний снимок (upsert по идентификатору).
func (r *Repository) Save(_ context.Context, digest *domain.CommunityDigest) error {
	if digest == nil {
		return fmt.Errorf("inmemory: сохранение nil-выжимки")
	}
	snapshot := digest.Snapshot()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.digests[snapshot.ID] = snapshot
	return nil
}

// FindByID загружает выжимку по идентификатору через domain.RehydrateDigest.
// Отсутствие выжимки даёт ошибку, для которой истинно errors.Is(err,
// ports.ErrDigestNotFound).
func (r *Repository) FindByID(_ context.Context, id domain.DigestID) (*domain.CommunityDigest, error) {
	r.mu.RLock()
	snapshot, ok := r.digests[id]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ports.ErrDigestNotFound, id)
	}
	digest, err := domain.RehydrateDigest(snapshot)
	if err != nil {
		return nil, fmt.Errorf("inmemory: восстановление выжимки %s: %w", id, err)
	}
	return digest, nil
}

// ListByCommunity возвращает выжимки сообщества в порядке построения (по времени
// создания, затем по идентификатору), чтобы выдача была воспроизводимой.
func (r *Repository) ListByCommunity(_ context.Context, id domain.CommunityID) ([]*domain.CommunityDigest, error) {
	r.mu.RLock()
	ids := make([]domain.DigestID, 0, len(r.digests))
	for digestID, snapshot := range r.digests {
		if snapshot.Source.ID == id {
			ids = append(ids, digestID)
		}
	}
	r.mu.RUnlock()

	sort.Slice(ids, func(i, j int) bool {
		left := r.digests[ids[i]]
		right := r.digests[ids[j]]
		if !left.CreatedAt.Equal(right.CreatedAt) {
			return left.CreatedAt.Before(right.CreatedAt)
		}
		return ids[i] < ids[j]
	})

	digests := make([]*domain.CommunityDigest, 0, len(ids))
	for _, digestID := range ids {
		digest, err := r.FindByID(context.Background(), digestID)
		if err != nil {
			return nil, err
		}
		digests = append(digests, digest)
	}
	return digests, nil
}
