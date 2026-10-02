package platform

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"lead-gen/backend/contexts/publishing/internal/ports"
)

// Deterministic — детерминированный in-memory адаптер порта публикации для среза
// и тестов: без сети он выдаёт предсказуемый адрес материала и позволяет заранее
// задать отказ площадки или неготовность канала.
//
// Он не подменяет официальный интерфейс, а исполняет роль площадки в проверках:
// наблюдаемое поведение повторяет реальный адаптер — идемпотентность по
// PublicationID, различимые отказ и неготовность.
type Deterministic struct {
	mu        sync.Mutex
	published []ports.PublishRequest
	rejected  map[string]string
	notReady  map[string]bool
}

// Deterministic реализует порт публикации.
var _ ports.PlatformPublisher = (*Deterministic)(nil)

// NewDeterministic создаёт адаптер, который считает все каналы готовыми и
// принимает все материалы.
func NewDeterministic() *Deterministic {
	return &Deterministic{
		rejected: make(map[string]string),
		notReady: make(map[string]bool),
	}
}

// WithRejected помечает площадку как отклоняющую публикацию с заданной причиной.
// Возвращает адаптер для цепочки настройки.
func (d *Deterministic) WithRejected(platform, reason string) *Deterministic {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.rejected[strings.ToLower(strings.TrimSpace(platform))] = reason
	return d
}

// WithNotReady помечает канал площадки как неготовый.
func (d *Deterministic) WithNotReady(platform string) *Deterministic {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.notReady[strings.ToLower(strings.TrimSpace(platform))] = true
	return d
}

// Ready реализует ports.PlatformPublisher.
func (d *Deterministic) Ready(_ context.Context, platform, _ string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.notReady[strings.ToLower(strings.TrimSpace(platform))] {
		return fmt.Errorf("%w: %s", ports.ErrChannelNotReady, platform)
	}
	return nil
}

// Publish реализует ports.PlatformPublisher. При заданном отказе возвращается
// ErrPublicationRejected с причиной; иначе выдаётся детерминированный адрес.
func (d *Deterministic) Publish(_ context.Context, req ports.PublishRequest) (ports.PublishResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	key := strings.ToLower(strings.TrimSpace(req.Platform))
	if reason, ok := d.rejected[key]; ok {
		return ports.PublishResult{}, fmt.Errorf("%w: %s: %s", ports.ErrPublicationRejected, req.Platform, reason)
	}
	d.published = append(d.published, req)
	return ports.PublishResult{
		Address: fmt.Sprintf("https://%s.example/%s/%s", key, req.Account, req.PublicationID),
	}, nil
}

// Published возвращает копию принятых запросов публикации. Метод нужен тестам:
// интерфейс порта им не ограничивается.
func (d *Deterministic) Published() []ports.PublishRequest {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]ports.PublishRequest, len(d.published))
	copy(out, d.published)
	return out
}
