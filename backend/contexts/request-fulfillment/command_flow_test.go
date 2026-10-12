package requestfulfillment_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"lead-gen/backend/contexts/request-fulfillment/internal/adapters/outbox/inmemory"
	persistence "lead-gen/backend/contexts/request-fulfillment/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/command"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
)

var epoch = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// fixedClock — часы с фиксированным временем.
type fixedClock struct{ at time.Time }

func (c fixedClock) Now() time.Time { return c.at }

// seqIDs — детерминированный генератор идентификаторов событий.
type seqIDs struct{ n int }

func (s *seqIDs) NewEventID() domain.EventID {
	s.n++
	return domain.EventID(fmt.Sprintf("evt-%d", s.n))
}

// harness собирает сценарии поверх общего репозитория и журнала outbox.
type harness struct {
	uow       *inmemory.UnitOfWork
	repo      *persistence.Repository
	journal   *inmemory.TransactionalJournal
	clock     fixedClock
	ids       *seqIDs
	submit    *command.SubmitRequestHandler
	startVal  *command.StartValidationHandler
	validate  *command.ValidateRequestHandler
	startProc *command.StartProcessingHandler
	fulfill   *command.FulfillRequestHandler
	reject    *command.RejectRequestHandler
	cancel    *command.CancelRequestHandler
}

func newHarness() *harness {
	repo := persistence.New()
	journal := inmemory.NewTransactionalJournal()
	uow := inmemory.NewUnitOfWorkWith(repo, journal)
	clock := fixedClock{at: epoch}
	ids := &seqIDs{}
	return &harness{
		uow:       uow,
		repo:      repo,
		journal:   journal,
		clock:     clock,
		ids:       ids,
		submit:    command.NewSubmitRequestHandler(uow, clock, ids),
		startVal:  command.NewStartValidationHandler(uow, clock, ids),
		validate:  command.NewValidateRequestHandler(uow, clock, ids),
		startProc: command.NewStartProcessingHandler(uow, clock, ids),
		fulfill:   command.NewFulfillRequestHandler(uow, clock, ids),
		reject:    command.NewRejectRequestHandler(uow, clock, ids),
		cancel:    command.NewCancelRequestHandler(uow, clock, ids),
	}
}

// submit выполняет подачу заявки с непустым оффером.
func (h *harness) submitOK(t *testing.T) command.SubmitRequestResult {
	t.Helper()
	result, err := h.submit.Handle(context.Background(), command.SubmitRequest{
		RequestID: "req-1", OfferID: "offer-1", TenantID: "tenant-1",
		Title:   "Заявка автоматизации найма",
		Content: "Предлагаем автоматизацию найма", Target: "IT-компании",
		Actor: "actor-1", CorrelationID: "corr-1", CausationID: "cause-1",
	})
	if err != nil {
		t.Fatalf("SubmitRequest: %v", err)
	}
	return result
}

// fields собирает общую часть команды перехода.
func fields() command.LifecycleFields {
	return command.LifecycleFields{
		RequestID: "req-1", TenantID: "tenant-1", Actor: "actor-1",
		CorrelationID: "corr-1", CausationID: "cause-1",
	}
}

// pending возвращает ожидающие публикации записи outbox.
func (h *harness) pending(t *testing.T) []platformoutbox.Message {
	t.Helper()
	messages, err := h.journal.Pending(context.Background(), 0)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	return messages
}

// TestLifecyclePublishesEvents — сценарии спецификации повторяются командами:
// подача и переходы публикуют события заявки в outbox.
func TestLifecyclePublishesEvents(t *testing.T) {
	h := newHarness()
	ctx := context.Background()

	if result := h.submitOK(t); result.Status != domain.StatusSubmitted || result.Version != 1 {
		t.Fatalf("после submit: status=%q version=%d", result.Status, result.Version)
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
	result, err := h.fulfill.Handle(ctx, command.FulfillRequest{LifecycleFields: fields()})
	if err != nil {
		t.Fatalf("FulfillRequest: %v", err)
	}
	if result.Status != domain.StatusFulfilled {
		t.Fatalf("статус = %q, ожидался fulfilled", result.Status)
	}

	names := eventNames(h.pending(t))
	want := []string{
		"request-submitted", "request-validated",
		"request-processing-started", "request-fulfilled",
	}
	if len(names) != len(want) {
		t.Fatalf("событий в outbox = %v, ожидались %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("событие %d = %q, ожидалось %q", i, names[i], want[i])
		}
	}
}

// TestEventPublicationIsIdempotent — публикация идемпотентна по event_id:
// повторная запись той же записи не создаёт вторую.
func TestEventPublicationIsIdempotent(t *testing.T) {
	journal := platformoutbox.NewMemoryJournal()
	message := platformoutbox.Message{ID: "evt-1", Exchange: "request-fulfillment", RoutingKey: "request-submitted.v1", Body: []byte("{}")}
	ctx := context.Background()
	if err := journal.Append(ctx, message); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := journal.Append(ctx, message); err != nil {
		t.Fatalf("повторный Append: %v", err)
	}
	messages, err := journal.Pending(ctx, 0)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("записей = %d, ожидалась 1", len(messages))
	}
}

// TestSubmitRejectsEmptyOffer — сценарий «Пустой оффер»: заявка не создаётся и
// событие не публикуется.
func TestSubmitRejectsEmptyOffer(t *testing.T) {
	h := newHarness()
	_, err := h.submit.Handle(context.Background(), command.SubmitRequest{
		RequestID: "req-1", OfferID: "offer-1", TenantID: "tenant-1",
		Title:   "Пустой оффер",
		Content: "   ", Actor: "actor-1",
	})
	if !errors.Is(err, domain.ErrOfferRequired) {
		t.Fatalf("ошибка = %v, ожидалась ErrOfferRequired", err)
	}
	if len(h.pending(t)) != 0 {
		t.Fatalf("отвергнутая заявка опубликовала событие")
	}
	if _, err := h.repo.FindByID(context.Background(), "req-1"); !errors.Is(err, ports.ErrRequestNotFound) {
		t.Fatalf("заявка сохранена при пустом оффере: err=%v", err)
	}
}

// TestStartProcessingWithoutValidation — сценарий «Запуск без валидации»:
// запрещённый переход не меняет состояние и не публикует событие.
func TestStartProcessingWithoutValidation(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	h.submitOK(t)
	before := len(h.pending(t))

	_, err := h.startProc.Handle(ctx, command.StartProcessing{LifecycleFields: fields()})
	if !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("ошибка = %v, ожидалась ErrInvalidTransition", err)
	}
	request, err := h.repo.FindByID(ctx, "req-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if request.Status() != domain.StatusSubmitted {
		t.Fatalf("статус = %q, ожидался submitted", request.Status())
	}
	if got := len(h.pending(t)); got != before {
		t.Fatalf("запрещённый переход опубликовал событие: %d → %d", before, got)
	}
}

// TestRejectFlow — отклонение из validating требует причины и публикует
// request-rejected.
func TestRejectFlow(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	h.submitOK(t)
	if _, err := h.startVal.Handle(ctx, command.StartValidation{LifecycleFields: fields()}); err != nil {
		t.Fatalf("StartValidation: %v", err)
	}
	if _, err := h.reject.Handle(ctx, command.RejectRequest{LifecycleFields: fields()}); !errors.Is(err, domain.ErrReasonRequired) {
		t.Fatalf("без причины: ошибка = %v", err)
	}
	result, err := h.reject.Handle(ctx, command.RejectRequest{LifecycleFields: fields(), Reason: "не наш профиль"})
	if err != nil {
		t.Fatalf("RejectRequest: %v", err)
	}
	if result.Status != domain.StatusRejected {
		t.Fatalf("статус = %q", result.Status)
	}
	names := eventNames(h.pending(t))
	if names[len(names)-1] != "request-rejected" {
		t.Fatalf("последнее событие = %q", names[len(names)-1])
	}
}

// TestHistoryPreservesActorAndTime — фиксация истории: переход сохраняет время и
// актора.
func TestHistoryPreservesActorAndTime(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	h.submitOK(t)
	if _, err := h.startVal.Handle(ctx, command.StartValidation{LifecycleFields: fields()}); err != nil {
		t.Fatalf("StartValidation: %v", err)
	}
	request, err := h.repo.FindByID(ctx, "req-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	transitions := request.Transitions()
	last := transitions[len(transitions)-1]
	if last.To() != domain.StatusValidating || last.Actor() != "actor-1" {
		t.Fatalf("переход = %+v", last)
	}
	if !last.At().Equal(epoch) {
		t.Fatalf("время перехода = %v, ожидалось %v", last.At(), epoch)
	}
}

// TestTenantMismatch — обращение к чужой заявке отвергается.
func TestTenantMismatch(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	h.submitOK(t)
	other := fields()
	other.TenantID = "tenant-2"
	if _, err := h.startVal.Handle(ctx, command.StartValidation{LifecycleFields: other}); !errors.Is(err, command.ErrTenantMismatch) {
		t.Fatalf("ошибка = %v, ожидалась ErrTenantMismatch", err)
	}
}

// eventNames извлекает имена событий из заголовков записей outbox.
func eventNames(messages []platformoutbox.Message) []string {
	out := make([]string, 0, len(messages))
	for _, message := range messages {
		out = append(out, message.Headers["event_name"])
	}
	return out
}
