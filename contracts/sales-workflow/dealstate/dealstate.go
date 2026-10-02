// Package dealstate объявляет опубликованный (published language) контракт
// deal-state контекста sales-workflow: состояние сделки в её жизненном цикле.
//
// Владелец сделки — sales-workflow (architecture/domain-catalog.yaml,
// aggregates.deal.owner_context). Контракт используется двумя способами:
//
//   - как событие (async/amqp, sales-to-analytics и deals-for-requests):
//     DealStateEvent оборачивает состояние сделки в конверт;
//   - как синхронный запрос (customer-supplier, query/http-json,
//     deals-for-requests): Port.Get возвращает DealState потребителю —
//     например, request-fulfillment.
//
// Зависимости — стандартная библиотека и общий пакет envelope. Контракт не
// импортирует внутренние пакеты владельца: владелец реализует интерфейс, а
// потребитель зависит от контракта, не видя реализации (design.md, Решение 2).
package dealstate

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"lead-gen/contracts/sales-workflow/envelope"
)

// ContractName — имя публикуемого контракта. Совпадает с объявленным в каталоге
// доменной архитектуры.
const ContractName = "deal-state"

// SchemaVersion — текущая версия схемы. Изменяется по правилам semver из
// пакета envelope.
const SchemaVersion = "1.0.0"

// Имена этапов сделки. Значения совпадают с доменным VO deal-stage владельца
// (internal/domain/deal_stage.go): контракт не принимает этап, которого нет в
// домене. Набор закрыт — неизвестный этап отвергается.
const (
	StageLead        = "lead"
	StageQualified   = "qualified"
	StageProposal    = "proposal"
	StageNegotiation = "negotiation"
	StageWon         = "won"
	StageLost        = "lost"
)

var allowedStages = map[string]struct{}{
	StageLead: {}, StageQualified: {}, StageProposal: {},
	StageNegotiation: {}, StageWon: {}, StageLost: {},
}

// IsKnownStage сообщает, что имя этапа входит в закрытый набор контракта.
func IsKnownStage(stage string) bool {
	_, ok := allowedStages[strings.ToLower(strings.TrimSpace(stage))]
	return ok
}

// ErrNotFound — сделки у владельца нет либо она принадлежит другому тенанту.
// Существование чужой сделки этим отказом не раскрывается.
var ErrNotFound = errors.New("dealstate: сделка не найдена")

// ErrUnavailable — владелец сделки недоступен: контракт не подключён или сбой
// транспорта. Потребитель не выдаёт это за успех и не выдумывает состояние.
var ErrUnavailable = errors.New("dealstate: владелец сделки недоступен")

// DealState — состояние сделки. Минимальные идентификаторы и ссылки; персональных
// данных здесь нет.
type DealState struct {
	// DealID — идентификатор сделки.
	DealID string `json:"deal_id"`
	// CompanyRef — непрозрачная ссылка на карточку компании.
	CompanyRef string `json:"company_ref"`
	// Stage — этап жизненного цикла (значение VO deal-stage).
	Stage string `json:"stage"`
	// ExternalRef — ссылка на запись сделки во внешней CRM.
	ExternalRef string `json:"external_ref"`
	// ExternalRevision — ревизия записи во внешней CRM.
	ExternalRevision int64 `json:"external_revision"`
	// ResultConfirmed — подтверждён ли результат сделки.
	ResultConfirmed bool `json:"result_confirmed"`
	// Version — версия агрегата сделки.
	Version int `json:"version"`
}

// validate проверяет обязательные поля payload.
func (d DealState) validate() error {
	var invalid []string
	if strings.TrimSpace(d.DealID) == "" {
		invalid = append(invalid, "deal_id")
	}
	if !IsKnownStage(d.Stage) {
		invalid = append(invalid, "stage")
	}
	if len(invalid) > 0 {
		return fmt.Errorf("dealstate: deal-state: некорректные или отсутствующие обязательные поля: %s", strings.Join(invalid, ", "))
	}
	return nil
}

// DealStateEvent — опубликованное событие состояния сделки: состояние, обёрнутое
// в конверт. Используется транспортом async/amqp.
type DealStateEvent struct {
	envelope.Envelope
	DealState
}

// NewDealStateEvent возвращает событие с проставленными именем и версией схемы.
// Остальные поля конверта и состояния заполняет вызывающий.
func NewDealStateEvent(env envelope.Envelope) DealStateEvent {
	env.Name = ContractName
	env.SchemaVersion = SchemaVersion
	return DealStateEvent{Envelope: env}
}

// Validate проверяет конверт и обязательные поля состояния.
func (e DealStateEvent) Validate() error {
	if err := e.Envelope.Validate(); err != nil {
		return err
	}
	if err := e.DealState.validate(); err != nil {
		return err
	}
	if e.Name != ContractName {
		return fmt.Errorf("dealstate: deal-state: неверное имя контракта %q", e.Name)
	}
	return nil
}

// Query — запрос состояния сделки. TenantID обязателен: чтение сделки другого
// тенанта завершается ErrNotFound.
type Query struct {
	DealID   string
	TenantID string
}

// Port — синхронный запрос состояния сделки владельцу. Реализация отвечает
// состоянием; ErrNotFound — сделки нет или она чужая, ErrUnavailable — сбой
// транспорта или отсутствие контракта.
type Port interface {
	Get(ctx context.Context, q Query) (DealState, error)
}

// InProcHandler — функция прямого вызова владельца для co-location и тестов.
type InProcHandler struct {
	// GetFunc реализует запрос состояния сделки.
	GetFunc func(ctx context.Context, q Query) (DealState, error)
}

// Get реализует Port. Незаданная функция честно сообщает о недоступности
// владельца, а не выдумывает состояние.
func (h InProcHandler) Get(ctx context.Context, q Query) (DealState, error) {
	if h.GetFunc == nil {
		return DealState{}, ErrUnavailable
	}
	return h.GetFunc(ctx, q)
}

// InProc заворачивает обработчик в Port для прямого вызова в одном процессе.
func InProc(handler InProcHandler) Port { return handler }
