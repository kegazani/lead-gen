package domain_test

import (
	"errors"
	"testing"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
)

// TestValidationDecisionApproved — сценарий «Одобрение»: решение фиксируется с
// актором и временем.
func TestValidationDecisionApproved(t *testing.T) {
	request := newRequest(t)
	if _, err := request.StartValidation(input(1)); err != nil {
		t.Fatalf("StartValidation: %v", err)
	}
	if _, err := request.Validate(input(2)); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	decision, ok := request.ValidationDecision()
	if !ok {
		t.Fatal("решение валидации не зафиксировано")
	}
	if decision.Outcome() != domain.ValidationApproved || !decision.Approved() {
		t.Fatalf("исход = %q", decision.Outcome())
	}
	if decision.Actor() != "actor-1" || decision.At().IsZero() {
		t.Fatalf("решение без актора или времени: %+v", decision)
	}
}

// TestValidationDecisionRejected — сценарий «Отклонение с причиной»: причина
// сохраняется в решении.
func TestValidationDecisionRejected(t *testing.T) {
	request := newRequest(t)
	if _, err := request.StartValidation(input(1)); err != nil {
		t.Fatalf("StartValidation: %v", err)
	}
	if _, err := request.Reject(inputReason(2, "недостаточно данных об оффере")); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	decision, ok := request.ValidationDecision()
	if !ok {
		t.Fatal("решение валидации не зафиксировано")
	}
	if decision.Outcome() != domain.ValidationRejected || decision.Approved() {
		t.Fatalf("исход = %q", decision.Outcome())
	}
	if decision.Reason() != "недостаточно данных об оффере" {
		t.Fatalf("причина = %q", decision.Reason())
	}
}

// TestRejectWithoutReason — сценарий «Отклонение без причины»: отклонение не
// выполняется, статус остаётся прежним, решение не фиксируется.
func TestRejectWithoutReason(t *testing.T) {
	request := newRequest(t)
	if _, err := request.StartValidation(input(1)); err != nil {
		t.Fatalf("StartValidation: %v", err)
	}
	if _, err := request.Reject(input(2)); !errors.Is(err, domain.ErrReasonRequired) {
		t.Fatalf("ошибка = %v, ожидалась ErrReasonRequired", err)
	}
	if request.Status() != domain.StatusValidating {
		t.Fatalf("статус = %q, ожидался validating", request.Status())
	}
	if _, ok := request.ValidationDecision(); ok {
		t.Fatal("решение зафиксировано вопреки отказу")
	}
}

// TestOfferEditedInPlaceBeforeValidation — сценарий «Правка до валидации»: оффер
// меняется на месте, новая версия не создаётся.
func TestOfferEditedInPlaceBeforeValidation(t *testing.T) {
	request := newRequest(t)
	if _, err := request.StartValidation(input(1)); err != nil {
		t.Fatalf("StartValidation: %v", err)
	}
	offer, err := request.ReviseOffer(input(2), "offer-2", "Уточнённое предложение", "SaaS-компании")
	if err != nil {
		t.Fatalf("ReviseOffer: %v", err)
	}
	if offer.Revision() != 1 {
		t.Fatalf("ревизия = %d, ожидалась 1 (правка на месте)", offer.Revision())
	}
	if request.Offer().Content().Text() != "Уточнённое предложение" {
		t.Fatalf("содержимое = %q", request.Offer().Content().Text())
	}
	if len(request.OfferHistory()) != 0 {
		t.Fatalf("создана история версий вопреки правке на месте: %+v", request.OfferHistory())
	}
}

// TestAttachCall — сценарий «Инициирование звонка»: заявка хранит идентификатор
// звонка; пустой идентификатор отвергается.
func TestAttachCall(t *testing.T) {
	request := newRequest(t)
	if _, err := request.StartValidation(input(1)); err != nil {
		t.Fatalf("StartValidation: %v", err)
	}
	if err := request.AttachCall("call-1"); err != nil {
		t.Fatalf("AttachCall: %v", err)
	}
	if request.CallID() != "call-1" {
		t.Fatalf("call_id = %q, ожидался call-1", request.CallID())
	}
	if err := request.AttachCall("   "); !errors.Is(err, domain.ErrCallIDRequired) {
		t.Fatalf("ошибка = %v, ожидалась ErrCallIDRequired", err)
	}
}

// TestSnapshotRoundTripKeepsDecisionAndCall проверяет, что решение валидации и
// ссылка на звонок переживают сохранение и восстановление.
func TestSnapshotRoundTripKeepsDecisionAndCall(t *testing.T) {
	request := newRequest(t)
	if _, err := request.StartValidation(input(1)); err != nil {
		t.Fatalf("StartValidation: %v", err)
	}
	if _, err := request.Validate(input(2)); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if err := request.AttachCall("call-1"); err != nil {
		t.Fatalf("AttachCall: %v", err)
	}

	restored, err := domain.RehydrateRequest(request.Snapshot())
	if err != nil {
		t.Fatalf("RehydrateRequest: %v", err)
	}
	decision, ok := restored.ValidationDecision()
	if !ok || decision.Outcome() != domain.ValidationApproved {
		t.Fatalf("решение потеряно при восстановлении: %+v (%v)", decision, ok)
	}
	if restored.CallID() != "call-1" {
		t.Fatalf("call_id = %q", restored.CallID())
	}
}

// TestRehydrateValidatedWithoutDecisionFails проверяет инвариант: статус validated
// без решения валидации — противоречивое состояние.
func TestRehydrateValidatedWithoutDecisionFails(t *testing.T) {
	request := newRequest(t)
	toValidated(t, request)
	snapshot := request.Snapshot()
	snapshot.ValidationDecision = nil
	if _, err := domain.RehydrateRequest(snapshot); !errors.Is(err, domain.ErrInvalidSnapshot) {
		t.Fatalf("ошибка = %v, ожидалась ErrInvalidSnapshot", err)
	}
}
