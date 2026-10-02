// Package publicationresult объявляет опубликованный контракт publication-result
// контекста publishing: исход публикации материала.
//
// Контракт выражен типом и DTO, а не пакетами владельца: потребитель держит
// только эту форму и не импортирует внутренние пакеты контекста-владельца.
// Запись несёт площадку, время, утвердившего оператора, результат и адрес
// материала; при отказе площадки — причину.
//
// Зависимости — исключительно стандартная библиотека. Транспорт — sync/http-json:
// при co-location вызов прямой (InProc), при разделении сервисов — сетевой клиент.
package publicationresult

import (
	"context"
	"errors"
	"time"
)

// ContractName — имя публикуемого контракта. Совпадает с объявленным в каталоге
// доменной архитектуры.
const ContractName = "publication-result"

// SchemaVersion — текущая версия схемы контракта (semver).
const SchemaVersion = "1.0.0"

// Имена статусов записи. Значения совпадают с доменным VO publication-status
// владельца.
const (
	// StatusDraft — материал подготовлен и ждёт утверждения.
	StatusDraft = "draft"
	// StatusApproved — материал утверждён и допущен к публикации.
	StatusApproved = "approved"
	// StatusQueued — публикация отложена ограничением частоты площадки.
	StatusQueued = "queued"
	// StatusPublished — площадка приняла материал.
	StatusPublished = "published"
	// StatusFailed — площадка отклонила материал.
	StatusFailed = "failed"
	// StatusRejected — оператор отклонил материал.
	StatusRejected = "rejected"
)

// Ошибки контракта.
var (
	// ErrNotFound — запись о публикации отсутствует либо принадлежит другому
	// тенанту.
	ErrNotFound = errors.New("publicationresult: публикация не найдена")

	// ErrUnavailable — владелец контракта недоступен: контракт не подключён или
	// сбой транспорта.
	ErrUnavailable = errors.New("publicationresult: владелец контракта недоступен")
)

// Record — запись об исходе публикации. Адрес заполнен при успехе, причина
// отказа — при StatusFailed.
type Record struct {
	// PublicationID — идентификатор публикации.
	PublicationID string `json:"publication_id"`
	// TenantID — тенант-владелец записи.
	TenantID string `json:"tenant_id"`
	// Platform — площадка назначения.
	Platform string `json:"platform"`
	// Account — учётная запись платформы на площадке.
	Account string `json:"account"`
	// Status — статус публикации (значение VO publication-status).
	Status string `json:"status"`
	// Address — адрес опубликованного материала (при успехе).
	Address string `json:"address,omitempty"`
	// FailureReason — причина отказа площадки (при StatusFailed).
	FailureReason string `json:"failure_reason,omitempty"`
	// ApprovedBy — оператор, утвердивший материал.
	ApprovedBy string `json:"approved_by,omitempty"`
	// Shortened — был ли материал сокращён под предел площадки.
	Shortened bool `json:"shortened,omitempty"`
	// PublishedAt — время фиксации результата попытки публикации.
	PublishedAt time.Time `json:"published_at,omitempty"`
}

// Query — запрос записей об исходах публикаций. Заданный PublicationID читает
// одну запись; иначе выбираются записи тенанта в указанном статусе.
type Query struct {
	// TenantID — тенант-владелец; обязателен.
	TenantID string `json:"tenant_id"`
	// PublicationID — идентификатор публикации (необязателен).
	PublicationID string `json:"publication_id,omitempty"`
	// Status — фильтр по статусу (необязателен).
	Status string `json:"status,omitempty"`
}

// Result — набор записей об исходах публикаций.
type Result struct {
	Records []Record `json:"records"`
}

// Port — чтение исходов публикаций у владельца. Реализация отвечает записями;
// ErrNotFound — запрошенной записи нет, ErrUnavailable — сбой транспорта или
// отсутствие контракта.
type Port interface {
	Query(ctx context.Context, q Query) (Result, error)
}

// InProcHandler — функция прямого вызова владельца для co-location и тестов.
type InProcHandler struct {
	// QueryFunc реализует чтение исходов публикаций.
	QueryFunc func(ctx context.Context, q Query) (Result, error)
}

// Query реализует Port. Незаданная функция честно сообщает о недоступности
// владельца, а не выдумывает записи.
func (h InProcHandler) Query(ctx context.Context, q Query) (Result, error) {
	if h.QueryFunc == nil {
		return Result{}, ErrUnavailable
	}
	return h.QueryFunc(ctx, q)
}

// InProc заворачивает обработчик в Port для прямого вызова в одном процессе.
func InProc(handler InProcHandler) Port { return handler }
