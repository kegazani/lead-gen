// Package csrf — технический примитив защиты изменяющих форм токеном
// (синхронизирующий «двойной» токен). Пакет не содержит доменных терминов и не
// зависит от сессий: сервер выдаёт случайный токен в cookie, кладёт тот же токен
// в скрытое поле формы и на изменяющем запросе сверяет поле с cookie. Сторонний
// сайт не может прочитать HttpOnly-cookie и не может подделать токен, поэтому
// межсайтовый запрос отвергается.
//
// Сравнение — константное по времени: значение токена не утекает через время
// ответа. Пустой токен не считается ни валидным, ни совпадающим.
package csrf

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
)

// Guard — правила защиты форм токеном.
type Guard struct {
	// CookieName — имя cookie с токеном. Пусто — "csrf".
	CookieName string
	// FieldName — имя скрытого поля формы с токеном. Пусто — "csrf".
	FieldName string
	// HeaderName — заголовок с токеном (для запросов без формы). Пусто — "X-CSRF-Token".
	HeaderName string
}

// NewGuard возвращает защиту со значениями по умолчанию.
func NewGuard() Guard { return Guard{} }

// withDefaults подставляет имена по умолчанию.
func (g Guard) withDefaults() Guard {
	if g.CookieName == "" {
		g.CookieName = "csrf"
	}
	if g.FieldName == "" {
		g.FieldName = "csrf"
	}
	if g.HeaderName == "" {
		g.HeaderName = "X-CSRF-Token"
	}
	return g
}

// EnsureToken возвращает действующий токен, при отсутствии — выдаёт и ставит
// cookie. Токен несекретен для сервера, но недоступен стороннему сайту.
func (g Guard) EnsureToken(w http.ResponseWriter, r *http.Request) string {
	if r != nil {
		if cookie, err := r.Cookie(g.withDefaults().CookieName); err == nil {
			if token := strings.TrimSpace(cookie.Value); token != "" {
				return token
			}
		}
	}
	token, err := randomToken()
	if err != nil {
		// Токен не сгенерирован: форма получит пустое значение, а Verify
		// отвергнет изменяющий запрос — отказ безопаснее приёма.
		return ""
	}
	if w != nil {
		http.SetCookie(w, &http.Cookie{
			Name:     g.withDefaults().CookieName,
			Value:    token,
			Path:     "/",
			HttpOnly: true,
			Secure:   isSecure(r),
			SameSite: http.SameSiteLaxMode,
		})
	}
	return token
}

// Verify сверяет токен запроса (скрытое поле формы, иначе заголовок) с cookie.
// Отсутствие любой из частей, расхождение или пустой токен — отказ.
func (g Guard) Verify(r *http.Request) bool {
	if r == nil {
		return false
	}
	cfg := g.withDefaults()
	provided := ""
	if r.PostForm != nil {
		provided = strings.TrimSpace(r.PostFormValue(cfg.FieldName))
	}
	if provided == "" {
		provided = strings.TrimSpace(r.Header.Get(cfg.HeaderName))
	}
	if provided == "" {
		return false
	}
	cookie, err := r.Cookie(cfg.CookieName)
	if err != nil {
		return false
	}
	expected := strings.TrimSpace(cookie.Value)
	if expected == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

// randomToken генерирует случайный токен (64 hex-символа).
func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// isSecure сообщает, что запрос пришёл по защищённому каналу (прямой TLS либо
// обратный прокси).
func isSecure(r *http.Request) bool {
	if r == nil {
		return false
	}
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}
