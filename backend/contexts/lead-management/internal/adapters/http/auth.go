package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"
)

// ErrInvalidCredentials — неверные логин или пароль.
var ErrInvalidCredentials = errors.New("httpapi: неверный логин или пароль")

// cookieName — имя cookie серверной сессии.
const cookieName = "session"

// defaultIdleTimeout — период неактивности, после которого сессия истекает.
const defaultIdleTimeout = 30 * time.Minute

// AuthConfig — настройки аутентификации оператора.
type AuthConfig struct {
	// Username — логин оператора.
	Username string
	// Password — пароль оператора. Хранится только в виде хэша (см. sessionManager).
	Password string
	// IdleTimeout — период неактивности, после которого сессия истекает. Ноль
	// означает defaultIdleTimeout.
	IdleTimeout time.Duration
	// Now — часы для проверки истечения сессии; nil — системное время.
	Now func() time.Time
}

// session — серверная сессия оператора.
type session struct {
	id         string
	username   string
	lastActive time.Time
	csrf       string
}

// sessionManager — серверные сессии и CSRF-токены. Пароль хранится только в виде
// хэша (salt:sha256); исходный пароль в памяти сервера не задерживается.
type sessionManager struct {
	mu           sync.Mutex
	sessions     map[string]*session
	username     string
	passwordHash string
	idleTimeout  time.Duration
	now          func() time.Time
}

// newSessionManager собирает менеджер сессий, хэшируя пароль.
func newSessionManager(cfg AuthConfig) *sessionManager {
	timeout := cfg.IdleTimeout
	if timeout <= 0 {
		timeout = defaultIdleTimeout
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &sessionManager{
		sessions:     make(map[string]*session),
		username:     cfg.Username,
		passwordHash: hashPassword(cfg.Password),
		idleTimeout:  timeout,
		now:          now,
	}
}

// Login проверяет учётные данные и создаёт сессию, возвращая её идентификатор.
func (m *sessionManager) Login(username, password string) (string, error) {
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
	m.mu.Lock()
	m.sessions[id] = &session{
		id:         id,
		username:   username,
		lastActive: m.now(),
		csrf:       csrf,
	}
	m.mu.Unlock()
	return id, nil
}

// authenticate возвращает сессию по идентификатору, продлевая её активность.
// Истёкшая или отсутствующая сессия даёт ok=false.
func (m *sessionManager) authenticate(id string) (*session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil, false
	}
	if m.now().Sub(s.lastActive) > m.idleTimeout {
		delete(m.sessions, id)
		return nil, false
	}
	s.lastActive = m.now()
	return s, true
}

// Logout удаляет сессию. Операция идемпотентна.
func (m *sessionManager) Logout(id string) {
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
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
