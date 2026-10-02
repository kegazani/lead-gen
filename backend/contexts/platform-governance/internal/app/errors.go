// Package app содержит прикладной слой контекста platform-governance: use cases
// самообслуживания клиента, работающие с доменом и опубликованным контрактом.
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
