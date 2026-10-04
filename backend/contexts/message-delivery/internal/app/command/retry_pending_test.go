package command

import (
	"context"
	"testing"

	"lead-gen/backend/contexts/message-delivery/internal/domain"
	"lead-gen/backend/contexts/message-delivery/internal/ports"
)

// seedTemporarilyFailed создаёт в хранилище сообщение, последняя попытка
// которого завершилась временным исходом (недоступность провайдера).
func seedTemporarilyFailed(t *testing.T, uow *fakeUoW, cmd domain.DeliveryCommand) *domain.Message {
	t.Helper()
	now := fixedClock{}.Now()
	message, err := domain.NewMessage(domain.MessageParams{
		ID:               domain.MessageID(cmd.CommandID),
		TenantID:         cmd.TenantID,
		ApprovedActionID: cmd.ApprovedActionID,
		RecipientRef:     cmd.RecipientRef,
		Address:          cmd.Address,
		SenderDomain:     cmd.SenderDomain,
		Subject:          cmd.Subject,
		Body:             cmd.Body,
		IdempotencyKey:   cmd.IdempotencyKey(),
		CreatedAt:        now,
	})
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
	if err := message.StartDelivery(now); err != nil {
		t.Fatalf("StartDelivery: %v", err)
	}
	if err := message.RecordDelivery(domain.OutcomeTemporary, "mailer-unavailable", now); err != nil {
		t.Fatalf("RecordDelivery: %v", err)
	}
	message.PullDomainEvents()
	if err := uow.messages.Save(context.Background(), message); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return message
}

func TestRetryPending_DeliversOnRetry(t *testing.T) {
	uow := newFakeUoW()
	cmd := testCommand(t)
	cmd.Subject = "Тема письма"
	cmd.Body = "Тело письма"
	seedTemporarilyFailed(t, uow, cmd)

	mailer := &fakeMailer{response: ports.ProviderResponse{Outcome: domain.OutcomeDelivered, ProviderCode: "250"}}
	handler := NewRetryPendingHandler(uow.messages, mailer, fixedClock{}, uow, 3)

	summary, err := handler.RetryDue(context.Background(), 10)
	if err != nil {
		t.Fatalf("RetryDue: %v", err)
	}
	if summary.Attempted != 1 || summary.Delivered != 1 || summary.Failed != 0 {
		t.Fatalf("итог прохода: %+v", summary)
	}
	if len(mailer.sent) != 1 {
		t.Fatalf("ожидалась одна повторная отправка, got %d", len(mailer.sent))
	}
	if mailer.sent[0].Subject != "Тема письма" || mailer.sent[0].Body != "Тело письма" {
		t.Fatalf("повтор должен отправлять сохранённое содержимое: %+v", mailer.sent[0])
	}

	updated, err := uow.messages.FindByID(context.Background(), domain.MessageID(cmd.CommandID))
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if updated.Status() != domain.MessageStatusDelivered {
		t.Fatalf("состояние после повтора: %q", updated.Status())
	}
	if len(updated.Attempts()) != 2 || updated.Attempts()[1].Number != 2 {
		t.Fatalf("ожидалась вторая попытка: %+v", updated.Attempts())
	}
	if len(uow.outbox.events) != 1 {
		t.Fatalf("ожидалось одно событие после повтора, got %d", len(uow.outbox.events))
	}
	if _, ok := uow.outbox.events[0].(domain.MessageDelivered); !ok {
		t.Fatalf("ожидалось MessageDelivered, got %T", uow.outbox.events[0])
	}
}

func TestRetryPending_StopsAtAttemptLimit(t *testing.T) {
	uow := newFakeUoW()
	cmd := testCommand(t)
	seedTemporarilyFailed(t, uow, cmd)

	mailer := &fakeMailer{response: ports.ProviderResponse{Outcome: domain.OutcomeDelivered}}
	handler := NewRetryPendingHandler(uow.messages, mailer, fixedClock{}, uow, 1)

	summary, err := handler.RetryDue(context.Background(), 10)
	if err != nil {
		t.Fatalf("RetryDue: %v", err)
	}
	if summary.Attempted != 0 {
		t.Fatalf("исчерпанный лимит попыток не должен приводить к повтору: %+v", summary)
	}
	if len(mailer.sent) != 0 {
		t.Fatal("повтор не должен выполняться после исчерпания лимита")
	}
}

func TestRetryPending_Disabled(t *testing.T) {
	uow := newFakeUoW()
	seedTemporarilyFailed(t, uow, testCommand(t))

	mailer := &fakeMailer{response: ports.ProviderResponse{Outcome: domain.OutcomeDelivered}}
	handler := NewRetryPendingHandler(uow.messages, mailer, fixedClock{}, uow, 0)

	summary, err := handler.RetryDue(context.Background(), 10)
	if err != nil {
		t.Fatalf("RetryDue: %v", err)
	}
	if summary.Attempted != 0 || len(mailer.sent) != 0 {
		t.Fatalf("отключённые повторные окна не должны отправлять: %+v", summary)
	}
}

func TestRetryPending_SkipsDeliveredMessage(t *testing.T) {
	uow := newFakeUoW()
	cmd := testCommand(t)
	now := fixedClock{}.Now()
	message, err := domain.NewMessage(domain.MessageParams{
		ID:               domain.MessageID(cmd.CommandID),
		TenantID:         cmd.TenantID,
		ApprovedActionID: cmd.ApprovedActionID,
		RecipientRef:     cmd.RecipientRef,
		Address:          cmd.Address,
		SenderDomain:     cmd.SenderDomain,
		IdempotencyKey:   cmd.IdempotencyKey(),
		CreatedAt:        now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := message.RecordDelivery(domain.OutcomeDelivered, "250", now); err != nil {
		t.Fatal(err)
	}
	message.PullDomainEvents()
	if err := uow.messages.Save(context.Background(), message); err != nil {
		t.Fatal(err)
	}

	mailer := &fakeMailer{response: ports.ProviderResponse{Outcome: domain.OutcomeDelivered}}
	handler := NewRetryPendingHandler(uow.messages, mailer, fixedClock{}, uow, 3)
	summary, err := handler.RetryDue(context.Background(), 10)
	if err != nil {
		t.Fatalf("RetryDue: %v", err)
	}
	if summary.Attempted != 0 || len(mailer.sent) != 0 {
		t.Fatalf("доставленное сообщение не подлежит повтору: %+v", summary)
	}
}
