package components

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Примитив ограничения частоты чувствительных действий (web-frontend-architecture,
// срез 7, 7.5; design.md, Решение 15): вход оператора, необратимые решения,
// повторные отправки. Общий слой даёт механизм; пороги задаёт потребитель,
// потому что порог — свойство сценария, а не разметки.

// RateLimitConfig настраивает ограничитель. Окно фиксированной ширины на ключ:
// предсказуемое правило, достаточное против подбора пароля и случайных повторов.
type RateLimitConfig struct {
	// Limit — число запросов, разрешённых в окне на один ключ. Неположительный
	// предел означает «без ограничения» — middleware пропускает запросы.
	Limit int
	// Window — ширина окна. Неположительное окно означает «без ограничения».
	Window time.Duration
	// KeyFunc извлекает ключ лимита из запроса. Пустой ключ означает «не
	// ограничивать» (например, нет данных для ключа). По умолчанию — адрес
	// клиента без порта.
	KeyFunc func(*http.Request) string
	// Now возвращает текущее время; nil — time.Now. Поле существует для
	// детерминированных тестов.
	Now func() time.Time
	// OnBlocked вызывается при каждом отклонённом запросе: факт блокировки
	// фиксируется потребителем (лог, метрика, запись аудита). nil — фиксация не
	// требуется.
	OnBlocked func(r *http.Request, key string)
}

// RateLimit возвращает middleware, отклоняющий запросы сверх порога кодом 429.
// Ограничитель держит состояние в памяти процесса: это защита одного экземпляра
// сервиса, а не распределённый лимит.
func RateLimit(config RateLimitConfig) func(http.Handler) http.Handler {
	keyFunc := config.KeyFunc
	if keyFunc == nil {
		keyFunc = clientKey
	}
	nowFunc := config.Now
	if nowFunc == nil {
		nowFunc = time.Now
	}
	limiter := &rateLimiter{
		limit:  config.Limit,
		window: config.Window,
		now:    nowFunc,
		state:  make(map[string]*rateWindow),
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := keyFunc(r)
			if !limiter.allow(nowFunc(), key) {
				if config.OnBlocked != nil {
					config.OnBlocked(r, key)
				}
				w.Header().Set("Retry-After", strconv.Itoa(int(limiter.window.Seconds())))
				http.Error(w, "слишком много запросов: повторите позже", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// rateLimiter — счётчик окна фиксированной ширины по ключу.
type rateLimiter struct {
	limit  int
	window time.Duration
	now    func() time.Time

	mu    sync.Mutex
	state map[string]*rateWindow
}

// rateWindow — состояние окна одного ключа.
type rateWindow struct {
	count int
	reset time.Time
}

// allow сообщает, разрешён ли запрос по ключу в момент now. Пустой ключ и
// неположительные предел/окно не ограничиваются. Истёкшие окна очищаются на
// каждом обращении, поэтому карта не растёт бесконечно.
func (l *rateLimiter) allow(now time.Time, key string) bool {
	if key == "" || l.limit <= 0 || l.window <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for candidate, window := range l.state {
		if now.Sub(window.reset) >= 0 {
			delete(l.state, candidate)
		}
	}
	window, ok := l.state[key]
	if !ok {
		l.state[key] = &rateWindow{count: 1, reset: now.Add(l.window)}
		return true
	}
	if window.count >= l.limit {
		return false
	}
	window.count++
	return true
}

// clientKey — адрес клиента без порта. Пустой RemoteAddr даёт пустой ключ и
// лимит не применяется: отсутствие данных не превращается в общий счётчик.
func clientKey(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
