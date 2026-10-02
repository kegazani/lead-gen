package ports

import (
	"context"
	"errors"
)

// InitiateCall — вход порта инициирования звонка-уточнения. Поля — только
// идентификаторы и код цели: телефона клиента здесь нет.
type InitiateCall struct {
	RequestID     string
	TenantID      string
	CorrelationID string
	Purpose       string
}

// CallResult — результат инициирования: идентификатор созданного звонка.
type CallResult struct {
	CallID string
}

// CallInitiator — порт инициирования звонка-уточнения через контракт владельца
// звонка (outreach-orchestration). Реализуется адаптером; контекст не зависит от
// транспорта контракта.
type CallInitiator interface {
	InitiateCall(ctx context.Context, in InitiateCall) (CallResult, error)
}

// ErrCallUnavailable — контракт звонка недоступен: провайдер не подключён или
// ответил сбоем. Заявка при этом не меняется.
var ErrCallUnavailable = errors.New("валидация: контракт звонка недоступен")

// ErrCallRejected — владелец звонка отклонил создание звонка по доменной причине.
var ErrCallRejected = errors.New("валидация: создание звонка отклонено")
