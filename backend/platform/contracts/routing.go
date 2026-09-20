package contracts

import (
	"fmt"
	"strings"
)

// RoutingKey возвращает ключ маршрутизации контракта вида `<name>.<major>`.
//
// Брокер маршрутизирует по MAJOR-версии: потребители, читающие разные MAJOR,
// подписываются на разные ключи, поэтому период параллельного чтения старой и
// новой версии обеспечен самой маршрутизацией. Пустое имя или некорректная
// версия — ошибка, а не ключ с пустым компонентом.
func RoutingKey(name, schemaVersion string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", fmt.Errorf("contracts: пустое имя контракта для ключа маршрутизации")
	}
	version, err := ParseVersion(schemaVersion)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s.%d", trimmed, version.Major), nil
}
