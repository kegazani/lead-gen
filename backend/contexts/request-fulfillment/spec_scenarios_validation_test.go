package requestfulfillment_test

import (
	"context"
	"errors"
	"testing"

	requestfulfillment "lead-gen/backend/contexts/request-fulfillment"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/command"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/contracts/outreach-orchestration/callintent"
)

// TestSpecScenariosValidation покрывает каждый сценарий спецификации capability
// request-validation. Имена подтестов повторяют сценарии spec.md.
func TestSpecScenariosValidation(t *testing.T) {
	ctx := context.Background()

	slice := func(t *testing.T) *requestfulfillment.Slice {
		t.Helper()
		built, err := requestfulfillment.Build(requestfulfillment.Config{})
		if err != nil {
			t.Fatalf("сборка среза: %v", err)
		}
		return built
	}

	submit := func(t *testing.T, s *requestfulfillment.Slice) {
		t.Helper()
		if _, err := s.SubmitRequest.Handle(ctx, command.SubmitRequest{
			RequestID: "req-1", OfferID: "offer-1", TenantID: "tenant-1",
			Content: "Предлагаем автоматизацию найма", Target: "IT-компании", Actor: "client",
		}); err != nil {
			t.Fatalf("SubmitRequest: %v", err)
		}
	}

	fields := func(tenant string) command.LifecycleFields {
		return command.LifecycleFields{RequestID: "req-1", TenantID: domain.TenantID(tenant), Actor: "admin"}
	}

	t.Run("Взятие в работу", func(t *testing.T) {
		s := slice(t)
		submit(t, s)
		result, err := s.StartValidation.Handle(ctx, command.StartValidation{LifecycleFields: fields("tenant-1")})
		if err != nil {
			t.Fatalf("StartValidation: %v", err)
		}
		if result.Status != domain.StatusValidating {
			t.Fatalf("статус = %q, ожидался validating", result.Status)
		}
		request, err := s.Repository.FindByID(ctx, "req-1")
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		transitions := request.Transitions()
		last := transitions[len(transitions)-1]
		if last.Actor() != "admin" || last.At().IsZero() {
			t.Fatalf("переход без актора или времени: %+v", last)
		}
	})

	t.Run("Одобрение", func(t *testing.T) {
		s := slice(t)
		submit(t, s)
		if _, err := s.StartValidation.Handle(ctx, command.StartValidation{LifecycleFields: fields("tenant-1")}); err != nil {
			t.Fatalf("StartValidation: %v", err)
		}
		result, err := s.ValidateRequest.Handle(ctx, command.ValidateRequest{LifecycleFields: fields("tenant-1")})
		if err != nil {
			t.Fatalf("ValidateRequest: %v", err)
		}
		if result.Status != domain.StatusValidated {
			t.Fatalf("статус = %q, ожидался validated", result.Status)
		}
	})

	t.Run("Отклонение без причины", func(t *testing.T) {
		s := slice(t)
		submit(t, s)
		if _, err := s.StartValidation.Handle(ctx, command.StartValidation{LifecycleFields: fields("tenant-1")}); err != nil {
			t.Fatalf("StartValidation: %v", err)
		}
		_, err := s.RejectRequest.Handle(ctx, command.RejectRequest{LifecycleFields: fields("tenant-1")})
		if !errors.Is(err, domain.ErrReasonRequired) {
			t.Fatalf("ошибка = %v, ожидалась ErrReasonRequired", err)
		}
	})

	t.Run("Отклонение с причиной", func(t *testing.T) {
		s := slice(t)
		submit(t, s)
		if _, err := s.StartValidation.Handle(ctx, command.StartValidation{LifecycleFields: fields("tenant-1")}); err != nil {
			t.Fatalf("StartValidation: %v", err)
		}
		result, err := s.RejectRequest.Handle(ctx, command.RejectRequest{
			LifecycleFields: fields("tenant-1"),
			Reason:          "оффер не соответствует ICP",
		})
		if err != nil {
			t.Fatalf("RejectRequest: %v", err)
		}
		if result.Status != domain.StatusRejected {
			t.Fatalf("статус = %q, ожидался rejected", result.Status)
		}
		request, err := s.Repository.FindByID(ctx, "req-1")
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		decision, ok := request.ValidationDecision()
		if !ok || decision.Reason() != "оффер не соответствует ICP" {
			t.Fatalf("причина решения = %+v", decision)
		}
	})

	t.Run("Инициирование звонка", func(t *testing.T) {
		s, err := requestfulfillment.Build(requestfulfillment.Config{
			CallContract: callintent.InProc(func(context.Context, callintent.Intent) (callintent.Result, error) {
				return callintent.Result{CallID: "call-1"}, nil
			}),
		})
		if err != nil {
			t.Fatalf("сборка среза: %v", err)
		}
		submit(t, s)
		if _, err := s.StartValidation.Handle(ctx, command.StartValidation{LifecycleFields: fields("tenant-1")}); err != nil {
			t.Fatalf("StartValidation: %v", err)
		}
		result, err := s.InitiateCall.Handle(ctx, command.InitiateCall{
			RequestID: "req-1", TenantID: "tenant-1", CorrelationID: "corr-1",
		})
		if err != nil {
			t.Fatalf("InitiateCall: %v", err)
		}
		if result.CallID != "call-1" {
			t.Fatalf("call_id = %q, ожидался call-1", result.CallID)
		}
		request, err := s.Repository.FindByID(ctx, "req-1")
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		if request.CallID() != "call-1" {
			t.Fatalf("заявка не сохранила звонок: %q", request.CallID())
		}
	})

	t.Run("Правка до валидации", func(t *testing.T) {
		s := slice(t)
		submit(t, s)
		if _, err := s.StartValidation.Handle(ctx, command.StartValidation{LifecycleFields: fields("tenant-1")}); err != nil {
			t.Fatalf("StartValidation: %v", err)
		}
		result, err := s.ReviseOffer.Handle(ctx, command.ReviseOffer{
			RequestID: "req-1", TenantID: "tenant-1", OfferID: "offer-2",
			Content: "Уточнённый оффер", Target: "SaaS-компании", Actor: "admin",
		})
		if err != nil {
			t.Fatalf("ReviseOffer: %v", err)
		}
		if result.OfferRevision != 1 {
			t.Fatalf("ревизия = %d, ожидалась 1 (правка на месте)", result.OfferRevision)
		}
		request, err := s.Repository.FindByID(ctx, "req-1")
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		if request.Offer().Content().Text() != "Уточнённый оффер" {
			t.Fatalf("оффер не изменён: %q", request.Offer().Content().Text())
		}
		if len(request.OfferHistory()) != 0 {
			t.Fatalf("создана версия вопреки правке на месте: %+v", request.OfferHistory())
		}
	})

	t.Run("Аудит решения", func(t *testing.T) {
		s := slice(t)
		submit(t, s)
		if _, err := s.StartValidation.Handle(ctx, command.StartValidation{LifecycleFields: fields("tenant-1")}); err != nil {
			t.Fatalf("StartValidation: %v", err)
		}
		if _, err := s.ValidateRequest.Handle(ctx, command.ValidateRequest{LifecycleFields: fields("tenant-1")}); err != nil {
			t.Fatalf("ValidateRequest: %v", err)
		}
		request, err := s.Repository.FindByID(ctx, "req-1")
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		decision, ok := request.ValidationDecision()
		if !ok {
			t.Fatal("решение валидации не зафиксировано")
		}
		if decision.Actor() != "admin" || decision.At().IsZero() || decision.Outcome() != domain.ValidationApproved {
			t.Fatalf("решение = %+v", decision)
		}
	})
}

// TestInitiateCallThroughContract — интеграционная проверка задачи 3.1:
// инициирование звонка идёт через контракт call-intent, а заявка фиксирует
// возвращённый call_id.
func TestInitiateCallThroughContract(t *testing.T) {
	ctx := context.Background()
	var received callintent.Intent
	built, err := requestfulfillment.Build(requestfulfillment.Config{
		CallContract: callintent.InProc(func(_ context.Context, intent callintent.Intent) (callintent.Result, error) {
			received = intent
			return callintent.Result{CallID: "call-77"}, nil
		}),
	})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	if _, err := built.SubmitRequest.Handle(ctx, command.SubmitRequest{
		RequestID: "req-1", OfferID: "offer-1", TenantID: "tenant-1",
		Content: "Оффер", Actor: "client",
	}); err != nil {
		t.Fatalf("SubmitRequest: %v", err)
	}
	lifecycle := command.LifecycleFields{RequestID: "req-1", TenantID: "tenant-1", Actor: "admin"}
	if _, err := built.StartValidation.Handle(ctx, command.StartValidation{LifecycleFields: lifecycle}); err != nil {
		t.Fatalf("StartValidation: %v", err)
	}

	result, err := built.InitiateCall.Handle(ctx, command.InitiateCall{
		RequestID: "req-1", TenantID: "tenant-1", CorrelationID: "corr-1",
	})
	if err != nil {
		t.Fatalf("InitiateCall: %v", err)
	}
	if result.CallID != "call-77" {
		t.Fatalf("call_id = %q", result.CallID)
	}
	if received.Purpose != callintent.PurposeOfferClarification || received.TenantID != "tenant-1" {
		t.Fatalf("контракт получил %+v", received)
	}
	request, err := built.Repository.FindByID(ctx, "req-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if request.CallID() != "call-77" {
		t.Fatalf("заявка не сохранила call_id: %q", request.CallID())
	}
}

// TestInitiateCallUnavailableKeepsRequest — сбой контракта звонка не портит
// заявку: статус и ссылка на звонок остаются прежними.
func TestInitiateCallUnavailableKeepsRequest(t *testing.T) {
	ctx := context.Background()
	built, err := requestfulfillment.Build(requestfulfillment.Config{})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	if _, err := built.SubmitRequest.Handle(ctx, command.SubmitRequest{
		RequestID: "req-1", OfferID: "offer-1", TenantID: "tenant-1",
		Content: "Оффер", Actor: "client",
	}); err != nil {
		t.Fatalf("SubmitRequest: %v", err)
	}
	lifecycle := command.LifecycleFields{RequestID: "req-1", TenantID: "tenant-1", Actor: "admin"}
	if _, err := built.StartValidation.Handle(ctx, command.StartValidation{LifecycleFields: lifecycle}); err != nil {
		t.Fatalf("StartValidation: %v", err)
	}

	if _, err := built.InitiateCall.Handle(ctx, command.InitiateCall{RequestID: "req-1", TenantID: "tenant-1"}); !errors.Is(err, ports.ErrCallUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrCallUnavailable", err)
	}
	request, err := built.Repository.FindByID(ctx, "req-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if request.Status() != domain.StatusValidating || request.CallID() != "" {
		t.Fatalf("заявка изменена при сбое контракта: status=%q call=%q", request.Status(), request.CallID())
	}
}
