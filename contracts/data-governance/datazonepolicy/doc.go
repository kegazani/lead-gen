// Package datazonepolicy объявляет опубликованный контракт data-zone-policy
// контекста data-governance: правила классификации зон данных для тенанта.
//
// Контракт выражен типом и DTO, а не пакетами владельца: потребитель
// (lead-management, audience) держит только эту форму и не импортирует
// внутренние пакеты контекста-владельца. Зависимости — исключительно стандартная
// библиотека.
//
// Транспорт — async/amqp (интеграция policy): правило доставляется подпиской как
// снимок и применяется потребителем локально. При co-location тот же Port
// реализуется прямым вызовом (InProc).
package datazonepolicy

import (
	"context"
	"errors"
	"time"
)

// ContractName — имя публикуемого контракта. Совпадает с объявленным в каталоге
// доменной архитектуры.
const ContractName = "data-zone-policy"

// SchemaVersion — текущая версия схемы контракта. Версионирование — semver:
// MAJOR несовместимо, MINOR добавляет опциональное поле, PATCH уточняет смысл.
const SchemaVersion = "1.0.0"

// ErrUnavailable — владелец решения недоступен: контракт не подключён или сбой
// транспорта. Потребитель не выдаёт это за положительное решение.
var ErrUnavailable = errors.New("datazonepolicy: владелец решения недоступен")

// ZoneQuery — запрос правила обращения с данными для цели и категории тенанта.
type ZoneQuery struct {
	TenantID string
	Purpose  string
	Category string
	// At — момент проверки; нулевое время — момент вызова у владельца.
	At time.Time
}

// ZoneDecision — решение политики зон. Authorized сообщает, действует ли у
// тенанта полномочие для указанной цели и категории.
type ZoneDecision struct {
	Authorized           bool
	Category             string
	Handling             string
	SharedStorageAllowed bool
	Minimize             bool
	Reason               string
}

// Port — запрос решения политики зон у владельца.
type Port interface {
	Decide(ctx context.Context, q ZoneQuery) (ZoneDecision, error)
}

// InProcHandler — функции прямого вызова владельца для co-location и тестов.
// Сетевой адаптер при разделении сервисов даёт ту же форму.
type InProcHandler struct {
	// DecideFunc реализует запрос решения. Незаданная функция честно сообщает о
	// недоступности владельца.
	DecideFunc func(ctx context.Context, q ZoneQuery) (ZoneDecision, error)
}

// Decide реализует Port.
func (h InProcHandler) Decide(ctx context.Context, q ZoneQuery) (ZoneDecision, error) {
	if h.DecideFunc == nil {
		return ZoneDecision{}, ErrUnavailable
	}
	return h.DecideFunc(ctx, q)
}

// InProc заворачивает обработчик в Port для прямого вызова в одном процессе.
func InProc(handler InProcHandler) Port { return handler }
