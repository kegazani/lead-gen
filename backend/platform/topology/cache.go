package topology

import (
	"sync"
	"time"
)

// Loader — потокобезопасный короткий кэш модели графа. Модель меняется только при
// изменении каталога или деплоя, поэтому повторная сборка в пределах интервала не
// перечитывает файлы. Нулевой ttl означает отсутствие кэширования.
type Loader struct {
	sources Sources
	ttl     time.Duration
	now     func() time.Time

	mu       sync.Mutex
	cached   Graph
	cachedAt time.Time
	hasCache bool
}

// NewLoader создаёт загрузчик с кэшем на интервал ttl.
func NewLoader(sources Sources, ttl time.Duration) *Loader {
	return &Loader{sources: sources, ttl: ttl, now: time.Now}
}

// Load возвращает модель графа. В пределах ttl отдаётся закэшированная копия;
// иначе граф пересобирается. Доступ потокобезопасен.
func (l *Loader) Load() Graph {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if l.hasCache && l.ttl > 0 && now.Sub(l.cachedAt) < l.ttl {
		return l.cached
	}
	graph := Build(l.sources)
	l.cached = graph
	l.cachedAt = now
	l.hasCache = true
	return graph
}

// Invalidate сбрасывает кэш: следующий Load пересоберёт граф.
func (l *Loader) Invalidate() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.hasCache = false
}
