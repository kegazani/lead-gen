package inmemory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"lead-gen/backend/contexts/community-intelligence/internal/domain"
	"lead-gen/backend/contexts/community-intelligence/internal/ports"
)

// Registry — in-memory реализация ports.CommunityRegistry и
// ports.CommunityTracker. Хранит только разрешённые источники: регистрация
// неавторизованного источника отвергается доменом.
type Registry struct {
	mu      sync.RWMutex
	sources map[string]domain.CommunitySource
	tracked map[string]ports.TrackedCommunity
}

// Registry реализует порты реестра и списка отслеживания.
var (
	_ ports.CommunityRegistry = (*Registry)(nil)
	_ ports.CommunityTracker  = (*Registry)(nil)
)

// NewRegistry создаёт пустой реестр.
func NewRegistry() *Registry {
	return &Registry{
		sources: make(map[string]domain.CommunitySource),
		tracked: make(map[string]ports.TrackedCommunity),
	}
}

// Register добавляет разрешённый источник и ставит сообщество на отслеживание.
// Тенант обязателен; источник обязан быть уже прошедшим NewCommunitySource.
func (r *Registry) Register(tenant domain.TenantID, source domain.CommunitySource, maxMessages int) error {
	if strings.TrimSpace(string(tenant)) == "" {
		return fmt.Errorf("%w: tenant_id", domain.ErrEmptyID)
	}
	if !source.IsAuthorized() {
		return domain.ErrSourceNotAuthorized
	}
	key := registryKey(tenant, source.ID())
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sources[key] = source
	r.tracked[key] = ports.TrackedCommunity{
		CommunityID: source.ID(),
		TenantID:    tenant,
		MaxMessages: maxMessages,
	}
	return nil
}

// FindByID возвращает разрешённый источник сообщества. Отсутствие даёт ошибку,
// для которой истинно errors.Is(err, ports.ErrCommunityNotFound).
func (r *Registry) FindByID(_ context.Context, tenant domain.TenantID, id domain.CommunityID) (domain.CommunitySource, error) {
	r.mu.RLock()
	source, ok := r.sources[registryKey(tenant, id)]
	r.mu.RUnlock()
	if !ok {
		return domain.CommunitySource{}, fmt.Errorf("%w: %s", ports.ErrCommunityNotFound, id)
	}
	return source, nil
}

// Tracked возвращает отслеживаемые сообщества в порядке (тенант, сообщество).
func (r *Registry) Tracked(_ context.Context) ([]ports.TrackedCommunity, error) {
	r.mu.RLock()
	keys := make([]string, 0, len(r.tracked))
	for key := range r.tracked {
		keys = append(keys, key)
	}
	r.mu.RUnlock()
	sort.Strings(keys)

	out := make([]ports.TrackedCommunity, 0, len(keys))
	for _, key := range keys {
		out = append(out, r.tracked[key])
	}
	return out, nil
}

// registryKey собирает ключ реестра из тенанта и сообщества.
func registryKey(tenant domain.TenantID, id domain.CommunityID) string {
	return string(tenant) + "\x00" + string(id)
}
