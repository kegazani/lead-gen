package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	pgqueue "lead-gen/backend/contexts/message-delivery/internal/adapters/commandsource/postgres"
	pgstore "lead-gen/backend/contexts/message-delivery/internal/adapters/persistence/postgres"
	appcommand "lead-gen/backend/contexts/message-delivery/internal/app/command"
	"lead-gen/backend/contexts/message-delivery/internal/domain"
	"lead-gen/backend/contexts/message-delivery/internal/ports"
)

type pgTestClock struct{ at time.Time }

func (c pgTestClock) Now() time.Time { return c.at }

type pgTestSuppression struct{}

func (pgTestSuppression) IsSuppressed(context.Context, domain.TenantID, domain.DeliveryAddress) (bool, error) {
	return false, nil
}

type unknownOnceMailer struct{ calls int }

func (m *unknownOnceMailer) Send(context.Context, ports.OutgoingMessage) (ports.ProviderResponse, error) {
	m.calls++
	return ports.ProviderResponse{Outcome: domain.OutcomeUnknown, ProviderCode: "outcome-unknown"}, nil
}

func queueCommand(t *testing.T, commandID, subject string) domain.DeliveryCommand {
	t.Helper()
	address, err := domain.ParseDeliveryAddress("durable@example.com")
	if err != nil {
		t.Fatal(err)
	}
	sender, err := domain.ParseSenderDomain("mail.example.com")
	if err != nil {
		t.Fatal(err)
	}
	return domain.DeliveryCommand{CommandID: domain.CommandID(commandID), TenantID: "queue-tenant",
		ApprovedActionID: domain.ApprovedActionID(commandID), CampaignID: "campaign-" + commandID, RecipientRef: "recipient-1",
		Address: address, SenderDomain: sender, Subject: subject, Body: "payload",
		CorrelationID: "correlation-1", CausationID: "decision-1", RequestedAt: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)}
}

func TestPostgresDeliveryMessageAndOutboxSurviveAdapterRebuild(t *testing.T) {
	truncate(t)
	ctx := context.Background()
	message := newMessage(t, "restart-message", "tenant-1|act-1")
	if err := message.StartDelivery(time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := message.RecordDelivery(domain.OutcomeDelivered, "250", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	firstUnit := pgstore.NewUnitOfWork(testPool(t))
	if err := firstUnit.WithinTransaction(ctx, func(tx ports.Transaction) error {
		if err := tx.Messages().Save(ctx, message); err != nil {
			return err
		}
		return tx.Outbox().Append(ctx, message.DomainEvents()...)
	}); err != nil {
		t.Fatalf("persist message/outbox: %v", err)
	}

	// Новый UoW и журнал моделируют повторную сборку адаптеров после restart.
	secondUnit := pgstore.NewUnitOfWork(testPool(t))
	var restored *domain.Message
	if err := secondUnit.WithinTransaction(ctx, func(tx ports.Transaction) error {
		var err error
		restored, err = tx.Messages().FindByIdempotencyKey(ctx, "tenant-1|act-1")
		return err
	}); err != nil {
		t.Fatalf("reload message: %v", err)
	}
	if restored.ID() != message.ID() || restored.Status() != domain.MessageStatusDelivered {
		t.Fatalf("message после restart изменился: id=%s status=%s", restored.ID(), restored.Status())
	}
	pending, err := pgstore.NewJournal(testPool(t)).Pending(ctx)
	if err != nil {
		t.Fatalf("reload outbox: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("outbox записей после restart=%d, ожидалась 1", len(pending))
	}
}

func TestPostgresReservationReplayDoesNotSendUnknownTwice(t *testing.T) {
	truncate(t)
	command := queueCommand(t, "pg-unknown-1", "Subject")
	uow := pgstore.NewUnitOfWork(testPool(t))
	mailer := &unknownOnceMailer{}
	handler := appcommand.NewDeliverMessageHandler(pgTestSuppression{}, mailer,
		pgTestClock{at: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)}, uow)
	first, err := handler.Handle(context.Background(), command)
	if err != nil {
		t.Fatalf("первичная отправка должна сохранить unknown outcome: %v", err)
	}
	if first.Status != domain.MessageStatusUnknown {
		t.Fatalf("первичный outcome = %+v, ожидался unknown", first)
	}
	result, err := handler.Handle(context.Background(), command)
	if err != nil {
		t.Fatalf("повтор зарезервированной команды: %v", err)
	}
	if !result.Replayed || result.Status != domain.MessageStatusUnknown {
		t.Fatalf("неизвестное состояние не восстановлено: %+v", result)
	}
	if mailer.calls != 1 {
		t.Fatalf("mailer вызван %d раз, ожидался 1", mailer.calls)
	}
	retryables, err := pgstore.NewMessageRepository(testPool(t)).ListRetryable(context.Background(), 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(retryables) != 0 {
		t.Fatalf("sending/unknown попал в автоматический retry: %d", len(retryables))
	}
}

func clearApprovedQueue(t *testing.T) {
	t.Helper()
	if _, err := testPool(t).Exec(context.Background(), `TRUNCATE "message-delivery".approved_action_inbox`); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool(t).Exec(context.Background(), `TRUNCATE "message-delivery".campaign_cancellation`); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresCancellationFencesBeforeActionAndCancelsLeasedItem(t *testing.T) {
	clearApprovedQueue(t)
	defer clearApprovedQueue(t)
	ctx := context.Background()
	queue, err := pgqueue.NewApprovedQueue(testPool(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	cmd := queueCommand(t, "pg-cancel-queue", "Subject")
	if duplicate, err := queue.Enqueue(ctx, cmd); err != nil || duplicate {
		t.Fatalf("enqueue duplicate=%v err=%v", duplicate, err)
	}
	if _, _, ok, err := queue.Claim(ctx); err != nil || !ok {
		t.Fatalf("claim pending item: ok=%v err=%v", ok, err)
	}
	if err := queue.CancelCampaign(ctx, cmd.TenantID.String(), cmd.CampaignID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := queue.Enqueue(ctx, cmd); !errors.Is(err, ports.ErrCampaignCancelled) {
		t.Fatalf("late enqueue error=%v", err)
	}
	var status string
	if err := testPool(t).QueryRow(ctx, `SELECT status FROM "message-delivery".approved_action_inbox WHERE idempotency_key=$1`, cmd.IdempotencyKey()).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "cancelled" {
		t.Fatalf("inbox status=%q, expected cancelled", status)
	}
}

func TestPostgresCancellationDoesNotRewriteUnknownMessage(t *testing.T) {
	clearApprovedQueue(t)
	defer clearApprovedQueue(t)
	ctx := context.Background()
	cmd := queueCommand(t, "pg-cancel-unknown", "Subject")
	uow := pgstore.NewUnitOfWork(testPool(t))
	mailer := &unknownOnceMailer{}
	handler := appcommand.NewDeliverMessageHandler(pgTestSuppression{}, mailer, pgTestClock{at: time.Now().UTC()}, uow)
	first, err := handler.Handle(ctx, cmd)
	if err != nil || first.Status != domain.MessageStatusUnknown {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	if err := uow.CancelCampaign(ctx, cmd.TenantID.String(), cmd.CampaignID); err != nil {
		t.Fatal(err)
	}
	replayed, err := handler.Handle(ctx, cmd)
	if !errors.Is(err, ports.ErrCampaignCancelled) {
		t.Fatalf("replay after cancel error=%v", err)
	}
	if replayed.Status != "" || mailer.calls != 1 {
		t.Fatalf("cancel rewrote/retried unknown: result=%+v calls=%d", replayed, mailer.calls)
	}
	message, err := pgstore.NewMessageRepository(testPool(t)).FindByIdempotencyKey(ctx, cmd.IdempotencyKey())
	if err != nil {
		t.Fatal(err)
	}
	if message.Status() != domain.MessageStatusUnknown {
		t.Fatalf("message status=%s, expected unknown", message.Status())
	}
}

func TestPostgresCancellationBeforeHandlerReservationDoesNotSend(t *testing.T) {
	clearApprovedQueue(t)
	defer clearApprovedQueue(t)
	ctx := context.Background()
	cmd := queueCommand(t, "pg-cancel-before-reservation", "Subject")
	uow := pgstore.NewUnitOfWork(testPool(t))
	if err := uow.CancelCampaign(ctx, cmd.TenantID.String(), cmd.CampaignID); err != nil {
		t.Fatal(err)
	}
	mailer := &unknownOnceMailer{}
	handler := appcommand.NewDeliverMessageHandler(pgTestSuppression{}, mailer, pgTestClock{at: time.Now().UTC()}, uow)
	if _, err := handler.Handle(ctx, cmd); !errors.Is(err, ports.ErrCampaignCancelled) {
		t.Fatalf("handle error=%v", err)
	}
	if mailer.calls != 0 {
		t.Fatalf("mailer вызван до fence: %d", mailer.calls)
	}
}

func TestPostgresApprovedQueueConcurrentClaimAndAck(t *testing.T) {
	clearApprovedQueue(t)
	defer clearApprovedQueue(t)
	queue, err := pgqueue.NewApprovedQueue(testPool(t), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	command := queueCommand(t, "pg-claim-1", "Subject")
	if duplicate, err := queue.Enqueue(context.Background(), command); err != nil || duplicate {
		t.Fatalf("enqueue duplicate=%v err=%v", duplicate, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
	defer cancel()
	type claimResult struct {
		token string
		ok    bool
		err   error
	}
	results := make(chan claimResult, 2)
	for i := 0; i < 2; i++ {
		go func() { _, token, ok, err := queue.Claim(ctx); results <- claimResult{token, ok, err} }()
	}
	first := <-results
	second := <-results
	claims := 0
	var token string
	for _, result := range []claimResult{first, second} {
		if result.ok {
			claims++
			token = result.token
		} else if !errors.Is(result.err, context.DeadlineExceeded) {
			t.Fatalf("неожиданный результат конкурирующего claim: %+v", result)
		}
	}
	if claims != 1 {
		t.Fatalf("эксклюзивных claim = %d, ожидался 1", claims)
	}
	if err := queue.Ack(context.Background(), token); err != nil {
		t.Fatalf("ACK: %v", err)
	}
}

func TestPostgresApprovedQueuePayloadConflictAndRestartRecovery(t *testing.T) {
	clearApprovedQueue(t)
	defer clearApprovedQueue(t)
	queue, err := pgqueue.NewApprovedQueue(testPool(t), 80*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	command := queueCommand(t, "pg-recovery-1", "Subject")
	if duplicate, err := queue.Enqueue(context.Background(), command); err != nil || duplicate {
		t.Fatalf("enqueue duplicate=%v err=%v", duplicate, err)
	}
	if duplicate, err := queue.Enqueue(context.Background(), command); err != nil || !duplicate {
		t.Fatalf("replay duplicate=%v err=%v", duplicate, err)
	}
	conflict := command
	conflict.Subject = "Different subject"
	if _, err := queue.Enqueue(context.Background(), conflict); err == nil {
		t.Fatal("payload conflict принят")
	}
	claimed, _, ok, err := queue.Claim(context.Background())
	if err != nil || !ok {
		t.Fatalf("Claim: ok=%v err=%v", ok, err)
	}
	if claimed.Subject != command.Subject {
		t.Fatalf("payload восстановлен неверно: %+v", claimed)
	}
	time.Sleep(100 * time.Millisecond)
	restarted, err := pgqueue.NewApprovedQueue(testPool(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	recovered, token, ok, err := restarted.Claim(context.Background())
	if err != nil || !ok {
		t.Fatalf("recovery claim: ok=%v err=%v", ok, err)
	}
	if recovered.CommandID != command.CommandID {
		t.Fatalf("восстановилась другая команда: %s", recovered.CommandID)
	}
	if err := restarted.Ack(context.Background(), token); err != nil {
		t.Fatal(err)
	}
}
