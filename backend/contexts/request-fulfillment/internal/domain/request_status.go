package domain

import "fmt"

// RequestStatus — статус заявки (VO request-status).
//
// Статусная модель: submitted → validating → validated → processing → fulfilled.
// Терминальные статусы — fulfilled, rejected, cancelled, failed; из них переходов
// нет. failed означает необратимо сломанное исполнение и терминален для
// автоматических переходов; явный повторный запуск (re-run) идёт отдельной
// командой вне обычной таблицы переходов (Request.Rerun).
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

	// StatusNeedsRevision — заявка возвращена клиенту на доработку оффера
	// (нетерминальный статус).
	StatusNeedsRevision RequestStatus = "needs-revision"

	// StatusFulfilled — заявка исполнена (терминальный статус).
	StatusFulfilled RequestStatus = "fulfilled"

	// StatusRejected — заявка отклонена по итогам валидации (терминальный статус).
	StatusRejected RequestStatus = "rejected"

	// StatusCancelled — заявка отменена (терминальный статус).
	StatusCancelled RequestStatus = "cancelled"

	// StatusFailed — исполнение заявки необратимо сломалось (терминальный статус
	// с причиной). Отличим от needs-revision: там действует клиент (доработка
	// оффера), здесь — оператор (повторный запуск либо разбор сбоя).
	StatusFailed RequestStatus = "failed"
)

// RequestStatuses возвращает согласованный набор статусов заявки.
func RequestStatuses() []RequestStatus {
	return []RequestStatus{
		StatusSubmitted, StatusValidating, StatusValidated,
		StatusProcessing, StatusNeedsRevision, StatusFulfilled, StatusRejected, StatusCancelled,
		StatusFailed,
	}
}

// isValid сообщает, что статус объявлен доменом.
func (s RequestStatus) isValid() bool {
	switch s {
	case StatusSubmitted, StatusValidating, StatusValidated,
		StatusProcessing, StatusNeedsRevision, StatusFulfilled, StatusRejected, StatusCancelled,
		StatusFailed:
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

// IsTerminal сообщает, что статус терминален: из него нет переходов. failed
// терминален для автоматического исполнения; единственный явный выход — отдельная
// операция повторного запуска (Request.Rerun), которая не проходит через
// canTransitionTo.
func (s RequestStatus) IsTerminal() bool {
	switch s {
	case StatusFulfilled, StatusRejected, StatusCancelled, StatusFailed:
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
	return s == StatusSubmitted || s == StatusValidating || s == StatusNeedsRevision
}

// canTransitionTo проверяет допустимость перехода по статусной модели:
//
//	submitted      → validating | cancelled
//	validating     → validated | rejected | needs-revision | cancelled
//	validated      → processing | cancelled
//	processing     → fulfilled | needs-revision | cancelled | failed
//	needs-revision → submitted | cancelled
//
// Из терминальных статусов переходов нет. Запуск исполнения (`processing`)
// возможен только из `validated`. Повторный запуск упавшей заявки
// (`failed → validated`) — отдельная явная операция Request.Rerun, а не ветка
// этой таблицы: `failed` остаётся терминальным для автоматического исполнения.
func (s RequestStatus) canTransitionTo(to RequestStatus) bool {
	if s.IsTerminal() {
		return false
	}
	switch s {
	case StatusSubmitted:
		return to == StatusValidating || to == StatusCancelled
	case StatusValidating:
		return to == StatusValidated || to == StatusRejected || to == StatusNeedsRevision || to == StatusCancelled
	case StatusValidated:
		return to == StatusProcessing || to == StatusCancelled
	case StatusProcessing:
		return to == StatusFulfilled || to == StatusNeedsRevision || to == StatusCancelled || to == StatusFailed
	case StatusNeedsRevision:
		return to == StatusSubmitted || to == StatusCancelled
	default:
		return false
	}
}
