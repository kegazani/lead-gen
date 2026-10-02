package ports

import "context"

// Ресурсы, на которые выносится решение о доступе.
const (
	// ResourceDashboard — клиентский дашборд счётчиков.
	ResourceDashboard = "client-dashboard"
	// ResourceCRM — полная CRM (сделки, содержимое ответов, обогащённые данные).
	ResourceCRM = "crm"
)

// AccessDecider — порт решения о доступе по роли и ресурсу.
//
// Настоящий контракт access-decision принадлежит platform-governance
// (`contracts/platform-governance`); здесь применяется локальная реализация того
// же смысла, чтобы срез не заводил второй домен доступа и не создавал пакет
// contracts/platform-governance. Решение о допустимости принимает владелец
// политики: адаптер лишь спрашивает и переводит отказ в код ответа.
type AccessDecider interface {
	// Allow сообщает, разрешён ли роли доступ к ресурсу. Ошибка означает, что
	// решение недоступно (сбой владельца политики), а не отказ.
	Allow(ctx context.Context, role, resource string) (bool, error)
}
