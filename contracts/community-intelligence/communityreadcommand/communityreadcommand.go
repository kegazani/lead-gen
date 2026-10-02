// Package communityreadcommand — опубликованный контракт community-read-command
// контекста community-intelligence: команда чтения разрешённого сообщества.
//
// Контракт выражен типами и интерфейсом, а не пакетами владельца: вызывающий
// держит только эту форму и не импортирует внутренние пакеты
// контекста-владельца. Команда не несёт авторов и состава участников: результат
// чтения — обезличенная выжимка.
//
// Зависимости — исключительно стандартная библиотека.
//
// Транспорт — sync/http-json: при co-location вызов прямой, при разделении
// сервисов — сетевой клиент; выбирает точка сборки.
package communityreadcommand

import (
	"context"
	"errors"
	"time"
)

// ContractName — имя публикуемого контракта. Совпадает с объявленным в каталоге
// доменной архитектуры.
const ContractName = "community-read-command"

// SchemaVersion — текущая версия схемы контракта.
const SchemaVersion = "1.0.0"

// ErrRejected — чтение отклонено по доменной причине владельца: сообщество не
// разрешено, период некорректен или за период нет сообщений. Потребитель
// различает этот отказ от сбоя транспорта.
var ErrRejected = errors.New("communityreadcommand: чтение отклонено владельцем")

// ErrUnavailable — владелец недоступен либо нет авторизованной сессии. Чтение не
// выполнено, и результат не выдумывается.
var ErrUnavailable = errors.New("communityreadcommand: владелец недоступен")

// ReadCommand — вход команды чтения разрешённого сообщества.
type ReadCommand struct {
	// CommunityID — читаемое сообщество.
	CommunityID string

	// TenantID — тенант-владелец.
	TenantID string

	// From, To — границы периода чтения.
	From time.Time
	To   time.Time

	// MaxMessages — верхняя граница числа сообщений; 0 — без ограничения.
	MaxMessages int

	// Actor — оператор или системный актор сбора.
	Actor string

	// CorrelationID — сквозной сценарий сбора.
	CorrelationID string
}

// ReadResult — результат чтения: идентификаторы выжимки и периода, число тем.
type ReadResult struct {
	DigestID    string
	CommunityID string
	PeriodFrom  time.Time
	PeriodTo    time.Time
	TopicCount  int
}

// Port — команда чтения сообщества владельцу.
type Port interface {
	Read(ctx context.Context, cmd ReadCommand) (ReadResult, error)
}

// InProcHandler — функция прямого вызова владельца для co-location и тестов.
type InProcHandler struct {
	// ReadFunc реализует команду чтения.
	ReadFunc func(ctx context.Context, cmd ReadCommand) (ReadResult, error)
}

// Read реализует Port. Незаданная функция честно сообщает о недоступности
// владельца, а не выдумывает результат.
func (h InProcHandler) Read(ctx context.Context, cmd ReadCommand) (ReadResult, error) {
	if h.ReadFunc == nil {
		return ReadResult{}, ErrUnavailable
	}
	return h.ReadFunc(ctx, cmd)
}

// InProc заворачивает обработчик в Port для прямого вызова в одном процессе.
func InProc(handler InProcHandler) Port { return handler }
