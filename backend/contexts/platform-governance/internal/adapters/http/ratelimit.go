package httpapi

import (
	"net/http"
	"time"

	"lead-gen/backend/platform/observability/observ"
	"lead-gen/frontend/components"
)

// Ограничение частоты чувствительных действий (web-frontend-architecture, срез 7,
// 7.5; service-topology-console, 7.4). Порог задаёт потребитель: у входа,
// необратимых решений и прокси метрик он свой. Ограничитель держит состояние в
// памяти процесса — это защита экземпляра, а не распределённый лимит.
const (
	// sensitivePostLimit — предельное число чувствительных POST в окне.
	sensitivePostLimit = 30
	// sensitivePostWindow — ширина окна ограничения чувствительных POST.
	sensitivePostWindow = time.Minute
	// topologyMetricsLimit — предельное число запросов к прокси метрик в минуту:
	// экран не должен становиться усилителем нагрузки на Prometheus.
	topologyMetricsLimit = 120
)

// sensitive оборачивает чувствительный POST собственным ограничителем частоты:
// каждый маршрут получает отдельный счётчик, поэтому всплеск на одном действии не
// блокирует другие.
func (s *Server) sensitive(next http.HandlerFunc) http.Handler {
	return components.RateLimit(components.RateLimitConfig{
		Limit:     sensitivePostLimit,
		Window:    sensitivePostWindow,
		OnBlocked: s.onRateLimited,
	})(next)
}

// topologyMetricsLimit ограничивает частоту запросов к серверному прокси метрик.
func (s *Server) topologyMetricsLimit(next http.HandlerFunc) http.Handler {
	return components.RateLimit(components.RateLimitConfig{
		Limit:     topologyMetricsLimit,
		Window:    time.Minute,
		OnBlocked: s.onRateLimited,
	})(next)
}

// onRateLimited фиксирует факт блокировки структурированным логом без персональных
// данных: причина, маршрут и признак превышения, но не значения формы.
func (s *Server) onRateLimited(r *http.Request, _ string) {
	if s.deps.Logger == nil {
		return
	}
	s.deps.Logger.Warn("console: превышен предел частоты чувствительных действий",
		observ.Fields{}, "service", consoleServiceName, "route", r.URL.Path)
}
