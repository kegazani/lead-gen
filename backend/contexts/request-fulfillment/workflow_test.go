package requestfulfillment

import (
	"context"
	"errors"
	"testing"

	workflowadapter "lead-gen/backend/contexts/request-fulfillment/internal/adapters/workflow"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/command"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/query"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/backend/platform/observability/observ"
)

// TestWorkflowUnavailableKeepsRequestValidated проверяет поведение при
// недоступности Temporal (change, задача 4.2): попытка запустить исполнение
// отклоняется, а заявка остаётся в статусе validated — состояние не теряется.
func TestWorkflowUnavailableKeepsRequestValidated(t *testing.T) {
	ctx := context.Background()
	slice, err := Build(Config{
		Logger:  observ.NewLogger(),
		Metrics: observ.NewMetrics(),
		Tracer:  observ.NewTracer(ServiceID),
	})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}

	if _, err := slice.SubmitRequest.Handle(ctx, command.SubmitRequest{
		RequestID: "req-1", OfferID: "offer-1", TenantID: "tenant-1",
		Content: "предлагаем X", Actor: "client",
	}); err != nil {
		t.Fatalf("подача заявки: %v", err)
	}
	fields := command.LifecycleFields{RequestID: "req-1", TenantID: "tenant-1", Actor: "admin"}
	if _, err := slice.StartValidation.Handle(ctx, command.StartValidation{LifecycleFields: fields}); err != nil {
		t.Fatalf("начало валидации: %v", err)
	}
	if _, err := slice.ValidateRequest.Handle(ctx, command.ValidateRequest{LifecycleFields: fields}); err != nil {
		t.Fatalf("подтверждение валидации: %v", err)
	}

	// Клиент Temporal не подключён: адаптер обязан честно сообщить о недоступности.
	starter := workflowadapter.NewClient(nil, "")
	if _, err := starter.StartFulfillment(ctx, ports.StartFulfillment{RequestID: "req-1", TenantID: "tenant-1"}); !errors.Is(err, ports.ErrWorkflowUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrWorkflowUnavailable", err)
	}

	view, err := slice.GetRequest.Handle(ctx, query.GetRequest{RequestID: "req-1", TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("чтение заявки: %v", err)
	}
	if view.Status != domain.StatusValidated {
		t.Fatalf("статус = %q, ожидался %q", view.Status, domain.StatusValidated)
	}
}
