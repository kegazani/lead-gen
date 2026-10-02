// Package inmemory реализует ports.AccessDecider поверх оперативной памяти.
//
// Это локальная реализация смысла контракта access-decision владельца
// platform-governance: клиент видит дашборд счётчиков, полная CRM доступна только
// оператору и администратору. Настоящий контракт и его политика остаются за
// platform-governance; здесь дублируется лишь решение, чтобы клиентский срез не
// открывал CRM.
package inmemory

import (
	"context"

	"lead-gen/backend/contexts/analytics/internal/ports"
)

// Роли, на которые выносится решение о доступе.
const (
	roleClient   = "client"
	roleOperator = "operator"
	roleAdmin    = "admin"
)

// Decider — in-memory решение о доступе по роли и ресурсу.
type Decider struct{}

// New создаёт решение о доступе.
func New() *Decider { return &Decider{} }

// Allow реализует ports.AccessDecider.
//
//   - дашборд счётчиков — клиенту (и оператору/админу, которые его сопровождают);
//   - полная CRM — только оператору и администратору, но не клиенту.
func (Decider) Allow(_ context.Context, role, resource string) (bool, error) {
	switch resource {
	case ports.ResourceDashboard:
		switch role {
		case roleClient, roleOperator, roleAdmin:
			return true, nil
		default:
			return false, nil
		}
	case ports.ResourceCRM:
		switch role {
		case roleOperator, roleAdmin:
			return true, nil
		default:
			return false, nil
		}
	default:
		return false, nil
	}
}

// Проверка соответствия порту на этапе компиляции.
var _ ports.AccessDecider = Decider{}
