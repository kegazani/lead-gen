// Package app содержит прикладной слой контекста platform-governance: use cases
// управления доступом (тенанты, роли, решения, аудит) и самообслуживания клиента,
// работающие с доменом и опубликованными контрактами.
//
// Общие исходы сценариев вынесены сюда, чтобы команды и запросы сообщали о них
// единообразно, а HTTP-адаптер переводил их в коды ответа не разбирая текст
// ошибки.
package app

import (
	"errors"
	"fmt"

	"lead-gen/contracts/request-fulfillment/requestintent"
)

// ErrRejected — сценарий отвергнут по доменной причине владельца (например,
// пустой оффер).
var ErrRejected = errors.New("сценарий: заявка отклонена владельцем")

// ErrRequestNotFound — заявка клиента не найдена либо принадлежит другому
// тенанту. Существование чужой заявки не раскрывается: «нет» и «не твоя»
// неотличимы.
var ErrRequestNotFound = errors.New("сценарий: заявка не найдена")

// ErrUnavailable — владелец заявки недоступен: контракт не подключён или сбой
// транспорта.
var ErrUnavailable = errors.New("сценарий: владелец заявки недоступен")

// ErrForbidden — у актора нет права на сценарий по политике least-privilege.
// Право проверяется на прикладном слое до изменения состояния.
var ErrForbidden = errors.New("сценарий: действие запрещено")

// ErrMissingActor — не задан субъект действия. Аутентификация вне среза, но
// команда без актора не выполняется: пустой субъект — ошибка, а не отказ.
var ErrMissingActor = errors.New("сценарий: не задан субъект действия")

// ErrMissingCommandID — не задан клиентский идентификатор команды. Без него
// идемпотентность по паре (tenant_id, command_id) невозможна.
var ErrMissingCommandID = errors.New("сценарий: не задан идентификатор команды")

// ErrInvalidCredentials — неверные email или пароль клиента. Причина не
// различается: по ответу нельзя установить, существует ли учётная запись или
// заявка ещё на проверке.
var ErrInvalidCredentials = errors.New("сценарий: неверный email или пароль")

// TranslateRequestIntent переводит ошибку опубликованного контракта request-intent
// в исход сценария. Доменный отказ и отсутствие заявки различаются; всё прочее
// считается недоступностью владельца и не выдаётся за успех.
func TranslateRequestIntent(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, requestintent.ErrRejected):
		return fmt.Errorf("%w: %v", ErrRejected, err)
	case errors.Is(err, requestintent.ErrNotFound):
		return fmt.Errorf("%w: %v", ErrRequestNotFound, err)
	default:
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
}
