package inmemory_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/message-delivery/internal/adapters/outbox/inmemory"
	"lead-gen/backend/contexts/message-delivery/internal/app/command"
	"lead-gen/backend/contexts/message-delivery/internal/domain"
	"lead-gen/backend/contexts/message-delivery/internal/ports"
)

// Интеграция use case с реальным адаптером проверяется на стороне адаптера:
// прикладной слой не получает обратную зависимость от инфраструктуры.
func TestDeliverMessage_CampaignFenceWinsBeforeReservation(t *testing.T) {
	address, err := domain.ParseDeliveryAddress("lead@example.com")
	if err != nil {
		t.Fatal(err)
	}
	sender, err := domain.ParseSenderDomain("mail.example.com")
	if err != nil {
		t.Fatal(err)
	}
	cmd := domain.DeliveryCommand{CommandID: "cmd-1", TenantID: "tenant-1", ApprovedActionID: "act-1", RecipientRef: "ref-1", Address: address, SenderDomain: sender, CorrelationID: "act-1", CampaignID: "campaign-1"}
	uow := inmemory.NewUnitOfWork()
	if err := uow.CancelCampaign(context.Background(), cmd.TenantID.String(), cmd.CampaignID); err != nil {
		t.Fatal(err)
	}
	mailer := &fenceMailer{}
	handler := command.NewDeliverMessageHandler(fenceSuppression{}, mailer, fenceClock{}, uow)
	if _, err := handler.Handle(context.Background(), cmd); !errors.Is(err, ports.ErrCampaignCancelled) {
		t.Fatalf("ожидался cancellation fence: %v", err)
	}
	if mailer.calls != 0 {
		t.Fatalf("mailer вызван до reservation fence: %d", mailer.calls)
	}
}

type fenceMailer struct{ calls int }

func (m *fenceMailer) Send(context.Context, ports.OutgoingMessage) (ports.ProviderResponse, error) {
	m.calls++
	return ports.ProviderResponse{}, nil
}

type fenceSuppression struct{}

func (fenceSuppression) IsSuppressed(context.Context, domain.TenantID, domain.DeliveryAddress) (bool, error) {
	return false, nil
}

type fenceClock struct{}

func (fenceClock) Now() time.Time { return time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC) }
