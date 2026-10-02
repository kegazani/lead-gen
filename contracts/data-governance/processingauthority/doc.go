// Package processingauthority объявляет опубликованный контракт
// processing-authority контекста data-governance: снимок полномочия на обработку
// данных и его проверку.
//
// Контракт выражен типом и DTO, а не пакетами владельца: потребитель
// (outreach-orchestration) держит только эту форму и не импортирует внутренние
// пакеты контекста-владельца. Зависимости — исключительно стандартная
// библиотека.
//
// Транспорт — async/amqp (интеграция policy): снимок полномочия доставляется
// подпиской и применяется потребителем локально, без обращения к data-governance
// на каждое решение. При co-location тот же Port реализуется прямым вызовом
// (InProc).
package processingauthority

import (
	"context"
	"errors"
	"time"
)

// ContractName — имя публикуемого контракта. Совпадает с объявленным в каталоге
// доменной архитектуры.
const ContractName = "processing-authority"

// SchemaVersion — текущая версия схемы контракта.
const SchemaVersion = "1.0.0"

// ErrNotFound — у тенанта нет полномочия для указанной цели и категории.
// Существование чужого полномочия этим отказом не раскрывается.
var ErrNotFound = errors.New("processingauthority: полномочие не найдено")

// ErrUnavailable — владелец полномочия недоступен: контракт не подключён или сбой
// транспорта. Потребитель не выдаёт это за разрешение.
var ErrUnavailable = errors.New("processingauthority: владелец полномочия недоступен")

// AuthorityQuery — запрос действующего полномочия для цели и категории тенанта.
type AuthorityQuery struct {
	TenantID string
	Purpose  string
	Category string
	// At — момент проверки; нулевое время — момент вызова у владельца.
	At time.Time
}

// Authority — снимок полномочия на обработку. Потребитель применяет его локально.
type Authority struct {
	BasisID       string
	TenantID      string
	Purpose       string
	Category      string
	Authorized    bool
	RetentionDays int
	ValidFrom     time.Time
	ValidUntil    time.Time
	Status        string
}

// Port — проверка полномочия у владельца.
type Port interface {
	Check(ctx context.Context, q AuthorityQuery) (Authority, error)
}

// InProcHandler — функции прямого вызова владельца для co-location и тестов.
type InProcHandler struct {
	// CheckFunc реализует проверку полномочия. Незаданная функция честно
	// сообщает о недоступности владельца.
	CheckFunc func(ctx context.Context, q AuthorityQuery) (Authority, error)
}

// Check реализует Port.
func (h InProcHandler) Check(ctx context.Context, q AuthorityQuery) (Authority, error) {
	if h.CheckFunc == nil {
		return Authority{}, ErrUnavailable
	}
	return h.CheckFunc(ctx, q)
}

// InProc заворачивает обработчик в Port для прямого вызова в одном процессе.
func InProc(handler InProcHandler) Port { return handler }
