package httpapi

import (
	"net/http"
	"strings"

	"lead-gen/backend/platform/consolescope"
)

// activeTenant определяет активный тенант экранного запроса: `tenant_id` из
// строки запроса или формы, затем — выбранный оператором тенант из cookie консоли
// (`consolescope`). Пусто означает, что тенант не задан: прежнее поведение
// очередей, которые при пустом тенанте просто не показывают чужие заявки.
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
