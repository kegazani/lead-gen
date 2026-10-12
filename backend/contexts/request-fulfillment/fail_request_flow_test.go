package requestfulfillment_test

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/request-fulfillment/internal/adapters/outbox/inmemory"
	persistence "lead-gen/backend/contexts/request-fulfillment/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/command"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
)

// failHarness собирает сценарии перехода в failed и повторного запуска поверх
// общего репозитория.
type failHarness struct {
	uow       *inmemory.UnitOfWork
	repo      *persistence.Repository
	journal   *inmemory.TransactionalJournal
	clock     fixedClock
	ids       *seqIDs
	submit    *command.SubmitRequestHandler
	startVal  *command.StartValidationHandler
	validate  *command.ValidateRequestHandler
	startProc *command.StartProcessingHandler
	fail      *command.FailRequestHandler
	rerun     *command.RerunRequestHandler
}

func newFailHarness() *failHarness {
	repo := persistence.New()
	journal := inmemory.NewTransactionalJournal()
	uow := inmemory.NewUnitOfWorkWith(repo, journal)
	clock := fixedClock{at: epoch}
	ids := &seqIDs{}
	return &failHarness{
		uow:       uow,
		repo:      repo,
		journal:   journal,
		clock:     clock,
		ids:       ids,
		submit:    command.NewSubmitRequestHandler(uow, clock, ids),
		startVal:  command.NewStartValidationHandler(uow, clock, ids),
		validate:  command.NewValidateRequestHandler(uow, clock, ids),
		startProc: command.NewStartProcessingHandler(uow, clock, ids),
		fail:      command.NewFailRequestHandler(uow, clock, ids),
		rerun:     command.NewRerunRequestHandler(uow, clock, ids),
	}
}

// toProcessing доводит заявку до processing.
func (h *failHarness) toProcessing(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if _, err := h.submit.Handle(ctx, command.SubmitRequest{
		RequestID: "req-1", OfferID: "offer-1", TenantID: "tenant-1",
		Content: "Предлагаем автоматизацию найма", Target: "IT-компании", Actor: "actor-1",
	}); err != nil {
		t.Fatalf("SubmitRequest: %v", err)
	}
	if _, err := h.startVal.Handle(ctx, command.StartValidation{LifecycleFields: fields()}); err != nil {
		t.Fatalf("StartValidation: %v", err)
	}
	if _, err := h.validate.Handle(ctx, command.ValidateRequest{LifecycleFields: fields()}); err != nil {
		t.Fatalf("ValidateRequest: %v", err)
	}
	if _, err := h.startProc.Handle(ctx, command.StartProcessing{LifecycleFields: fields()}); err != nil {
		t.Fatalf("StartProcessing: %v", err)
	}
}

// pending возвращает ожидающие публикации записи outbox.
func (h *failHarness) pending(t *testing.T) []platformoutbox.Message {
	t.Helper()
	messages, err := h.journal.Pending(context.Background(), 0)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	return messages
}

// TestFailRequestHandler — команда FailRequest переводит processing → failed и
// публикует request-execution-failed с кодом и текстом.
func TestFailRequestHandler(t *testing.T) {
	h := newFailHarness()
	h.toProcessing(t)
	ctx := context.Background()

	result, err := h.fail.Handle(ctx, command.FailRequest{
		LifecycleFields: fields(),
		ReasonCode:      "search_failed",
		Reason:          "поиск упал",
	})
	if err != nil {
		t.Fatalf("FailRequest: %v", err)
	}
	if result.Status != domain.StatusFailed {
		t.Fatalf("статус = %q, ожидался failed", result.Status)
	}

	names := eventNames(h.pending(t))
	if len(names) == 0 || names[len(names)-1] != "request-execution-failed" {
		t.Fatalf("последнее событие = %v, ожидалось request-execution-failed", names)
	}
}

// TestFailRequestHandlerIdempotent — повторный FailRequest по already-failed
// заявке — no-op: версия и число событий не растут.
func TestFailRequestHandlerIdempotent(t *testing.T) {
	h := newFailHarness()
	h.toProcessing(t)
	ctx := context.Background()

	if _, err := h.fail.Handle(ctx, command.FailRequest{
		LifecycleFields: fields(), ReasonCode: "search_failed", Reason: "поиск упал",
	}); err != nil {
		t.Fatalf("FailRequest: %v", err)
	}
	request, err := h.repo.FindByID(ctx, "req-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	version := request.Version()
	events := len(h.pending(t))

	if _, err := h.fail.Handle(ctx, command.FailRequest{
		LifecycleFields: fields(), ReasonCode: "search_failed", Reason: "поиск упал",
	}); err != nil {
		t.Fatalf("повтор FailRequest: %v", err)
	}
	request, err = h.repo.FindByID(ctx, "req-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if request.Version() != version || len(h.pending(t)) != events {
		t.Fatalf("повтор изменил состояние: версия %d→%d, событий %d→%d",
			version, request.Version(), events, len(h.pending(t)))
	}
}

// TestFailRequestHandlerTerminalError — FailRequest по терминальной заявке даёт
// доменную ошибку терминального состояния.
func TestFailRequestHandlerTerminalError(t *testing.T) {
	h := newFailHarness()
	h.toProcessing(t)
	ctx := context.Background()

	fulfill := command.NewFulfillRequestHandler(h.uow, h.clock, h.ids)
	if _, err := fulfill.Handle(ctx, command.FulfillRequest{LifecycleFields: fields()}); err != nil {
		t.Fatalf("FulfillRequest: %v", err)
	}
	if _, err := h.fail.Handle(ctx, command.FailRequest{
		LifecycleFields: fields(), ReasonCode: "search_failed", Reason: "поиск упал",
	}); !errors.Is(err, domain.ErrTerminalState) {
		t.Fatalf("ошибка = %v, ожидалась ErrTerminalState", err)
	}
}

// TestRerunRequestHandler — команда RerunRequest переводит failed → validated.
func TestRerunRequestHandler(t *testing.T) {
	h := newFailHarness()
	h.toProcessing(t)
	ctx := context.Background()

	if _, err := h.fail.Handle(ctx, command.FailRequest{
		LifecycleFields: fields(), ReasonCode: "handoff_failed", Reason: "передача упала",
	}); err != nil {
		t.Fatalf("FailRequest: %v", err)
	}
	result, err := h.rerun.Handle(ctx, command.RerunRequest{LifecycleFields: fields(), Reason: "повторный запуск"})
	if err != nil {
		t.Fatalf("RerunRequest: %v", err)
	}
	if result.Status != domain.StatusValidated {
		t.Fatalf("статус = %q, ожидался validated", result.Status)
	}
}

// TestRerunRequestHandlerRestartsExecution — после явного повторного запуска
// стандартный путь validated → processing снова запускает исполнение той же заявки,
// без создания новой.
func TestRerunRequestHandlerRestartsExecution(t *testing.T) {
	h := newFailHarness()
	h.toProcessing(t)
	ctx := context.Background()

	if _, err := h.fail.Handle(ctx, command.FailRequest{
		LifecycleFields: fields(), ReasonCode: "search_failed", Reason: "поиск упал",
	}); err != nil {
		t.Fatalf("FailRequest: %v", err)
	}
	if _, err := h.rerun.Handle(ctx, command.RerunRequest{LifecycleFields: fields(), Reason: "повторный запуск"}); err != nil {
		t.Fatalf("RerunRequest: %v", err)
	}
	result, err := h.startProc.Handle(ctx, command.StartProcessing{LifecycleFields: fields()})
	if err != nil {
		t.Fatalf("StartProcessing после re-run: %v", err)
	}
	if result.Status != domain.StatusProcessing {
		t.Fatalf("статус = %q, ожидался processing", result.Status)
	}
	// Новая заявка не создаётся: кампания и идентификатор заявки те же.
	request, err := h.repo.FindByID(ctx, "req-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if request.ID() != "req-1" {
		t.Fatalf("идентификатор заявки = %q, ожидался req-1", request.ID())
	}
}
