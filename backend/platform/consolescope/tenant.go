// Package consolescope — общий контекст выбранного тенанта для операторских
// консолей. Оператор (администратор платформы) выбирает активный тенант один раз
// на экране выбора тенанта, а консоли разных сервисов читают этот выбор как
// фолбэк, когда в запросе нет `tenant_id`. Выбор хранится в cookie с общим именем:
// cookie не различает порт, поэтому выбор переносится между консолями одного
// хоста (разные сервисы — разные порты).
//
// Пакет переносит только выбор; решение о допустимости выбора принимает владелец
// доступа (`platform-governance`) на сервере, а не этот пакет.
package consolescope

import (
	"net/http"
	"strings"
)

// CookieName — имя cookie активного тенанта. Одно на все консоли: выбор оператора
// один на хост.
const CookieName = "active_tenant"

// SetActiveTenant сохраняет выбранный тенант в cookie консоли. Пустой тенант
// снимает выбор (MaxAge=-1): консоль возвращается к фолбэку на конфигурацию.
func SetActiveTenant(w http.ResponseWriter, r *http.Request, tenant string) {
	tenant = strings.TrimSpace(tenant)
	cookie := &http.Cookie{
		Name:     CookieName,
		Value:    tenant,
		Path:     "/",
		HttpOnly: true,
		Secure:   isSecure(r),
		SameSite: http.SameSiteLaxMode,
	}
	if tenant == "" {
		cookie.MaxAge = -1
	}
	http.SetCookie(w, cookie)
}

// ActiveTenant читает выбранный оператором тенант из cookie. Пусто — выбор не
// задан (или cookie отсутствует).
func ActiveTenant(r *http.Request) string {
	cookie, err := r.Cookie(CookieName)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(cookie.Value)
}

// isSecure сообщает, что запрос пришёл по защищённому каналу (прямой TLS либо
// обратный прокси): на dev-контуре http флаг отсутствует, иначе cookie не
// отправилась бы.
func isSecure(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}
