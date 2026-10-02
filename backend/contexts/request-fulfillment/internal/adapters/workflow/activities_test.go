package workflowadapter

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/request-fulfillment/internal/adapters/idgen"
	"lead-gen/backend/contexts/request-fulfillment/internal/adapters/outbox/inmemory"
	persistence "lead-gen/backend/contexts/request-fulfillment/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/command"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
)

// fixedClock — часы с фиксированным временем для детерминированных тестов.
type fixedClock struct{ at time.Time }

// Now реализует ports.Clock.
func (c fixedClock) Now() time.Time { return c.at }

// newValidatedRequest собирает срез контекста и заявку в статусе validated:
// activity-тесты проверяют вызов application service, а не повторяют его тесты.
func newValidatedRequest(t *testing.T) (*Activities, *persistence.Repository) {
	t.Helper()
	ctx := context.Background()
	clock := fixedClock{at: time.Unix(1700000000, 0).UTC()}
	ids := idgen.NewRandom()
	repo := persistence.New()
	uow := inmemory.NewUnitOfWorkWith(repo, platformoutbox.NewMemoryJournal())

	submit := command.NewSubmitRequestHandler(uow, clock, ids)
	startValidation := command.NewStartValidationHandler(uow, clock, ids)
	validate := command.NewValidateRequestHandler(uow, clock, ids)

	if _, err := submit.Handle(ctx, command.SubmitRequest{
		RequestID: "req-1", OfferID: "offer-1", TenantID: "tenant-1",
		Content: "предлагаем X", Actor: "client", At: clock.Now(),
	}); err != nil {
		t.Fatalf("подача заявки: %v", err)
	}
	fields := command.LifecycleFields{RequestID: "req-1", TenantID: "tenant-1", Actor: "admin", At: clock.Now()}
	if _, err := startValidation.Handle(ctx, command.StartValidation{LifecycleFields: fields}); err != nil {
		t.Fatalf("начало валидации: %v", err)
	}
	if _, err := validate.Handle(ctx, command.ValidateRequest{LifecycleFields: fields}); err != nil {
		t.Fatalf("подтверждение валидации: %v", err)
	}

	return &Activities{
		StartProcessing: command.NewStartProcessingHandler(uow, clock, ids),
		Fulfill:         command.NewFulfillRequestHandler(uow, clock, ids),
		Cancel:          command.NewCancelRequestHandler(uow, clock, ids),
		Clock:           clock,
		IDs:             ids,
	}, repo
}

// TestHandleStartProcessingCallsApplicationService проверяет, что activity —
// тонкий адаптер: она переводит заявку в processing через application service,
// а не содержит доменной логики.
func TestHandleStartProcessingCallsApplicationService(t *testing.T) {
	activities, repo := newValidatedRequest(t)
	ctx := context.Background()

	if err := activities.HandleStartProcessing(ctx, transitionInput{
		RequestID: "req-1", TenantID: "tenant-1", CorrelationID: "corr-1", Actor: "workflow",
	}); err != nil {
		t.Fatalf("activity запуска исполнения: %v", err)
	}

	request, err := repo.FindByID(ctx, "req-1")
	if err != nil {
		t.Fatalf("чтение заявки: %v", err)
	}
	if request.Status() != domain.StatusProcessing {
		t.Fatalf("статус = %q, ожидался %q", request.Status(), domain.StatusProcessing)
	}
}

// TestHandleStartProcessingPropagatesDomainError проверяет, что activity не
// подменяет доменную ошибку успехом: недопустимый переход возвращается
// вызывающему.
func TestHandleStartProcessingPropagatesDomainError(t *testing.T) {
	activities, repo := newValidatedRequest(t)
	ctx := context.Background()

	// Заявка ещё в validating: сначала отклоним её повторным вызовом на неверном
	// тенанте — activity обязана вернуть ошибку сценария.
	err := activities.HandleStartProcessing(ctx, transitionInput{
		RequestID: "req-1", TenantID: "tenant-other",
	})
	if err == nil {
		t.Fatal("ожидалась ошибка о чужом тенанте")
	}
	if !errors.Is(err, command.ErrTenantMismatch) {
		t.Fatalf("ошибка = %v, ожидалась ErrTenantMismatch", err)
	}

	request, err := repo.FindByID(ctx, "req-1")
	if err != nil {
		t.Fatalf("чтение заявки: %v", err)
	}
	if request.Status() != domain.StatusValidated {
		t.Fatalf("статус изменился на %q вопреки отказу", request.Status())
	}
}
