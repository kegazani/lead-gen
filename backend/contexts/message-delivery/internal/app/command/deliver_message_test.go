package command

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/message-delivery/internal/domain"
	"lead-gen/backend/contexts/message-delivery/internal/ports"
)

var testInstant = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

func testCommand(t *testing.T) domain.DeliveryCommand {
	t.Helper()
	address, err := domain.ParseDeliveryAddress("lead@example.com")
	if err != nil {
		t.Fatal(err)
	}
	sender, err := domain.ParseSenderDomain("mail.example.com")
	if err != nil {
		t.Fatal(err)
	}
	return domain.DeliveryCommand{
		CommandID:        domain.CommandID("cmd-1"),
		TenantID:         domain.TenantID("tenant-1"),
		ApprovedActionID: domain.ApprovedActionID("act-1"),
		RecipientRef:     domain.RecipientRef("ref-1"),
		Address:          address,
		SenderDomain:     sender,
		CorrelationID:    domain.CorrelationID("act-1"),
	}
}

func newTestHandler(uow *fakeUoW, mailer *fakeMailer, suppression *fakeSuppression) *DeliverMessageHandler {
	return NewDeliverMessageHandler(suppression, mailer, fixedClock{}, uow)
}

func TestDeliverMessage_Delivered(t *testing.T) {
	uow := newFakeUoW()
	mailer := &fakeMailer{response: ports.ProviderResponse{Outcome: domain.OutcomeDelivered, ProviderCode: "250"}}
	handler := newTestHandler(uow, mailer, newFakeSuppression())

	result, err := handler.Handle(context.Background(), testCommand(t))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if result.Status != domain.MessageStatusDelivered || result.AttemptNumber != 1 || result.ProviderCode != "250" {
		t.Fatalf("результат: %+v", result)
	}
	if result.Replayed {
		t.Fatal("первый вызов не является повтором")
	}
	if len(mailer.sent) != 1 {
		t.Fatalf("ожидалась одна отправка, got %d", len(mailer.sent))
	}
	if len(uow.outbox.events) != 1 {
		t.Fatalf("ожидалось одно событие, got %d", len(uow.outbox.events))
	}
	if _, ok := uow.outbox.events[0].(domain.MessageDelivered); !ok {
		t.Fatalf("ожидалось MessageDelivered, got %T", uow.outbox.events[0])
	}
	// Репутация учитывает нормализованный ответ провайдера.
	reputation, err := uow.reputations.FindByDomain(context.Background(), testSender(t))
	if err != nil {
		t.Fatalf("репутация: %v", err)
	}
	if reputation.DeliveredCount() != 1 {
		t.Fatalf("счётчик доставок: %d", reputation.DeliveredCount())
	}
}

func TestDeliverMessage_Failure(t *testing.T) {
	uow := newFakeUoW()
	mailer := &fakeMailer{response: ports.ProviderResponse{Outcome: domain.OutcomeHardBounce, ProviderCode: "550"}}
	handler := newTestHandler(uow, mailer, newFakeSuppression())

	result, err := handler.Handle(context.Background(), testCommand(t))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if result.Status != domain.MessageStatusFailed {
		t.Fatalf("статус: %q", result.Status)
	}
	if _, ok := uow.outbox.events[0].(domain.MessageDeliveryFailed); !ok {
		t.Fatalf("ожидалось MessageDeliveryFailed, got %T", uow.outbox.events[0])
	}
	reputation, err := uow.reputations.FindByDomain(context.Background(), testSender(t))
	if err != nil {
		t.Fatal(err)
	}
	if reputation.HardBounceCount() != 1 {
		t.Fatalf("счётчик жёстких отказов: %d", reputation.HardBounceCount())
	}
}

func TestDeliverMessage_MailerUnavailableRecordsTemporary(t *testing.T) {
	uow := newFakeUoW()
	mailer := &fakeMailer{err: ports.ErrMailerUnavailable}
	handler := newTestHandler(uow, mailer, newFakeSuppression())

	result, err := handler.Handle(context.Background(), testCommand(t))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if result.Status != domain.MessageStatusFailed || result.ProviderCode != "mailer-unavailable" {
		t.Fatalf("результат: %+v", result)
	}
	event, ok := uow.outbox.events[0].(domain.MessageDeliveryFailed)
	if !ok || event.Outcome != domain.OutcomeTemporary {
		t.Fatalf("ожидался временный отказ, got %#v", uow.outbox.events[0])
	}
	// Сбой транспорта не является ответом провайдера и не меняет репутацию.
	if _, err := uow.reputations.FindByDomain(context.Background(), testSender(t)); !errors.Is(err, ports.ErrSenderReputationNotFound) {
		t.Fatalf("репутация не должна меняться при сбое транспорта, got %v", err)
	}
}

func TestDeliverMessage_UnknownExternalOutcomeIsNotBlindlyRetried(t *testing.T) {
	uow := newFakeUoW()
	mailer := &fakeMailer{err: ports.ErrMailerOutcomeUnknown}
	handler := newTestHandler(uow, mailer, newFakeSuppression())
	cmd := testCommand(t)

	if _, err := handler.Handle(context.Background(), cmd); !errors.Is(err, ports.ErrMailerOutcomeUnknown) {
		t.Fatalf("ожидался неизвестный исход, получено %v", err)
	}
	if len(mailer.sent) != 1 {
		t.Fatalf("первичный вызов mailer = %d, ожидался 1", len(mailer.sent))
	}
	replayed, err := handler.Handle(context.Background(), cmd)
	if err != nil {
		t.Fatalf("повтор команды: %v", err)
	}
	if !replayed.Replayed || replayed.Status != domain.MessageStatusSending {
		t.Fatalf("неизвестный исход должен оставаться зарезервированным: %+v", replayed)
	}
	if len(mailer.sent) != 1 {
		t.Fatalf("повтор неизвестного исхода отправил дубль: вызовов=%d", len(mailer.sent))
	}
}

func TestDeliverMessage_IdempotentReplay(t *testing.T) {
	uow := newFakeUoW()
	mailer := &fakeMailer{response: ports.ProviderResponse{Outcome: domain.OutcomeDelivered, ProviderCode: "250"}}
	handler := newTestHandler(uow, mailer, newFakeSuppression())
	ctx := context.Background()
	cmd := testCommand(t)

	first, err := handler.Handle(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	second, err := handler.Handle(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if first.Replayed || !second.Replayed {
		t.Fatalf("признак повтора: first=%v second=%v", first.Replayed, second.Replayed)
	}
	if first.MessageID != second.MessageID {
		t.Fatalf("повтор должен вернуть то же сообщение: %s vs %s", first.MessageID, second.MessageID)
	}
	if len(mailer.sent) != 1 {
		t.Fatalf("повтор не должен отправлять письмо второй раз, got %d отправок", len(mailer.sent))
	}
	if len(uow.outbox.events) != 1 {
		t.Fatalf("повтор не должен создавать второе событие, got %d", len(uow.outbox.events))
	}
}

func TestDeliverMessage_SuspendedSenderRejected(t *testing.T) {
	uow := newFakeUoW()
	reputation, err := domain.NewSenderReputation(testSender(t), testInstant)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := reputation.ApplyNormalizedOutcome(domain.NormalizedOutcome{Outcome: domain.OutcomeHardBounce}, testInstant); err != nil {
			t.Fatal(err)
		}
	}
	if err := uow.reputations.Save(context.Background(), reputation); err != nil {
		t.Fatal(err)
	}

	mailer := &fakeMailer{response: ports.ProviderResponse{Outcome: domain.OutcomeDelivered}}
	handler := newTestHandler(uow, mailer, newFakeSuppression())

	if _, err := handler.Handle(context.Background(), testCommand(t)); !errors.Is(err, domain.ErrSenderSuspended) {
		t.Fatalf("ожидался отказ по репутации, got %v", err)
	}
	if len(mailer.sent) != 0 {
		t.Fatal("приостановленный домен не должен отправлять")
	}
}

func TestDeliverMessage_SuppressedRecipientRejected(t *testing.T) {
	uow := newFakeUoW()
	suppression := newFakeSuppression()
	suppression.suppressed[testCommand(t).Address] = struct{}{}
	mailer := &fakeMailer{response: ports.ProviderResponse{Outcome: domain.OutcomeDelivered}}
	handler := newTestHandler(uow, mailer, suppression)

	if _, err := handler.Handle(context.Background(), testCommand(t)); !errors.Is(err, domain.ErrRecipientSuppressed) {
		t.Fatalf("ожидался отказ по стоп-листу, got %v", err)
	}
	if len(mailer.sent) != 0 {
		t.Fatal("стоп-лист должен блокировать отправку")
	}
	if len(uow.outbox.events) != 0 {
		t.Fatal("заблокированная отправка не создаёт событий")
	}
}

func TestDeliverMessage_RequiresApprovedAction(t *testing.T) {
	uow := newFakeUoW()
	handler := newTestHandler(uow, &fakeMailer{}, newFakeSuppression())
	cmd := testCommand(t)
	cmd.ApprovedActionID = ""
	if _, err := handler.Handle(context.Background(), cmd); !errors.Is(err, domain.ErrApprovedActionRequired) {
		t.Fatalf("ожидался отказ политики approved-action-only, got %v", err)
	}
}

func testSender(t *testing.T) domain.SenderDomain {
	t.Helper()
	sender, err := domain.ParseSenderDomain("mail.example.com")
	if err != nil {
		t.Fatal(err)
	}
	return sender
}
