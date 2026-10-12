// Package leadsearch — опубликованный (published language) контракт lead-search
// контекста lead-management: поиск лидов под критерий заявки с дозабором из
// внешних источников краулингом.
//
// Владелец поиска — lead-management (architecture/domain-catalog.yaml): пул
// компаний и контактов, порт поиска/обхода (Firecrawl) и векторное хранилище
// принадлежат ему. Потребитель (request-fulfillment) формулирует критерий и
// лимит, а получает результат как ссылки-идентификаторы компаний. Сырых карточек,
// контактов и персональных данных контракт не несёт — только идентификаторы и
// признак происхождения (payload-гигиена, data-governance).
//
// Зависимости — только стандартная библиотека. Владелец реализует Port, а
// потребитель зависит от контракта, не видя реализации (design.md, Решение 2).
//
// Транспорт — sync/http-json (команда): при co-location вызов прямой (InProc),
// при разделении сервисов — сетевой клиент (httpport); выбирает точка сборки по
// топологии.
package leadsearch

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ContractName — имя публикуемого контракта. Совпадает с объявленным в каталоге
// доменной архитектуры.
const ContractName = "lead-search"

// SchemaVersion — текущая версия схемы контракта. Версионирование — semver:
// MAJOR несовместимо, MINOR добавляет опциональное поле, PATCH уточняет смысл.
const SchemaVersion = "1.1.0"

// ErrRejected — поиск отклонён по доменной причине владельца (например, пустой
// критерий или отрицательный лимит). Потребитель различает этот отказ от сбоя
// транспорта.
var ErrRejected = errors.New("leadsearch: поиск отклонён владельцем")

// ErrUnavailable — владелец поиска недоступен: контракт не подключён или сбой
// транспорта. Потребитель не выдаёт это за пустой результат.
var ErrUnavailable = errors.New("leadsearch: владелец поиска недоступен")

// Происхождение найденного лида в результате.
const (
	// OriginPool — лид взят из уже существующего пула (БД компаний).
	OriginPool = "pool"
	// OriginCrawl — лид добрался краулингом и сохранён в пул.
	OriginCrawl = "crawl"
)

// Query — критерий поиска (текст оффера/цели клиента) и лимит.
type Query struct {
	// RequestID — заявка, под которую ищутся лиды (сквозная ссылка сценария).
	RequestID string
	// TenantID — тенант-владелец пула.
	TenantID string
	// Criteria — критерий поиска: текст оффера/цели клиента. Обязателен.
	Criteria string
	// Limit — сколько лидов нужно; 0 — значение владельца по умолчанию,
	// отрицательное значение запрещено.
	Limit int
}

// LeadRef — ссылка на найденный лид: идентификатор компании и происхождение.
// Персональных данных и содержимого карточки здесь нет по определению контракта.
type LeadRef struct {
	// ID — идентификатор компании (нормализованный домен).
	ID string
	// Origin — происхождение: OriginPool или OriginCrawl.
	Origin string
}

// Result — результат поиска: сколько лидов найдено всего и из каких источников.
type Result struct {
	// Found — всего найдено лидов (FromPool + Crawled).
	Found int
	// FromPool — найдено в существующем пуле.
	FromPool int
	// Crawled — добрано краулингом и сохранено.
	Crawled int
	// Leads — ссылки на найденные компании.
	Leads []LeadRef
	// PartialReason — стабильный безопасный код причины неполноты. Пустое значение
	// означает, что известной причины неполного дозабора нет.
	PartialReason string
}

// Validate проверяет обязательные поля запроса и называет все нарушения разом.
func (q Query) Validate() error {
	var invalid []string
	if strings.TrimSpace(q.Criteria) == "" {
		invalid = append(invalid, "criteria")
	}
	if q.Limit < 0 {
		invalid = append(invalid, "limit")
	}
	if len(invalid) > 0 {
		return fmt.Errorf("leadsearch: query: некорректные поля: %s", strings.Join(invalid, ", "))
	}
	return nil
}

// Port — команда поиска владельцу. Реализация ищет лидов сначала в пуле, при
// нехватке — краулингом, и возвращает ссылки; ErrRejected — доменный отказ,
// ErrUnavailable — сбой транспорта или отсутствие контракта.
type Port interface {
	Search(ctx context.Context, query Query) (Result, error)
}

// InProcHandler — функция прямого вызова владельца для co-location и тестов.
// Сетевой адаптер при разделении сервисов даёт ту же форму.
type InProcHandler struct {
	// SearchFunc реализует поиск.
	SearchFunc func(ctx context.Context, query Query) (Result, error)
}

// Search реализует Port. Незаданная функция честно сообщает о недоступности
// владельца, а не выдумывает результат.
func (h InProcHandler) Search(ctx context.Context, query Query) (Result, error) {
	if h.SearchFunc == nil {
		return Result{}, ErrUnavailable
	}
	return h.SearchFunc(ctx, query)
}

// InProc заворачивает обработчик в Port для прямого вызова в одном процессе.
func InProc(handler InProcHandler) Port { return handler }
