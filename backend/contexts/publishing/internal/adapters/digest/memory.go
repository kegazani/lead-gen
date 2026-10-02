package digest

import (
	"context"
	"fmt"
	"sync"

	"lead-gen/backend/contexts/publishing/internal/domain"
	"lead-gen/backend/contexts/publishing/internal/ports"
)

// Memory — детерминированный in-memory источник выжимок для среза и тестов.
// Выжимки кладутся по идентификатору; сырых сообщений и авторов здесь нет по
// определению типа domain.Digest.
type Memory struct {
	mu     sync.RWMutex
	digest map[string]domain.Digest
}

// Memory реализует порт источника выжимок.
var _ ports.DigestSource = (*Memory)(nil)

// NewMemory создаёт пустой источник.
func NewMemory() *Memory {
	return &Memory{digest: make(map[string]domain.Digest)}
}

// Put кладёт выжимку. Пустой идентификатор отвергается: ссылка обязана быть
// разрешимой.
func (m *Memory) Put(d domain.Digest) error {
	if d.Ref.ID == "" {
		return fmt.Errorf("digest: выжимка без идентификатора")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.digest[d.Ref.ID] = d
	return nil
}

// Fetch реализует ports.DigestSource. Незнакомая ссылка даёт ErrDigestNotFound.
func (m *Memory) Fetch(_ context.Context, ref domain.DigestRef) (domain.Digest, error) {
	m.mu.RLock()
	d, ok := m.digest[ref.ID]
	m.mu.RUnlock()
	if !ok {
		return domain.Digest{}, fmt.Errorf("%w: %s", ports.ErrDigestNotFound, ref.ID)
	}
	return d, nil
}
