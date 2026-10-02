// Package callintent — опубликованный (published language) контракт call-intent
// контекста outreach-orchestration: команда инициировать звонок-уточнение клиенту
// по заявке.
//
// Владелец агрегата `call` — outreach-orchestration (architecture/domain-catalog.yaml,
// entity.call.owner_context), поэтому команда приходит туда, а заявка лишь хранит
// ссылку на созданный звонок по идентификатору. Телефон клиента звонку не
// передаётся: контракт несёт только идентификаторы и код цели, а контакт владелец
// находит сам — payload-гигиена (`platform/workflow`, data-governance).
//
// Транспорт — sync/http-json (команда): при co-location вызов прямой, при
// разделении сервисов — сетевой клиент; выбирает точка сборки по топологии.
package callintent

import (
	"context"
	"errors"
)

// ContractName — имя публикуемого контракта.
const ContractName = "call-intent"

// SchemaVersion — текущая версия схемы контракта (semver).
const SchemaVersion = "1.0.0"

// ErrRejected — звонок не создан по доменной причине владельца (например,
// недоступный канал). Потребитель различает этот отказ от сбоя транспорта.
var ErrRejected = errors.New("callintent: звонок отклонён владельцем")

// Purpose — короткий контролируемый код цели звонка, а не свободный текст.
type Purpose string

// PurposeOfferClarification — уточнение оффера заявки в ходе валидации.
const PurposeOfferClarification Purpose = "offer-clarification"

// Intent — вход команды. Только идентификаторы и код цели: персональных данных
// лида и контактов здесь нет.
type Intent struct {
	// RequestID — заявка, по которой звонят.
	RequestID string
	// TenantID — тенант-владелец заявки.
	TenantID string
	// CorrelationID — сквозной сценарий (валидация заявки).
	CorrelationID string
	// Purpose — код цели звонка.
	Purpose Purpose
}

// Result — результат команды: идентификатор созданного звонка во владении
// outreach-orchestration.
type Result struct {
	CallID string
}

// Port — команда владельцу звонка. Реализация создаёт звонок и возвращает его
// идентификатор; ErrRejected — доменный отказ, прочие ошибки — сбой.
type Port interface {
	Initiate(ctx context.Context, intent Intent) (Result, error)
}

// inProc — реализация прямого вызова для co-location и тестов.
type inProc struct {
	handler func(ctx context.Context, intent Intent) (Result, error)
}

// Initiate реализует Port.
func (p inProc) Initiate(ctx context.Context, intent Intent) (Result, error) {
	return p.handler(ctx, intent)
}

// InProc заворачивает функцию-обработчик в Port для прямого вызова в одном
// процессе. Сетевой адаптер при разделении сервисов даёт ту же форму.
func InProc(handler func(ctx context.Context, intent Intent) (Result, error)) Port {
	return inProc{handler: handler}
}
