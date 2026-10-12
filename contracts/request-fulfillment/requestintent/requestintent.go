// Package requestintent — опубликованный (published language) контракт
// request-intent контекста request-fulfillment: команда подать заявку клиента и
// запрос её состояния.
//
// Владелец агрегата `request` — request-fulfillment
// (architecture/domain-catalog.yaml, aggregates.request.owner_context), поэтому
// заявку создаёт владелец, а портал лишь формулирует намерение. Контракт не
// несёт сырых лидов, карточек компаний и контактов: только описание оффера
// клиента и идентификаторы — payload-гигиена (platform/workflow,
// data-governance).
//
// Зависимости — исключительно стандартная библиотека. Контракт не импортирует
// ни внутренние пакеты владельца (request-fulfillment), ни platform, поэтому
// владелец реализует интерфейс, а потребитель зависит от контракта, не видя
// реализации (design.md, Решение 2).
//
// Транспорт — sync/http-json (команда и запрос): при co-location вызов прямой,
// при разделении сервисов — сетевой клиент; выбирает точка сборки по топологии.
package requestintent

import (
	"context"
	"errors"
)

// ContractName — имя публикуемого контракта. Совпадает с объявленным в каталоге
// доменной архитектуры.
const ContractName = "request-intent"

// SchemaVersion — текущая версия схемы контракта. Обязательное имя в SubmitIntent
// делает эту версию несовместимой с клиентами 1.x.
const SchemaVersion = "2.0.0"

// ErrRejected — заявка не создана по доменной причине владельца (например,
// пустой оффер). Потребитель различает этот отказ от сбоя транспорта.
var ErrRejected = errors.New("requestintent: заявка отклонена владельцем")

// ErrNotFound — заявки у владельца нет либо она принадлежит другому тенанту.
// Существование чужой заявки этим отказом не раскрывается: «нет» и «не твоя»
// неотличимы.
var ErrNotFound = errors.New("requestintent: заявка не найдена")

// ErrUnavailable — владелец заявки недоступен: контракт не подключён или сбой
// транспорта. Потребитель не выдаёт это за успех и не выдумывает состояние.
var ErrUnavailable = errors.New("requestintent: владелец заявки недоступен")

// SubmitIntent — вход команды подачи заявки. Идентификаторы заявки и версии
// оффера назначает потребитель, чтобы повтор команды был идемпотентен по
// идентификатору.
type SubmitIntent struct {
	// RequestID — идентификатор создаваемой заявки.
	RequestID string
	// OfferID — идентификатор версии оффера.
	OfferID string
	// TenantID — тенант-владелец заявки.
	TenantID string
	// Title — обязательное короткое имя заявки (1–100 Unicode-символов).
	Title string `json:"title"`
	// Content — что клиент предлагает (непустой).
	Content string
	// Target — кому адресован оффер; может быть пустым.
	Target string
	// ExecutionPurpose различает editorial-подготовку и legacy delivery.
	// Пустое значение совместимо со старыми клиентами и означает delivery.
	ExecutionPurpose string `json:"execution_purpose,omitempty"`
	// SenderCompany содержит только публичные данные компании для editorial
	// generation; портал формирует его из active Tenant/Profile.
	SenderCompany *SenderCompanySnapshot `json:"sender_company,omitempty"`
	// Actor — клиент, подавший заявку.
	Actor string
	// CorrelationID — сквозной сценарий подачи; пусто — владелец берёт
	// идентификатор заявки.
	CorrelationID string
}

// SenderCompanySnapshot — безопасный межконтекстный снимок отправителя.
// Он не содержит учётных данных, контактов или юридических идентификаторов.
type SenderCompanySnapshot struct {
	CompanyName string                  `json:"company_name"`
	Website     string                  `json:"website"`
	Description string                  `json:"description"`
	Services    []SenderServiceSnapshot `json:"services"`
}

// SenderServiceSnapshot — публичное описание услуги отправителя.
type SenderServiceSnapshot struct {
	Name  string `json:"name"`
	Short string `json:"short"`
	Full  string `json:"full"`
}

// SubmitResult — результат команды: идентификатор созданной заявки и её статус.
type SubmitResult struct {
	RequestID string
	Status    string
}

// StatusQuery — запрос состояния заявки. TenantID обязателен: чтение заявки
// другого тенанта завершается ErrNotFound.
type StatusQuery struct {
	RequestID string
	TenantID  string
}

// StatusResult — состояние заявки для клиента. Поля оффера — собственные данные
// клиента; сырых лидов и контактов здесь нет по определению контракта.
//
// Phase, ErrorReason и ErrorMessage — аддитивные опциональные поля (MINOR 1.1.0):
// старый потребитель их игнорирует. Phase — фаза исполнения, ErrorReason —
// нормализованный машинный код причины сбоя, ErrorMessage — текст сообщения
// оператору. Пусты для заявок без сбоя исполнения.
type StatusResult struct {
	RequestID     string
	Status        string
	Title         string
	OfferContent  string
	OfferTarget   string
	OfferRevision int
	Phase         string
	ErrorReason   string
	ErrorMessage  string
}

// ListQuery — запрос списка заявок клиента. TenantID обязателен: чтение заявок
// другого тенанта не выполняется.
type ListQuery struct {
	TenantID string
}

// ListEntry — строка списка заявок клиента: идентификатор и текущий статус.
type ListEntry struct {
	RequestID string
	Title     string
	Status    string
}

// Port — команда и запросы владельцу заявки. Реализация создаёт заявку и
// отвечает её состоянием; ErrRejected и ErrNotFound — доменные исходы,
// ErrUnavailable — сбой транспорта или отсутствие контракта.
type Port interface {
	Submit(ctx context.Context, in SubmitIntent) (SubmitResult, error)
	Status(ctx context.Context, q StatusQuery) (StatusResult, error)
	List(ctx context.Context, q ListQuery) ([]ListEntry, error)
}

// InProcHandler — функции прямого вызова владельца для co-location и тестов.
// Сетевой адаптер при разделении сервисов даёт ту же форму.
type InProcHandler struct {
	// SubmitFunc реализует команду подачи заявки.
	SubmitFunc func(ctx context.Context, in SubmitIntent) (SubmitResult, error)
	// StatusFunc реализует запрос состояния заявки.
	StatusFunc func(ctx context.Context, q StatusQuery) (StatusResult, error)
	// ListFunc реализует запрос списка заявок клиента.
	ListFunc func(ctx context.Context, q ListQuery) ([]ListEntry, error)
}

// Submit реализует Port. Незаданная функция честно сообщает о недоступности
// владельца, а не выдумывает результат.
func (h InProcHandler) Submit(ctx context.Context, in SubmitIntent) (SubmitResult, error) {
	if h.SubmitFunc == nil {
		return SubmitResult{}, ErrUnavailable
	}
	return h.SubmitFunc(ctx, in)
}

// Status реализует Port. Незаданная функция честно сообщает о недоступности
// владельца, а не выдумывает состояние.
func (h InProcHandler) Status(ctx context.Context, q StatusQuery) (StatusResult, error) {
	if h.StatusFunc == nil {
		return StatusResult{}, ErrUnavailable
	}
	return h.StatusFunc(ctx, q)
}

// List реализует Port. Незаданная функция честно сообщает о недоступности
// владельца, а не выдумывает список.
func (h InProcHandler) List(ctx context.Context, q ListQuery) ([]ListEntry, error) {
	if h.ListFunc == nil {
		return nil, ErrUnavailable
	}
	return h.ListFunc(ctx, q)
}

// InProc заворачивает обработчики в Port для прямого вызова в одном процессе.
func InProc(handler InProcHandler) Port { return handler }
