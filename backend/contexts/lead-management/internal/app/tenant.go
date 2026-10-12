package app

import "strings"

// tenantOrDefault возвращает активный тенант запроса, а при пустом — тенант
// конфигурации сценария. Единый фолбэк для консольных команд: команда работает в
// тенанте, выбранном в консоли, а без выбора — в тенанте процесса (прежнее
// поведение).
func tenantOrDefault(active, fallback string) string {
	if tenant := strings.TrimSpace(active); tenant != "" {
		return tenant
	}
	return strings.TrimSpace(fallback)
}
