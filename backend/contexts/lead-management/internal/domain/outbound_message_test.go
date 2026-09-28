package domain

import (
	"errors"
	"testing"
	"time"
)

func TestCreateOutboundMessageRequiresRecipient(t *testing.T) {
	_, err := CreateOutboundMessage(CreateOutboundMessageParams{ID: "m1", LeadID: "example.com"})
	if !errors.Is(err, ErrEmptyRecipient) {
		t.Fatalf("ожидалась ErrEmptyRecipient, получено %v", err)
	}
}

func TestCreateOutboundMessageRequiresID(t *testing.T) {
	_, err := CreateOutboundMessage(CreateOutboundMessageParams{LeadID: "example.com", Recipient: "a@example.com"})
	if !errors.Is(err, ErrEmptyMessageID) {
		t.Fatalf("ожидалась ErrEmptyMessageID, получено %v", err)
	}
}

func TestCreateOutboundMessageRequiresLead(t *testing.T) {
	_, err := CreateOutboundMessage(CreateOutboundMessageParams{ID: "m1", Recipient: "a@example.com"})
	if !errors.Is(err, ErrEmptyCompanyID) {
		t.Fatalf("ожидалась ErrEmptyCompanyID, получено %v", err)
	}
}

func TestCreateOutboundMessageRejectsSuppressedRecipient(t *testing.T) {
	_, err := CreateOutboundMessage(CreateOutboundMessageParams{
		ID:         "m1",
		LeadID:     "example.com",
		Recipient:  "a@example.com",
		Suppressed: true,
	})
	if !errors.Is(err, ErrSuppressedRecipient) {
		t.Fatalf("ожидалась ErrSuppressedRecipient, получено %v", err)
	}
}

func TestOutboundMessageLifecycle(t *testing.T) {
	m, err := CreateOutboundMessage(CreateOutboundMessageParams{
		ID:        "m1",
		LeadID:    "example.com",
		Recipient: "a@example.com",
		Template:  "intro",
		Body:      "Привет, {{company}}",
		CreatedAt: baseTime,
	})
	if err != nil {
		t.Fatalf("CreateOutboundMessage: %v", err)
	}
	if m.Status() != MessageStatusQueued {
		t.Fatalf("Status %q, ожидался %q", m.Status(), MessageStatusQueued)
	}

	m.MarkSent(baseTime.Add(time.Hour))
	if m.Status() != MessageStatusSent {
		t.Fatalf("Status после MarkSent %q", m.Status())
	}
	events := m.Events()
	if len(events) != 1 || events[0].EventName() != "message-sent" {
		t.Fatalf("ожидалось одно событие message-sent, получено %v", events)
	}
	if sent, ok := events[0].(MessageSent); !ok || sent.Recipient != "a@example.com" || sent.MessageID != "m1" {
		t.Fatalf("событие не несёт данные сообщения: %+v", events[0])
	}
	if len(m.Events()) != 0 {
		t.Fatal("после вычитки буфер событий должен быть пуст")
	}
}

func TestOutboundMessageFailureAndBounce(t *testing.T) {
	m, err := CreateOutboundMessage(CreateOutboundMessageParams{
		ID:        "m1",
		LeadID:    "example.com",
		Recipient: "a@example.com",
		CreatedAt: baseTime,
	})
	if err != nil {
		t.Fatalf("CreateOutboundMessage: %v", err)
	}
	m.MarkFailed("smtp refused", baseTime.Add(time.Hour))
	if m.Status() != MessageStatusFailed || m.Reason() != "smtp refused" {
		t.Fatalf("после MarkFailed: %q %q", m.Status(), m.Reason())
	}

	b, err := CreateOutboundMessage(CreateOutboundMessageParams{
		ID:        "m2",
		LeadID:    "example.com",
		Recipient: "b@example.com",
		CreatedAt: baseTime,
	})
	if err != nil {
		t.Fatalf("CreateOutboundMessage: %v", err)
	}
	b.MarkBounced("mailbox full", baseTime.Add(time.Hour))
	if b.Status() != MessageStatusBounced || b.Reason() != "mailbox full" {
		t.Fatalf("после MarkBounced: %q %q", b.Status(), b.Reason())
	}
}

func TestMessageStatusSuppresses(t *testing.T) {
	suppressing := []MessageStatus{MessageStatusBounced, MessageStatusUnsubscribed, MessageStatusRejected}
	for _, status := range suppressing {
		if !status.Suppresses() {
			t.Fatalf("статус %q должен подавлять рассылку", status)
		}
	}
	notSuppressing := []MessageStatus{MessageStatusQueued, MessageStatusSent, MessageStatusFailed}
	for _, status := range notSuppressing {
		if status.Suppresses() {
			t.Fatalf("статус %q не должен подавлять рассылку", status)
		}
	}
}

func TestOutboundMessageSuppressionOutcomes(t *testing.T) {
	cases := []struct {
		status MessageStatus
		mark   func(*OutboundMessage, string, time.Time)
	}{
		{MessageStatusBounced, (*OutboundMessage).MarkBounced},
		{MessageStatusUnsubscribed, (*OutboundMessage).MarkUnsubscribed},
		{MessageStatusRejected, (*OutboundMessage).MarkRejected},
	}
	for _, tc := range cases {
		m, err := CreateOutboundMessage(CreateOutboundMessageParams{
			ID:        "m1",
			LeadID:    "example.com",
			Recipient: "a@example.com",
			CreatedAt: baseTime,
		})
		if err != nil {
			t.Fatalf("CreateOutboundMessage: %v", err)
		}
		tc.mark(m, "причина", baseTime.Add(time.Hour))
		if m.Status() != tc.status || m.Reason() != "причина" {
			t.Fatalf("статус %q: получено %q %q", tc.status, m.Status(), m.Reason())
		}
	}
}

func TestOutboundMessageSnapshotRoundTrip(t *testing.T) {
	m, err := CreateOutboundMessage(CreateOutboundMessageParams{
		ID:        "m1",
		LeadID:    "example.com",
		Recipient: "a@example.com",
		Template:  "intro",
		Body:      "body",
		CreatedAt: baseTime,
	})
	if err != nil {
		t.Fatalf("CreateOutboundMessage: %v", err)
	}
	m.MarkSent(baseTime.Add(time.Hour))

	rehydrated, err := RehydrateOutboundMessage(m.Snapshot())
	if err != nil {
		t.Fatalf("RehydrateOutboundMessage: %v", err)
	}
	if rehydrated.ID() != m.ID() || rehydrated.LeadID() != m.LeadID() || rehydrated.Recipient() != m.Recipient() || rehydrated.Status() != m.Status() {
		t.Fatalf("восстановленное сообщение не совпадает: %+v", rehydrated)
	}
}

func TestRehydrateOutboundMessageRejectsContradictorySnapshot(t *testing.T) {
	if _, err := RehydrateOutboundMessage(OutboundMessageSnapshot{ID: "m1", LeadID: "example.com"}); !errors.Is(err, ErrEmptyRecipient) {
		t.Fatalf("ожидалась ErrEmptyRecipient, получено %v", err)
	}
	if _, err := RehydrateOutboundMessage(OutboundMessageSnapshot{
		ID:        "m1",
		Recipient: "a@example.com",
		Status:    MessageStatus("weird"),
	}); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("ожидалась ErrInvalidSnapshot, получено %v", err)
	}
}
