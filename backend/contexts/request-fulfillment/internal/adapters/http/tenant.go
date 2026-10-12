package httpapi

import (
	"net/http"
	"strings"

	"lead-gen/backend/platform/consolescope"
	"lead-gen/frontend/components"
)

// activeTenant определяет активный тенант экранного запроса: `tenant_id` из
// строки запроса или формы, затем — выбранный оператором тенант из cookie консоли
// (`consolescope`). Пусто означает, что тенант не задан: прежнее поведение
// очередей без подключённой платформенной консоли. В общем контуре очереди
// направляют к выбору компании через requireSelectedTenant.
//
// Формы-команды по-прежнему несут `tenant_id` скрытым полем: этот помощник
// обслуживает экраны (GET), где важно перенести выбор оператора без параметра.
func activeTenant(r *http.Request) string {
	if tenant := strings.TrimSpace(r.URL.Query().Get("tenant_id")); tenant != "" {
		return tenant
	}
	if tenant := strings.TrimSpace(r.FormValue("tenant_id")); tenant != "" {
		return tenant
	}
	return consolescope.ActiveTenant(r)
}

// requireSelectedTenant направляет оператора к выбору компании, когда очередь
// иначе выглядела бы пустой. Без платформенной базы сохраняется dev-сборка.
func (s *Server) requireSelectedTenant(w http.ResponseWriter, r *http.Request) (string, bool) {
	if tenant := activeTenant(r); tenant != "" {
		return tenant, true
	}
	base := strings.TrimRight(s.console[components.ConsoleServicePlatform], "/")
	if base == "" {
		// Отдельный dev-сервис без владельца выбора сохраняет прежний экран.
		return "", true
	}
	http.Redirect(w, r, components.ConsoleHrefWithContext(base+"/console/tenant", components.ConsoleContext{Actor: actorOf(r)}), http.StatusSeeOther)
	return "", false
}
