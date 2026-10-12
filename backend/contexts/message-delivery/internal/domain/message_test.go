package domain

import (
	"errors"
	"testing"
	"time"
)

var testTime = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

func mustAddress(t *testing.T, raw string) DeliveryAddress {
	t.Helper()
	address, err := ParseDeliveryAddress(raw)
	if err != nil {
		t.Fatalf("адрес %q: %v", raw, err)
	}
	return address
}

func mustSender(t *testing.T, raw string) SenderDomain {
	t.Helper()
	sender, err := ParseSenderDomain(raw)
	if err != nil {
		t.Fatalf("домен %q: %v", raw, err)
	}
	return sender
}

func validMessageParams(t *testing.T) MessageParams {
	t.Helper()
	return MessageParams{
		ID:               MessageID("msg-1"),
		TenantID:         TenantID("tenant-1"),
		ApprovedActionID: ApprovedActionID("act-1"),
		RecipientRef:     RecipientRef("recipient-ref-1"),
		Address:          mustAddress(t, "lead@example.com"),
		SenderDomain:     mustSender(t, "mail.example.com"),
		IdempotencyKey:   "tenant-1|act-1",
		CreatedAt:        testTime,
	}
}

func TestNewMessage_ValidatesRequiredFields(t *testing.T) {
	if _, err := NewMessage(validMessageParams(t)); err != nil {
		t.Fatalf("корректные параметры отвергнуты: %v", err)
	}

	noAction := validMessageParams(t)
	noAction.ApprovedActionID = ""
	if _, err := NewMessage(noAction); !errors.Is(err, ErrEmptyID) {
		t.Fatalf("без утверждённого действия: got %v", err)
	}

	noKey := validMessageParams(t)
	noKey.IdempotencyKey = "  "
	if _, err := NewMessage(noKey); !errors.Is(err, ErrIdempotencyKeyRequired) {
		t.Fatalf("без ключа идемпотентности: got %v", err)
	}

	noTime := validMessageParams(t)
	noTime.CreatedAt = time.Time{}
	if _, err := NewMessage(noTime); !errors.Is(err, ErrAttemptTimeRequired) {
		t.Fatalf("без времени создания: got %v", err)
	}
}

func TestMessage_DeliveredLifecycle(t *testing.T) {
	message, err := NewMessage(validMessageParams(t))
	if err != nil {
		t.Fatal(err)
	}
	if message.Status() != MessageStatusPending {
		t.Fatalf("начальное состояние: got %q", message.Status())
	}
	if err := message.StartDelivery(testTime); err != nil {
		t.Fatalf("StartDelivery: %v", err)
	}
	if message.Status() != MessageStatusSending {
		t.Fatalf("после старта: got %q", message.Status())
	}
	if err := message.RecordDelivery(OutcomeDelivered, "250", testTime.Add(time.Second)); err != nil {
		t.Fatalf("RecordDelivery: %v", err)
	}
	if message.Status() != MessageStatusDelivered {
		t.Fatalf("после доставки: got %q", message.Status())
	}

	events := message.PullDomainEvents()
	if len(events) != 1 {
		t.Fatalf("ожидалось одно событие, got %d", len(events))
	}
	delivered, ok := events[0].(MessageDelivered)
	if !ok {
		t.Fatalf("ожидалось MessageDelivered, got %T", events[0])
	}
	if delivered.AttemptNumber != 1 || delivered.ProviderCode != "250" {
		t.Fatalf("поля события: %+v", delivered)
	}
	if len(message.DomainEvents()) != 0 {
		t.Fatal("после PullDomainEvents буфер должен быть пуст")
	}
}

func TestMessage_FailedLifecycle(t *testing.T) {
	message, err := NewMessage(validMessageParams(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := message.StartDelivery(testTime); err != nil {
		t.Fatal(err)
	}
	if err := message.RecordDelivery(OutcomeHardBounce, "550", testTime); err != nil {
		t.Fatal(err)
	}
	if message.Status() != MessageStatusFailed {
		t.Fatalf("после отказа: got %q", message.Status())
	}
	events := message.PullDomainEvents()
	if len(events) != 1 {
		t.Fatalf("ожидалось одно событие, got %d", len(events))
	}
	failed, ok := events[0].(MessageDeliveryFailed)
	if !ok {
		t.Fatalf("ожидалось MessageDeliveryFailed, got %T", events[0])
	}
	if failed.Outcome != OutcomeHardBounce {
		t.Fatalf("исход события: %q", failed.Outcome)
	}
}

func TestMessage_RejectsTransitions(t *testing.T) {
	message, err := NewMessage(validMessageParams(t))
	if err != nil {
		t.Fatal(err)
	}
	// Из pending нельзя записать исход без старта? Запись исхода из pending
	// допустима (попытка могла не успеть перейти в sending), поэтому проверяем
	// запрет повторной записи после терминального состояния.
	if err := message.RecordDelivery(OutcomeDelivered, "250", testTime); err != nil {
		t.Fatal(err)
	}
	if err := message.RecordDelivery(OutcomeDelivered, "250", testTime); !errors.Is(err, ErrInvalidMessageTransition) {
		t.Fatalf("повторная запись после терминального состояния: got %v", err)
	}

	other, err := NewMessage(validMessageParams(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := other.RecordDelivery(OutcomeDelivered, "", testTime); err != nil {
		t.Fatal(err)
	}
	if err := other.StartDelivery(testTime); !errors.Is(err, ErrInvalidMessageTransition) {
		t.Fatalf("старт терминального сообщения: got %v", err)
	}
}

func TestMessage_CampaignFlowsToSnapshotAndEvent(t *testing.T) {
	params := validMessageParams(t)
	params.CampaignID = "handoff-req-7"
	message, err := NewMessage(params)
	if err != nil {
		t.Fatal(err)
	}
	if message.CampaignID() != "handoff-req-7" {
		t.Fatalf("кампания не сохранена в агрегате: %q", message.CampaignID())
	}
	if err := message.RecordDelivery(OutcomeDelivered, "250", testTime); err != nil {
		t.Fatal(err)
	}
	events := message.DomainEvents()
	if len(events) != 1 {
		t.Fatalf("ожидалось одно событие, got %d", len(events))
	}
	delivered, ok := events[0].(MessageDelivered)
	if !ok {
		t.Fatalf("неожиданное событие %T", events[0])
	}
	if delivered.CampaignID != "handoff-req-7" {
		t.Fatalf("событие не несёт кампанию: %q", delivered.CampaignID)
	}
	restored, err := RehydrateMessage(message.Snapshot())
	if err != nil {
		t.Fatalf("RehydrateMessage: %v", err)
	}
	if restored.CampaignID() != "handoff-req-7" {
		t.Fatalf("снимок не сохранил кампанию: %q", restored.CampaignID())
	}
}

func TestMessage_UnknownOutcomeIsDurableAndEmitsNoDeliveryEvent(t *testing.T) {
	message, err := NewMessage(validMessageParams(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := message.StartDelivery(testTime); err != nil {
		t.Fatal(err)
	}
	if err := message.RecordDelivery(OutcomeUnknown, "outcome-unknown", testTime); err != nil {
		t.Fatal(err)
	}
	if message.Status() != MessageStatusUnknown || message.IsRetryable() {
		t.Fatalf("неизвестный исход должен оставаться отдельным неретраибельным статусом: %q", message.Status())
	}
	if events := message.DomainEvents(); len(events) != 0 {
		t.Fatalf("unknown не должен публиковать событие завершения delivery: %#v", events)
	}
	restored, err := RehydrateMessage(message.Snapshot())
	if err != nil {
		t.Fatalf("RehydrateMessage: %v", err)
	}
	if restored.Status() != MessageStatusUnknown || restored.IsRetryable() {
		t.Fatalf("unknown не сохранился через snapshot: status=%q retryable=%v", restored.Status(), restored.IsRetryable())
	}
	if outcome, ok := restored.LastOutcome(); !ok || outcome != OutcomeUnknown {
		t.Fatalf("исход попытки не сохранён: %q, %v", outcome, ok)
	}
}

func TestMessage_SnapshotRoundTrip(t *testing.T) {
	message, err := NewMessage(validMessageParams(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := message.StartDelivery(testTime); err != nil {
		t.Fatal(err)
	}
	if err := message.RecordDelivery(OutcomeSoftBounce, "421", testTime.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	message.PullDomainEvents()

	restored, err := RehydrateMessage(message.Snapshot())
	if err != nil {
		t.Fatalf("RehydrateMessage: %v", err)
	}
	if restored.Status() != message.Status() {
		t.Fatalf("состояние: got %q, want %q", restored.Status(), message.Status())
	}
	if len(restored.Attempts()) != 1 || restored.Attempts()[0].Number != 1 {
		t.Fatalf("попытки не восстановлены: %+v", restored.Attempts())
	}
	if len(restored.DomainEvents()) != 0 {
		t.Fatal("восстановление не должно эмитить события")
	}
}

func TestRehydrateMessage_RejectsTerminalWithoutAttempts(t *testing.T) {
	params := validMessageParams(t)
	message, err := NewMessage(params)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := message.Snapshot()
	snapshot.Status = MessageStatusDelivered
	if _, err := RehydrateMessage(snapshot); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("терминальное сообщение без попыток: got %v", err)
	}
}

func TestMessage_ContentRoundTrip(t *testing.T) {
	params := validMessageParams(t)
	params.Subject = "Тема письма"
	params.Body = "Тело письма"
	message, err := NewMessage(params)
	if err != nil {
		t.Fatal(err)
	}
	if message.Subject() != "Тема письма" || message.Body() != "Тело письма" {
		t.Fatalf("содержимое агрегата: %q / %q", message.Subject(), message.Body())
	}
	restored, err := RehydrateMessage(message.Snapshot())
	if err != nil {
		t.Fatalf("RehydrateMessage: %v", err)
	}
	if restored.Subject() != "Тема письма" || restored.Body() != "Тело письма" {
		t.Fatalf("содержимое не восстановлено: %q / %q", restored.Subject(), restored.Body())
	}
}

func TestMessage_RetryableAfterTemporary(t *testing.T) {
	message, err := NewMessage(validMessageParams(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := message.StartDelivery(testTime); err != nil {
		t.Fatal(err)
	}
	if err := message.RecordDelivery(OutcomeTemporary, "mailer-unavailable", testTime); err != nil {
		t.Fatal(err)
	}
	message.PullDomainEvents()
	if !message.IsRetryable() {
		t.Fatal("сообщение с временным исходом должно быть доступно повтору")
	}
	if err := message.ReopenForRetry(testTime.Add(time.Minute)); err != nil {
		t.Fatalf("ReopenForRetry: %v", err)
	}
	if message.Status() != MessageStatusSending {
		t.Fatalf("состояние после повтора: %q", message.Status())
	}
	if err := message.RecordDelivery(OutcomeDelivered, "250", testTime.Add(2*time.Minute)); err != nil {
		t.Fatalf("RecordDelivery: %v", err)
	}
	attempts := message.Attempts()
	if len(attempts) != 2 || attempts[1].Number != 2 {
		t.Fatalf("журнал попыток: %+v", attempts)
	}
}

func TestMessage_NotRetryableAfterProviderVerdict(t *testing.T) {
	message, err := NewMessage(validMessageParams(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := message.RecordDelivery(OutcomeHardBounce, "550", testTime); err != nil {
		t.Fatal(err)
	}
	if message.IsRetryable() {
		t.Fatal("жёсткий отказ провайдера не подлежит повтору")
	}
	if err := message.ReopenForRetry(testTime); !errors.Is(err, ErrMessageNotRetryable) {
		t.Fatalf("ожидалась ErrMessageNotRetryable, got %v", err)
	}
}
