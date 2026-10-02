package domain

import "fmt"

// RequestStatus — статус заявки (VO request-status).
//
// Статусная модель: submitted → validating → validated → processing → fulfilled.
// Терминальные статусы — rejected, cancelled; из них переходов нет.
type RequestStatus string

const (
	// StatusSubmitted — заявка подана клиентом и ждёт валидации.
	StatusSubmitted RequestStatus = "submitted"

	// StatusValidating — заявка на проверке (уточнение оффера оператором).
	StatusValidating RequestStatus = "validating"

	// StatusValidated — заявка проверена и готова к запуску исполнения.
	StatusValidated RequestStatus = "validated"

	// StatusProcessing — исполнение заявки запущено (агенты работают).
	StatusProcessing RequestStatus = "processing"

	// StatusFulfilled — заявка исполнена (терминальный статус).
	StatusFulfilled RequestStatus = "fulfilled"

	// StatusRejected — заявка отклонена по итогам валидации (терминальный статус).
	StatusRejected RequestStatus = "rejected"

	// StatusCancelled — заявка отменена (терминальный статус).
	StatusCancelled RequestStatus = "cancelled"
)

// RequestStatuses возвращает согласованный набор статусов заявки.
func RequestStatuses() []RequestStatus {
	return []RequestStatus{
		StatusSubmitted, StatusValidating, StatusValidated,
		StatusProcessing, StatusFulfilled, StatusRejected, StatusCancelled,
	}
}

// isValid сообщает, что статус объявлен доменом.
func (s RequestStatus) isValid() bool {
	switch s {
	case StatusSubmitted, StatusValidating, StatusValidated,
		StatusProcessing, StatusFulfilled, StatusRejected, StatusCancelled:
		return true
	default:
		return false
	}
}

// validate отвергает статус вне согласованного набора.
func (s RequestStatus) validate() error {
	if !s.isValid() {
		return fmt.Errorf("%w: %q", ErrUnknownRequestStatus, s)
	}
	return nil
}

// IsTerminal сообщает, что статус терминален: из него нет переходов.
func (s RequestStatus) IsTerminal() bool {
	switch s {
	case StatusFulfilled, StatusRejected, StatusCancelled:
		return true
	default:
		return false
	}
}

// OfferEditableInPlace сообщает, что оффер редактируется на месте: до валидации
// (submitted, validating) валидатор уточняет формулировки, не создавая новую
// версию оффера. После validated действует обратное правило — правка создаёт
// новую версию (инвариант I4).
func (s RequestStatus) OfferEditableInPlace() bool {
	return s == StatusSubmitted || s == StatusValidating
}

// canTransitionTo проверяет допустимость перехода по статусной модели:
//
//	submitted  → validating | cancelled
//	validating → validated | rejected | cancelled
//	validated  → processing | cancelled
//	processing → fulfilled | cancelled
//
// Из терминальных статусов переходов нет. Запуск исполнения (`processing`)
// возможен только из `validated`.
func (s RequestStatus) canTransitionTo(to RequestStatus) bool {
	if s.IsTerminal() {
		return false
	}
	switch s {
	case StatusSubmitted:
		return to == StatusValidating || to == StatusCancelled
	case StatusValidating:
		return to == StatusValidated || to == StatusRejected || to == StatusCancelled
	case StatusValidated:
		return to == StatusProcessing || to == StatusCancelled
	case StatusProcessing:
		return to == StatusFulfilled || to == StatusCancelled
	default:
		return false
	}
}
