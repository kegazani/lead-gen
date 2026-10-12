package ports

import "context"

// Ресурсы, на которые выносится решение о доступе.
const (
	// ResourceDashboard — клиентский дашборд счётчиков.
	ResourceDashboard = "client-dashboard"
	// ResourceCRM — полная CRM (сделки, содержимое ответов, обогащённые данные).
	ResourceCRM = "crm"
	// ResourceJobs — задания длительных операций (просмотр DLQ и повтор
	// исчерпавших попытки задач). Только администратор.
	ResourceJobs = "jobs-admin"

	// ResourceInsights — анализ качества поиска (lead-quality-insights):
	// распределение вердиктов и причин, рекомендации оператору. Клиенту не
	// показывается: это операционный разбор причин отбора.
	ResourceInsights = "lead-quality-insights"
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
