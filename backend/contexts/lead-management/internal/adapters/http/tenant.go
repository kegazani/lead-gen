package httpapi

import (
	"net/http"
	"strings"

	"lead-gen/backend/platform/consolescope"
)

// tenantParam — имя параметра запроса/формы, несущего активный тенант. Его
// подставляет навигация консоли при переходе между разделами (`tenant_id`).
const tenantParam = "tenant_id"

// activeTenant определяет активный тенант запроса: `tenant_id` из строки запроса
// (GET-экраны) или из тела формы (POST-команды), затем — выбранный оператором
// тенант из cookie консоли (`consolescope`), а при его отсутствии — тенант
// конфигурации процесса (`-tenant`, `Config.TenantID`). Это единая точка
// определения активного тенанта для всех консольных экранов и команд: сценарий
// получает ровно один тенант, поэтому данные и события не смешиваются между
// тенантами.
//
// Пустой тенант везде даёт прежнее поведение (тенант процесса), поэтому dev-прогон
// и существующие сценарии без выбранного тенанта не меняются.
func (s *Server) activeTenant(r *http.Request) string {
	if tenant := strings.TrimSpace(r.URL.Query().Get(tenantParam)); tenant != "" {
		return tenant
	}
	if tenant := strings.TrimSpace(r.PostFormValue(tenantParam)); tenant != "" {
		return tenant
	}
	if tenant := consolescope.ActiveTenant(r); tenant != "" {
		return tenant
	}
	return strings.TrimSpace(s.tenantID)
}
