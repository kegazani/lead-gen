// Package consoleauth — общий приём аутентификации операторской консоли:
// серверные сессии в cookie, CSRF-токен на изменяющих запросах и middleware,
// требующий сессию. Пакет относится к серверной части и не тянет доменных
// зависимостей, поэтому годится любому HTTP-адаптеру консоли.
//
// Пакет не знает о разметке: отказ по CSRF отдаётся через Policy.OnCSRFError, а
// страница входа рисуется на стороне адаптера. Пароль хранится только в виде
// хэша (salt:sha256); исходный пароль в памяти сервера не задерживается.
package consoleauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// CookieName — имя cookie серверной сессии.
const CookieName = "session"

// DefaultIdleTimeout — период неактивности, после которого сессия истекает.
const DefaultIdleTimeout = 30 * time.Minute

// ErrInvalidCredentials — неверные логин или пароль.
var ErrInvalidCredentials = errors.New("consoleauth: неверный логин или пароль")

// Config — настройки аутентификации оператора.
type Config struct {
	// Username — логин оператора.
	Username string
	// Password — пароль оператора. Хранится только в виде хэша (см. Manager).
	Password string
	// IdleTimeout — период неактивности, после которого сессия истекает. Ноль
	// означает DefaultIdleTimeout.
	IdleTimeout time.Duration
	// Now — часы для проверки истечения сессии; nil — системное время.
	Now func() time.Time
	// CookieName — имя cookie серверной сессии. Пусто означает CookieName
	// ("session") — общее имя всех консолей: cookie не различает порт, и общий
	// стор сессий делает вход в одну консоль действительным в другой. Своё имя
	// остаётся возможным, но больше не требуется для их разведения.
	CookieName string
	// Store — хранилище серверных сессий. nil означает MemoryStore: сессии
	// живут в памяти процесса. Общий Store (например, PostgreSQL) делает сессию,
	// выданную одним процессом консоли, действительной в другом.
	Store Store
}

// Session — серверная сессия оператора.
type Session struct {
	id         string
	username   string
	lastActive time.Time
	csrf       string
}

// Username возвращает логин оператора.
func (s *Session) Username() string { return s.username }

// CSRF возвращает CSRF-токен сессии.
func (s *Session) CSRF() string { return s.csrf }

// Manager — серверные сессии и CSRF-токены. Сессии хранит Store: по умолчанию
// это память процесса, а общий Store позволяет принимать сессию, выданную
// другим процессом консоли.
type Manager struct {
	store        Store
	username     string
	passwordHash string
	idleTimeout  time.Duration
	now          func() time.Time
	cookieName   string
}

// New собирает менеджер сессий, хэшируя пароль.
func New(cfg Config) *Manager {
	timeout := cfg.IdleTimeout
	if timeout <= 0 {
		timeout = DefaultIdleTimeout
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	cookieName := cfg.CookieName
	if cookieName == "" {
		cookieName = CookieName
	}
	store := cfg.Store
	if store == nil {
		store = NewMemoryStore()
	}
	return &Manager{
		store:        store,
		username:     cfg.Username,
		passwordHash: hashPassword(cfg.Password),
		idleTimeout:  timeout,
		now:          now,
		cookieName:   cookieName,
	}
}

// CookieName возвращает имя cookie серверной сессии менеджера.
func (m *Manager) CookieName() string { return m.cookieName }

// Login проверяет учётные данные и создаёт сессию, возвращая её идентификатор.
func (m *Manager) Login(username, password string) (string, error) {
	if username != m.username || !verifyPassword(password, m.passwordHash) {
		return "", ErrInvalidCredentials
	}
	id, err := randomToken()
	if err != nil {
		return "", err
	}
	csrf, err := randomToken()
	if err != nil {
		return "", err
	}
	rec := Record{ID: id, Username: username, CSRF: csrf, LastActive: m.now()}
	if err := m.store.Save(context.Background(), rec); err != nil {
		return "", fmt.Errorf("consoleauth: сохранение сессии: %w", err)
	}
	return id, nil
}

// Authenticate возвращает сессию по идентификатору, продлевая её активность.
// Истёкшая или отсутствующая сессия даёт ok=false.
func (m *Manager) Authenticate(id string) (*Session, bool) {
	rec, ok, err := m.store.Load(context.Background(), id)
	if err != nil || !ok {
		return nil, false
	}
	if m.now().Sub(rec.LastActive) > m.idleTimeout {
		_ = m.store.Delete(context.Background(), id)
		return nil, false
	}
	rec.LastActive = m.now()
	if err := m.store.Save(context.Background(), rec); err != nil {
		// Хранилище недоступно: сессию не выдаём и не продлеваем — отказ
		// безопаснее работы с непродлённой сессией.
		return nil, false
	}
	return &Session{id: rec.ID, username: rec.Username, lastActive: rec.LastActive, csrf: rec.CSRF}, true
}

// Logout удаляет сессию. Операция идемпотентна.
func (m *Manager) Logout(id string) {
	_ = m.store.Delete(context.Background(), id)
}

// Policy — правила доступа к маршрутам консоли для Middleware.
type Policy struct {
	// LoginPath — страница входа, куда редиректится запрос без валидной сессии.
	// Пусто — "/login".
	LoginPath string
	// Public — пути, доступные без сессии (точное совпадение).
	Public []string
	// PublicPrefixes — префиксы путей, доступные без сессии (например, статика).
	PublicPrefixes []string
	// CSRFExempt — пути, на которых проверка CSRF пропускается (например, выход),
	// несмотря на изменяющий метод.
	CSRFExempt []string
	// OnCSRFError рисует отказ по CSRF. nil — простой http.Error со статусом 403.
	OnCSRFError http.HandlerFunc
}

// Middleware требует сессию для всех маршрутов, кроме публичных, и проверяет
// CSRF-токен на изменяющих запросах. Сессия кладётся в контекст запроса, чтобы
// обработчики не аутентифицировали повторно; достать её можно через Current.
func (m *Manager) Middleware(policy Policy) func(http.Handler) http.Handler {
	loginPath := policy.LoginPath
	if loginPath == "" {
		loginPath = "/login"
	}
	onCSRFError := policy.OnCSRFError
	if onCSRFError == nil {
		onCSRFError = func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "недопустимый запрос: отсутствует или неверен CSRF-токен", http.StatusForbidden)
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			if policy.isPublic(path) {
				next.ServeHTTP(w, r)
				return
			}
			cookie, err := r.Cookie(m.cookieName)
			if err != nil {
				http.Redirect(w, r, loginPath, http.StatusSeeOther)
				return
			}
			sess, ok := m.Authenticate(cookie.Value)
			if !ok {
				http.Redirect(w, r, loginPath, http.StatusSeeOther)
				return
			}
			if r.Method == http.MethodPost && !policy.csrfExempt(path) {
				if r.FormValue("csrf") != sess.CSRF() {
					onCSRFError(w, r)
					return
				}
			}
			ctx := context.WithValue(r.Context(), ctxSessionKey{}, sess)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// isPublic сообщает, что путь доступен без сессии.
func (p Policy) isPublic(path string) bool {
	for _, public := range p.Public {
		if path == public {
			return true
		}
	}
	for _, prefix := range p.PublicPrefixes {
		if prefix != "" && strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// csrfExempt сообщает, что на пути проверка CSRF пропускается.
func (p Policy) csrfExempt(path string) bool {
	for _, exempt := range p.CSRFExempt {
		if path == exempt {
			return true
		}
	}
	return false
}

// ctxSessionKey — ключ контекста запроса с сессией оператора.
type ctxSessionKey struct{}

// Current возвращает сессию оператора из контекста запроса (nil, если сессии нет).
func Current(ctx context.Context) *Session {
	sess, _ := ctx.Value(ctxSessionKey{}).(*Session)
	return sess
}

// CSRF возвращает CSRF-токен сессии (пусто, если сессии нет).
func CSRF(r *http.Request) string {
	if sess := Current(r.Context()); sess != nil {
		return sess.CSRF()
	}
	return ""
}

// SetSessionCookie ставит cookie серверной сессии с именем по умолчанию: HttpOnly,
// Secure (TLS или обратный прокси), SameSite=Lax, Path=/.
func SetSessionCookie(w http.ResponseWriter, r *http.Request, id string) {
	setSessionCookie(w, r, CookieName, id)
}

// ClearSessionCookie удаляет cookie серверной сессии с именем по умолчанию
// (MaxAge=-1).
func ClearSessionCookie(w http.ResponseWriter, r *http.Request) {
	clearSessionCookie(w, r, CookieName)
}

// SetSessionCookie ставит cookie серверной сессии с именем, настроенным менеджером.
func (m *Manager) SetSessionCookie(w http.ResponseWriter, r *http.Request, id string) {
	setSessionCookie(w, r, m.cookieName, id)
}

// ClearSessionCookie удаляет cookie серверной сессии с именем, настроенным
// менеджером (MaxAge=-1).
func (m *Manager) ClearSessionCookie(w http.ResponseWriter, r *http.Request) {
	clearSessionCookie(w, r, m.cookieName)
}

// setSessionCookie ставит cookie серверной сессии с заданным именем: HttpOnly,
// Secure (TLS или обратный прокси), SameSite=Lax, Path=/.
func setSessionCookie(w http.ResponseWriter, r *http.Request, name, id string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		Secure:   isSecure(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// clearSessionCookie удаляет cookie серверной сессии с заданным именем
// (MaxAge=-1).
func clearSessionCookie(w http.ResponseWriter, r *http.Request, name string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   isSecure(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// isSecure сообщает, что запрос пришёл по защищённому каналу (прямой TLS либо
// обратный прокси).
func isSecure(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// hashPassword хэширует пароль с солью: salt:sha256(salt+password).
func hashPassword(password string) string {
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	sum := sha256.Sum256(append(salt, []byte(password)...))
	return hex.EncodeToString(salt) + ":" + hex.EncodeToString(sum[:])
}

// verifyPassword сверяет пароль с сохранённым хэшем.
func verifyPassword(password, stored string) bool {
	parts := strings.SplitN(stored, ":", 2)
	if len(parts) != 2 {
		return false
	}
	salt, err := hex.DecodeString(parts[0])
	if err != nil {
		return false
	}
	sum := sha256.Sum256(append(salt, []byte(password)...))
	expected := hex.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(expected), []byte(parts[1])) == 1
}

// randomToken генерирует случайный токен (64 hex-символа).
func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
