package domain_test

import (
	"errors"
	"testing"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
)

// TestRepeatStartProcessingIsNoOp — сценарий «Повторный переход в тот же целевой
// статус»: повторный StartProcessing на заявке в processing завершается успехом
// без изменения статуса и версии и без нового события.
func TestRepeatStartProcessingIsNoOp(t *testing.T) {
	request := newRequest(t)
	toValidated(t, request)
	if _, err := request.StartProcessing(input(3)); err != nil {
		t.Fatalf("StartProcessing: %v", err)
	}
	version := request.Version()
	transitions := len(request.Transitions())
	request.PullDomainEvents()

	events, err := request.StartProcessing(input(4))
	if err != nil {
		t.Fatalf("повтор StartProcessing: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("повтор эмитировал события: %v", names(events))
	}
	if request.Status() != domain.StatusProcessing {
		t.Fatalf("статус = %q, ожидался processing", request.Status())
	}
	if request.Version() != version {
		t.Fatalf("версия = %d, ожидалась %d", request.Version(), version)
	}
	if len(request.Transitions()) != transitions {
		t.Fatalf("переходов = %d, ожидалось %d", len(request.Transitions()), transitions)
	}
}

// TestRepeatCancelKeepsSingleDecision — сценарий «Повторное решение не
// дублируется»: повторная отмена уже отменённой заявки не создаёт новый переход и
// новое событие.
func TestRepeatCancelKeepsSingleDecision(t *testing.T) {
	request := newRequest(t)
	if _, err := request.Cancel(inputReason(1, "клиент передумал")); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	version := request.Version()
	transitions := len(request.Transitions())
	request.PullDomainEvents()

	events, err := request.Cancel(inputReason(2, "клиент передумал"))
	if err != nil {
		t.Fatalf("повтор Cancel: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("повтор отмены эмитировал события: %v", names(events))
	}
	if request.Status() != domain.StatusCancelled {
		t.Fatalf("статус = %q, ожидался cancelled", request.Status())
	}
	if request.Version() != version || len(request.Transitions()) != transitions {
		t.Fatalf("состояние изменилось: версия %d→%d, переходов %d→%d",
			version, request.Version(), transitions, len(request.Transitions()))
	}
}

// TestRepeatCancelWithoutReasonIsNoOp — повтор отмены уже отменённой заявки —
// no-op независимо от причины: пустая причина не превращает успешный повтор в
// ErrReasonRequired.
func TestRepeatCancelWithoutReasonIsNoOp(t *testing.T) {
	request := newRequest(t)
	if _, err := request.Cancel(inputReason(1, "клиент передумал")); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	events, err := request.Cancel(input(2))
	if err != nil {
		t.Fatalf("повтор Cancel без причины: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("повтор отмены эмитировал события: %v", names(events))
	}
}

// TestRepeatRejectWithoutReasonIsNoOp — повтор отклонения уже отклонённой заявки
// — no-op независимо от причины.
func TestRepeatRejectWithoutReasonIsNoOp(t *testing.T) {
	request := newRequest(t)
	if _, err := request.StartValidation(input(1)); err != nil {
		t.Fatalf("StartValidation: %v", err)
	}
	if _, err := request.Reject(inputReason(2, "не наш профиль")); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	events, err := request.Reject(input(3))
	if err != nil {
		t.Fatalf("повтор Reject без причины: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("повтор отклонения эмитировал события: %v", names(events))
	}
}

// TestRepeatFulfillIsNoOp — повторное завершение уже исполненной заявки — no-op,
// а не вторая запись перехода.
func TestRepeatFulfillIsNoOp(t *testing.T) {
	request := newRequest(t)
	toValidated(t, request)
	if _, err := request.StartProcessing(input(3)); err != nil {
		t.Fatalf("StartProcessing: %v", err)
	}
	if _, err := request.Fulfill(input(4)); err != nil {
		t.Fatalf("Fulfill: %v", err)
	}
	version := request.Version()
	transitions := len(request.Transitions())

	events, err := request.Fulfill(input(5))
	if err != nil {
		t.Fatalf("повтор Fulfill: %v", err)
	}
	if len(events) != 0 || request.Version() != version || len(request.Transitions()) != transitions {
		t.Fatalf("повтор Fulfill изменил состояние: events=%v version=%d transitions=%d",
			names(events), request.Version(), len(request.Transitions()))
	}
}

// TestRepeatValidateKeepsDecision — повторная валидация уже validated заявки не
// создаёт второе решение и не эмитит событие.
func TestRepeatValidateKeepsDecision(t *testing.T) {
	request := newRequest(t)
	toValidated(t, request)
	decision, ok := request.ValidationDecision()
	if !ok {
		t.Fatal("решение валидации не зафиксировано")
	}
	version := request.Version()

	events, err := request.Validate(input(3))
	if err != nil {
		t.Fatalf("повтор Validate: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("повтор валидации эмитировал события: %v", names(events))
	}
	if request.Version() != version {
		t.Fatalf("версия = %d, ожидалась %d", request.Version(), version)
	}
	again, ok := request.ValidationDecision()
	if !ok || again.Outcome() != decision.Outcome() || !again.At().Equal(decision.At()) {
		t.Fatalf("решение изменилось: %+v → %+v", decision, again)
	}
}

// TestTerminalToDifferentStatusStillRejected — идемпотентность не ослабляет
// запрет: переход из терминального состояния в другой статус по-прежнему
// отвергается.
func TestTerminalToDifferentStatusStillRejected(t *testing.T) {
	request := newRequest(t)
	if _, err := request.Cancel(inputReason(1, "клиент передумал")); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if _, err := request.StartProcessing(input(2)); !errors.Is(err, domain.ErrInvalidTransition) &&
		!errors.Is(err, domain.ErrTerminalState) {
		t.Fatalf("ошибка = %v, ожидалась ErrTerminalState/ErrInvalidTransition", err)
	}
}
