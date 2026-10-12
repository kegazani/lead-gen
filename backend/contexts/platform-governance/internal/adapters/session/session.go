// Package session реализует серверные сессии портала клиента контекста
// platform-governance: cookie хранит только случайный токен, а соответствие
// токена тенанту и пользователю живёт в серверном store (память процесса либо
// PostgreSQL). Пакет не знает о разметке и не тянет доменных зависимостей.
package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// CookieName — имя cookie серверной сессии портала.
const CookieName = "portal_session"

// DefaultIdleTimeout — период неактивности, после которого сессия истекает.
const DefaultIdleTimeout = 24 * time.Hour

// Record — сохранённая серверная сессия портала: плоское представление, которое
// читает и пишет любой Store.
type Record struct {
	// ID — идентификатор сессии (значение cookie).
	ID string
	// TenantID — тенант, в контексте которого выдана сессия.
	TenantID string
	// UserID — пользователь учётной записи.
	UserID string
	// LastActive — момент последней активности; по нему проверяется простой.
	LastActive time.Time
}

// Store хранит серверные сессии портала. Реализации обязаны быть
// потокобезопасными. Load возвращает ok=false, если сессии нет: отсутствие
// записи — не ошибка.
type Store interface {
	Save(ctx context.Context, rec Record) error
	Load(ctx context.Context, id string) (Record, bool, error)
	Delete(ctx context.Context, id string) error
}

// MemoryStore — стор сессий в памяти процесса; значение по умолчанию.
type MemoryStore struct {
	mu       sync.Mutex
	sessions map[string]Record
}

// NewMemoryStore создаёт пустой стор сессий в памяти.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{sessions: make(map[string]Record)}
}

// Save записывает сессию, заменяя прежнюю запись с тем же ID.
func (s *MemoryStore) Save(_ context.Context, rec Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		s.sessions = make(map[string]Record)
	}
	s.sessions[rec.ID] = rec
	return nil
}

// Load возвращает сессию по ID; ok=false, если её нет.
func (s *MemoryStore) Load(_ context.Context, id string) (Record, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.sessions[id]
	return rec, ok, nil
}

// Delete удаляет сессию. Операция идемпотентна.
func (s *MemoryStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
	return nil
}

// Проверка соответствия интерфейсу на этапе компиляции.
var _ Store = (*MemoryStore)(nil)

// Session — серверная сессия портала.
type Session struct {
	tenantID string
	userID   string
}

// TenantID возвращает тенант сессии.
func (s *Session) TenantID() string { return s.tenantID }

// UserID возвращает пользователя сессии.
func (s *Session) UserID() string { return s.userID }

// Manager — серверные сессии портала и cookie. Сессии хранит Store: по умолчанию
// это память процесса, а общий Store (PostgreSQL) делает сессию, выданную одним
// процессом, действительной в другом.
type Manager struct {
	store       Store
	idleTimeout time.Duration
	now         func() time.Time
}

// NewManager собирает менеджер сессий. nil-store означает MemoryStore, nil-now —
// системное время.
func NewManager(store Store, now func() time.Time) *Manager {
	if store == nil {
		store = NewMemoryStore()
	}
	if now == nil {
		now = time.Now
	}
	return &Manager{store: store, idleTimeout: DefaultIdleTimeout, now: now}
}

// Create создаёт сессию и возвращает её идентификатор (значение cookie).
func (m *Manager) Create(ctx context.Context, tenantID, userID string) (string, error) {
	id, err := randomToken()
	if err != nil {
		return "", err
	}
	rec := Record{ID: id, TenantID: tenantID, UserID: userID, LastActive: m.now()}
	if err := m.store.Save(ctx, rec); err != nil {
		return "", fmt.Errorf("session: сохранение сессии: %w", err)
	}
	return id, nil
}

// Lookup возвращает сессию по идентификатору, продлевая активность. Истёкшая или
// отсутствующая сессия даёт ok=false.
func (m *Manager) Lookup(ctx context.Context, id string) (*Session, bool) {
	rec, ok, err := m.store.Load(ctx, id)
	if err != nil || !ok {
		return nil, false
	}
	if m.now().Sub(rec.LastActive) > m.idleTimeout {
		_ = m.store.Delete(ctx, id)
		return nil, false
	}
	rec.LastActive = m.now()
	if err := m.store.Save(ctx, rec); err != nil {
		return nil, false
	}
	return &Session{tenantID: rec.TenantID, userID: rec.UserID}, true
}

// Delete удаляет сессию. Операция идемпотентна.
func (m *Manager) Delete(ctx context.Context, id string) {
	_ = m.store.Delete(ctx, id)
}

// Middleware требует сессию: запрос без валидной сессии перенаправляется на
// /login. Сессия кладётся в контекст запроса; достать её можно через FromContext.
func (m *Manager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(CookieName)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		sess, ok := m.Lookup(r.Context(), cookie.Value)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		ctx := context.WithValue(r.Context(), ctxSessionKey{}, sess)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ctxSessionKey — ключ контекста запроса с сессией портала.
type ctxSessionKey struct{}

// FromContext возвращает сессию портала из контекста (nil, если её нет).
func FromContext(ctx context.Context) *Session {
	sess, _ := ctx.Value(ctxSessionKey{}).(*Session)
	return sess
}

// SetCookie ставит cookie серверной сессии: HttpOnly, Secure (TLS или обратный
// прокси), SameSite=Lax, Path=/.
func (m *Manager) SetCookie(w http.ResponseWriter, r *http.Request, id string) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		Secure:   isSecure(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearCookie удаляет cookie серверной сессии (MaxAge=-1).
func (m *Manager) ClearCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   isSecure(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// randomToken генерирует случайный токен (64 hex-символа из 32 байт).
func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// isSecure сообщает, что запрос пришёл по защищённому каналу.
func isSecure(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}
