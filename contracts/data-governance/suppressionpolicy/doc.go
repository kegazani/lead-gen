// Package suppressionpolicy объявляет опубликованный контракт suppression-policy
// контекста data-governance: снимок правила подавления контактов и его проверку.
//
// Контракт выражен типом и DTO, а не пакетами владельца: потребитель
// (message-delivery) держит только эту форму и не импортирует внутренние пакеты
// контекста-владельца. Зависимости — исключительно стандартная библиотека.
//
// Транспорт — async/amqp (интеграция policy): правило доставляется подпиской;
// delivery дополнительно выполняет preflight-проверку непосредственно перед
// необратимым действием (docs/domain-model.md, правило 8). При co-location тот же
// Port реализуется прямым вызовом (InProc).
package suppressionpolicy

import (
	"context"
	"errors"
	"time"
)

// ContractName — имя публикуемого контракта. Совпадает с объявленным в каталоге
// доменной архитектуры.
const ContractName = "suppression-policy"

// SchemaVersion — текущая версия схемы контракта.
const SchemaVersion = "1.0.0"

// ErrUnavailable — владелец правила недоступен: контракт не подключён или сбой
// транспорта. Consumer не считает подавление отсутствующим при недоступности
// владельца.
var ErrUnavailable = errors.New("suppressionpolicy: владелец правила недоступен")

// SuppressionQuery — запрос состояния стоп-листа для контакта в пределах тенанта.
type SuppressionQuery struct {
	TenantID string
	Contact  string
	// At — момент проверки; нулевое время — момент вызова у владельца.
	At time.Time
}

// SuppressionDecision — решение о подавлении контакта.
type SuppressionDecision struct {
	Suppressed    bool
	Reason        string
	SuppressionID string
	BasisID       string
	Since         time.Time
}

// Port — проверка подавления контакта у владельца.
type Port interface {
	Check(ctx context.Context, q SuppressionQuery) (SuppressionDecision, error)
}

// InProcHandler — функции прямого вызова владельца для co-location и тестов.
type InProcHandler struct {
	// CheckFunc реализует проверку подавления. Незаданная функция честно сообщает
	// о недоступности владельца.
	CheckFunc func(ctx context.Context, q SuppressionQuery) (SuppressionDecision, error)
}

// Check реализует Port.
func (h InProcHandler) Check(ctx context.Context, q SuppressionQuery) (SuppressionDecision, error) {
	if h.CheckFunc == nil {
		return SuppressionDecision{}, ErrUnavailable
	}
	return h.CheckFunc(ctx, q)
}

// InProc заворачивает обработчик в Port для прямого вызова в одном процессе.
func InProc(handler InProcHandler) Port { return handler }
