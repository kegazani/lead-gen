package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"lead-gen/backend/platform/consoleauth"
)

// AuthConfig — алиас настроек аутентификации оператора из общего пакета
// consoleauth. Сохранён, чтобы существующие вызовы httpapi.AuthConfig{...}
// компилировались без правок.
type AuthConfig = consoleauth.Config

// ErrInvalidCredentials — алиас ошибки неверных учётных данных.
var ErrInvalidCredentials = consoleauth.ErrInvalidCredentials

// currentSession возвращает сессию оператора из контекста запроса (nil, если её
// нет).
func currentSession(r *http.Request) *consoleauth.Session {
	return consoleauth.Current(r.Context())
}

// consoleActor возвращает логин текущего оператора из сессии для переноса в
// ссылки навигации. Пустой актор без сессии честнее выдуманного значения.
func consoleActor(r *http.Request) string {
	if sess := currentSession(r); sess != nil {
		return sess.Username()
	}
	return ""
}

// csrfToken возвращает CSRF-токен сессии (пусто, если сессии нет).
func csrfToken(r *http.Request) string {
	return consoleauth.CSRF(r)
}

// randomToken генерирует случайный токен (64 hex-символа). Остаётся в адаптере
// для не-сессионных нужд — ключа идемпотентности формы отправки и идентификаторов
// шаблонов; токены самой сессии генерирует consoleauth.
func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
