package requestfulfillment_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"lead-gen/backend/contexts/request-fulfillment/internal/app/command"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
)

// returnRev / resubmit — новые обработчики поверх общего harness.
func (h *harness) returnRev() *command.ReturnForRevisionHandler {
	return command.NewReturnForRevisionHandler(h.uow, h.clock, h.ids)
}

func (h *harness) resubmit() *command.ResubmitRequestHandler {
	return command.NewResubmitRequestHandler(h.uow, h.clock, h.ids)
}

// submitByID подаёт заявку с заданным идентификатором и тенантом.
func submitByID(t *testing.T, h *harness, id, tenant string) {
	t.Helper()
	_, err := h.submit.Handle(context.Background(), command.SubmitRequest{
		RequestID: domain.RequestID(id), OfferID: domain.OfferID("offer-" + id),
		TenantID: domain.TenantID(tenant), Content: "Оффер", Target: "IT-компании",
		Actor: "actor-1",
	})
	if err != nil {
		t.Fatalf("SubmitRequest %s: %v", id, err)
	}
}

// fieldsFor собирает общую часть команды для произвольной заявки.
func fieldsFor(id, tenant string) command.LifecycleFields {
	return command.LifecycleFields{
		RequestID: domain.RequestID(id), TenantID: domain.TenantID(tenant),
		Actor: "actor-1", CorrelationID: domain.CorrelationID("corr-" + id), CausationID: domain.CausationID("cause-" + id),
	}
}

// toProcessing доводит заявку до статуса processing.
func toProcessing(t *testing.T, h *harness, id, tenant string) {
	t.Helper()
	ctx := context.Background()
	if _, err := h.startVal.Handle(ctx, command.StartValidation{LifecycleFields: fieldsFor(id, tenant)}); err != nil {
		t.Fatalf("StartValidation %s: %v", id, err)
	}
	if _, err := h.validate.Handle(ctx, command.ValidateRequest{LifecycleFields: fieldsFor(id, tenant)}); err != nil {
		t.Fatalf("ValidateRequest %s: %v", id, err)
	}
	if _, err := h.startProc.Handle(ctx, command.StartProcessing{LifecycleFields: fieldsFor(id, tenant)}); err != nil {
		t.Fatalf("StartProcessing %s: %v", id, err)
	}
}

// TestReturnForRevisionFlow — возврат на доработку из validating и processing
// публикует request-returned-for-revision.
func TestReturnForRevisionFlow(t *testing.T) {
	for _, fromProcessing := range []bool{false, true} {
		fromProcessing := fromProcessing
		t.Run(fmt.Sprintf("from-processing=%v", fromProcessing), func(t *testing.T) {
			h := newHarness()
			ctx := context.Background()
			submitByID(t, h, "req-1", "tenant-1")
			if fromProcessing {
				toProcessing(t, h, "req-1", "tenant-1")
			} else {
				if _, err := h.startVal.Handle(ctx, command.StartValidation{LifecycleFields: fieldsFor("req-1", "tenant-1")}); err != nil {
					t.Fatalf("StartValidation: %v", err)
				}
			}

			result, err := h.returnRev().Handle(ctx, command.ReturnForRevision{
				LifecycleFields: fieldsFor("req-1", "tenant-1"), Reason: "уточните цель",
			})
			if err != nil {
				t.Fatalf("ReturnForRevision: %v", err)
			}
			if result.Status != domain.StatusNeedsRevision {
				t.Fatalf("статус = %q, ожидался needs-revision", result.Status)
			}

			names := eventNames(h.pending(t))
			if names[len(names)-1] != "request-returned-for-revision" {
				t.Fatalf("последнее событие = %q, ожидалось request-returned-for-revision", names[len(names)-1])
			}
		})
	}
}

// TestReturnForRevisionRequiresReason — возврат без причины отклоняется.
func TestReturnForRevisionRequiresReason(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	submitByID(t, h, "req-1", "tenant-1")
	if _, err := h.startVal.Handle(ctx, command.StartValidation{LifecycleFields: fieldsFor("req-1", "tenant-1")}); err != nil {
		t.Fatalf("StartValidation: %v", err)
	}
	if _, err := h.returnRev().Handle(ctx, command.ReturnForRevision{LifecycleFields: fieldsFor("req-1", "tenant-1")}); !errors.Is(err, domain.ErrReasonRequired) {
		t.Fatalf("ошибка = %v, ожидалась ErrReasonRequired", err)
	}
}

// TestResubmitFlow — повторная подача needs-revision → submitted публикует
// request-resubmitted.
func TestResubmitFlow(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	submitByID(t, h, "req-1", "tenant-1")
	if _, err := h.startVal.Handle(ctx, command.StartValidation{LifecycleFields: fieldsFor("req-1", "tenant-1")}); err != nil {
		t.Fatalf("StartValidation: %v", err)
	}
	if _, err := h.returnRev().Handle(ctx, command.ReturnForRevision{
		LifecycleFields: fieldsFor("req-1", "tenant-1"), Reason: "уточните цель",
	}); err != nil {
		t.Fatalf("ReturnForRevision: %v", err)
	}

	result, err := h.resubmit().Handle(ctx, command.ResubmitRequest{LifecycleFields: fieldsFor("req-1", "tenant-1")})
	if err != nil {
		t.Fatalf("Resubmit: %v", err)
	}
	if result.Status != domain.StatusSubmitted {
		t.Fatalf("статус = %q, ожидался submitted", result.Status)
	}
	names := eventNames(h.pending(t))
	if names[len(names)-1] != "request-resubmitted" {
		t.Fatalf("последнее событие = %q, ожидалось request-resubmitted", names[len(names)-1])
	}
}

// TestSubmitRequestLimit — шестая нетерминальная заявка отклоняется.
func TestSubmitRequestLimit(t *testing.T) {
	h := newHarness()
	for i := 1; i <= 5; i++ {
		submitByID(t, h, fmt.Sprintf("req-%d", i), "tenant-1")
	}
	_, err := h.submit.Handle(context.Background(), command.SubmitRequest{
		RequestID: "req-6", OfferID: "offer-6", TenantID: "tenant-1",
		Content: "Оффер", Actor: "actor-1",
	})
	if !errors.Is(err, domain.ErrRequestLimitReached) {
		t.Fatalf("ошибка = %v, ожидалась ErrRequestLimitReached", err)
	}
}

// TestSubmitRequestLimitFreesAfterTerminal — после завершения заявки лимит
// освобождается.
func TestSubmitRequestLimitFreesAfterTerminal(t *testing.T) {
	h := newHarness()
	for i := 1; i <= 5; i++ {
		submitByID(t, h, fmt.Sprintf("req-%d", i), "tenant-1")
	}
	// Завершаем одну заявку: fulfilled — терминальный статус.
	toProcessing(t, h, "req-1", "tenant-1")
	if _, err := h.fulfill.Handle(context.Background(), command.FulfillRequest{LifecycleFields: fieldsFor("req-1", "tenant-1")}); err != nil {
		t.Fatalf("FulfillRequest: %v", err)
	}
	submitByID(t, h, "req-6", "tenant-1")
}

// TestStartProcessingLimit — вторая заявка в работу отклоняется.
func TestStartProcessingLimit(t *testing.T) {
	h := newHarness()
	submitByID(t, h, "req-1", "tenant-1")
	toProcessing(t, h, "req-1", "tenant-1")

	submitByID(t, h, "req-2", "tenant-1")
	if _, err := h.startVal.Handle(context.Background(), command.StartValidation{LifecycleFields: fieldsFor("req-2", "tenant-1")}); err != nil {
		t.Fatalf("StartValidation req-2: %v", err)
	}
	if _, err := h.validate.Handle(context.Background(), command.ValidateRequest{LifecycleFields: fieldsFor("req-2", "tenant-1")}); err != nil {
		t.Fatalf("ValidateRequest req-2: %v", err)
	}
	if _, err := h.startProc.Handle(context.Background(), command.StartProcessing{LifecycleFields: fieldsFor("req-2", "tenant-1")}); !errors.Is(err, domain.ErrActiveRequestLimitReached) {
		t.Fatalf("ошибка = %v, ожидалась ErrActiveRequestLimitReached", err)
	}
}

// TestStartProcessingLimitFreesAfterFulfill — после завершения работы можно
// запустить следующую заявку в работу.
func TestStartProcessingLimitFreesAfterFulfill(t *testing.T) {
	h := newHarness()
	submitByID(t, h, "req-1", "tenant-1")
	toProcessing(t, h, "req-1", "tenant-1")
	if _, err := h.fulfill.Handle(context.Background(), command.FulfillRequest{LifecycleFields: fieldsFor("req-1", "tenant-1")}); err != nil {
		t.Fatalf("FulfillRequest: %v", err)
	}

	submitByID(t, h, "req-2", "tenant-1")
	toProcessing(t, h, "req-2", "tenant-1")
}
