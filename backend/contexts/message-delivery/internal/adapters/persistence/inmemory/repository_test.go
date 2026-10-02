package inmemory

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/message-delivery/internal/domain"
	"lead-gen/backend/contexts/message-delivery/internal/ports"
)

var repoInstant = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

func testMessage(t *testing.T, id, key string) *domain.Message {
	t.Helper()
	address, err := domain.ParseDeliveryAddress("lead@example.com")
	if err != nil {
		t.Fatal(err)
	}
	sender, err := domain.ParseSenderDomain("mail.example.com")
	if err != nil {
		t.Fatal(err)
	}
	message, err := domain.NewMessage(domain.MessageParams{
		ID:               domain.MessageID(id),
		TenantID:         domain.TenantID("tenant-1"),
		ApprovedActionID: domain.ApprovedActionID("act-1"),
		RecipientRef:     domain.RecipientRef("ref-1"),
		Address:          address,
		SenderDomain:     sender,
		IdempotencyKey:   key,
		CreatedAt:        repoInstant,
	})
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func TestMessages_SaveAndFind(t *testing.T) {
	repository := NewMessages()
	ctx := context.Background()
	message := testMessage(t, "msg-1", "tenant-1|act-1")
	if err := repository.Save(ctx, message); err != nil {
		t.Fatal(err)
	}

	byID, err := repository.FindByID(ctx, "msg-1")
	if err != nil {
		t.Fatal(err)
	}
	if byID.IdempotencyKey() != "tenant-1|act-1" {
		t.Fatalf("ключ: %q", byID.IdempotencyKey())
	}

	byKey, err := repository.FindByIdempotencyKey(ctx, "tenant-1|act-1")
	if err != nil {
		t.Fatal(err)
	}
	if byKey.ID() != "msg-1" {
		t.Fatalf("id: %q", byKey.ID())
	}
}

func TestMessages_NotFound(t *testing.T) {
	repository := NewMessages()
	if _, err := repository.FindByID(context.Background(), "nope"); !errors.Is(err, ports.ErrMessageNotFound) {
		t.Fatalf("FindByID: got %v", err)
	}
	if _, err := repository.FindByIdempotencyKey(context.Background(), "nope"); !errors.Is(err, ports.ErrMessageNotFound) {
		t.Fatalf("FindByIdempotencyKey: got %v", err)
	}
}

func TestMessages_RejectsDuplicateKeyForOtherMessage(t *testing.T) {
	repository := NewMessages()
	ctx := context.Background()
	if err := repository.Save(ctx, testMessage(t, "msg-1", "tenant-1|act-1")); err != nil {
		t.Fatal(err)
	}
	if err := repository.Save(ctx, testMessage(t, "msg-2", "tenant-1|act-1")); err == nil {
		t.Fatal("ключ идемпотентности должен быть уникален")
	}
}

func TestMessages_CloneIsolation(t *testing.T) {
	repository := NewMessages()
	ctx := context.Background()
	clone := repository.Clone()
	if err := clone.Save(ctx, testMessage(t, "msg-1", "tenant-1|act-1")); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.FindByID(ctx, "msg-1"); !errors.Is(err, ports.ErrMessageNotFound) {
		t.Fatalf("клон не должен влиять на исходный репозиторий: %v", err)
	}
	repository.ReplaceWith(clone)
	if _, err := repository.FindByID(ctx, "msg-1"); err != nil {
		t.Fatalf("после ReplaceWith сообщение должно быть видно: %v", err)
	}
}

func TestReputations_SaveFind(t *testing.T) {
	repository := NewReputations()
	ctx := context.Background()
	sender, err := domain.ParseSenderDomain("mail.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.FindByDomain(ctx, sender); !errors.Is(err, ports.ErrSenderReputationNotFound) {
		t.Fatalf("FindByDomain: got %v", err)
	}

	reputation, err := domain.NewSenderReputation(sender, repoInstant)
	if err != nil {
		t.Fatal(err)
	}
	if err := reputation.ApplyNormalizedOutcome(domain.NormalizedOutcome{Outcome: domain.OutcomeDelivered}, repoInstant); err != nil {
		t.Fatal(err)
	}
	if err := repository.Save(ctx, reputation); err != nil {
		t.Fatal(err)
	}
	restored, err := repository.FindByDomain(ctx, sender)
	if err != nil {
		t.Fatal(err)
	}
	if restored.DeliveredCount() != 1 {
		t.Fatalf("счётчик доставок: %d", restored.DeliveredCount())
	}
}
