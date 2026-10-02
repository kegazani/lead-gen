// Package communitydigest — опубликованный контракт community-digest контекста
// community-intelligence: обезличенная выжимка разрешённого Telegram-сообщества.
//
// Контракт выражен типами и интерфейсом, а не пакетами владельца: потребитель
// (publishing) держит только эту форму и не импортирует внутренние пакеты
// контекста-владельца. Сырое содержание авторов остаётся в красной зоне и в
// контракт не попадает: выжимка несёт темы, частоты, обезличенные цитаты и
// ссылки на сообщения (design.md, Решение 3 и 6).
//
// Зависимости — исключительно стандартная библиотека.
//
// Транспорт — sync/http-json (команда чтения выжимки): при co-location вызов
// прямой, при разделении сервисов — сетевой клиент; выбирает точка сборки.
package communitydigest

import (
	"context"
	"errors"
	"time"
)

// ContractName — имя публикуемого контракта. Совпадает с объявленным в каталоге
// доменной архитектуры.
const ContractName = "community-digest"

// SchemaVersion — текущая версия схемы контракта. Версионирование — semver.
const SchemaVersion = "1.0.0"

// ErrNotFound — выжимки у владельца нет либо она принадлежит другому тенанту.
// Существование чужой выжимки этим отказом не раскрывается.
var ErrNotFound = errors.New("communitydigest: выжимка не найдена")

// ErrUnavailable — владелец выжимки недоступен: контракт не подключён или сбой
// транспорта. Потребитель не выдаёт это за успех и не выдумывает выжимку.
var ErrUnavailable = errors.New("communitydigest: владелец выжимки недоступен")

// SourceRef — ссылка на сообщение-источник пункта выжимки. Автора не несёт.
type SourceRef struct {
	CommunityID string
	MessageID   string
}

// TopicView — пункт выжимки: тема, частота упоминаний, обезличенные примеры
// формулировок и ссылки на источники.
type TopicView struct {
	Name     string
	Mentions int
	Quotes   []string
	Sources  []SourceRef
}

// DigestView — обезличенная выжимка сообщества за период.
type DigestView struct {
	DigestID       string
	CommunityID    string
	CommunityTitle string
	PeriodFrom     time.Time
	PeriodTo       time.Time
	CommunitySize  int
	CreatedAt      time.Time
	Topics         []TopicView
}

// GetQuery — запрос выжимки. TenantID обязателен: чтение выжимки другого
// тенанта завершается ErrNotFound.
type GetQuery struct {
	DigestID string
	TenantID string
}

// Port — чтение выжимки у владельца. ErrNotFound — доменный исход (выжимки нет
// или она чужая), ErrUnavailable — сбой транспорта или отсутствие контракта.
type Port interface {
	Get(ctx context.Context, q GetQuery) (DigestView, error)
}

// InProcHandler — функция прямого вызова владельца для co-location и тестов.
type InProcHandler struct {
	// GetFunc реализует чтение выжимки.
	GetFunc func(ctx context.Context, q GetQuery) (DigestView, error)
}

// Get реализует Port. Незаданная функция честно сообщает о недоступности
// владельца, а не выдумывает выжимку.
func (h InProcHandler) Get(ctx context.Context, q GetQuery) (DigestView, error) {
	if h.GetFunc == nil {
		return DigestView{}, ErrUnavailable
	}
	return h.GetFunc(ctx, q)
}

// InProc заворачивает обработчик в Port для прямого вызова в одном процессе.
func InProc(handler InProcHandler) Port { return handler }
