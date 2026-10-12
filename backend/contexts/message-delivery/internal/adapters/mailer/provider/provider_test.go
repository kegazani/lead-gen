package provider

import (
	"context"
	"errors"
	"net/textproto"
	"testing"

	"lead-gen/backend/contexts/message-delivery/internal/domain"
	"lead-gen/backend/contexts/message-delivery/internal/ports"
	"lead-gen/contracts/integration-platform/mail"
)

// fakePort — подмена контракта mail: фиксирует переданное письмо и отвечает
// заданной ошибкой.
type fakePort struct {
	sent []mail.Message
	err  error
}

func (p *fakePort) Send(_ context.Context, message mail.Message) error {
	p.sent = append(p.sent, message)
	return p.err
}

func testMessage(t *testing.T) ports.OutgoingMessage {
	t.Helper()
	address, err := domain.ParseDeliveryAddress("lead@example.com")
	if err != nil {
		t.Fatal(err)
	}
	sender, err := domain.ParseSenderDomain("mail.example.com")
	if err != nil {
		t.Fatal(err)
	}
	return ports.OutgoingMessage{
		MessageID:    domain.MessageID("msg-1"),
		TenantID:     domain.TenantID("tenant-1"),
		SenderDomain: sender,
		To:           address,
		RecipientRef: domain.RecipientRef("ref-1"),
		Subject:      "Тема",
		Body:         "Тело",
	}
}

func TestNew_RequiresPortAndFrom(t *testing.T) {
	if _, err := New(Config{From: "a@example.com"}); err == nil {
		t.Fatal("без порта mail сборка должна отвергаться")
	}
	if _, err := New(Config{Port: &fakePort{}}); err == nil {
		t.Fatal("без адреса отправителя сборка должна отвергаться")
	}
}

func TestSend_NormalizesSuccess(t *testing.T) {
	port := &fakePort{}
	mailer, err := New(Config{Port: port, From: "sender@example.com"})
	if err != nil {
		t.Fatal(err)
	}

	response, err := mailer.Send(context.Background(), testMessage(t))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if response.Outcome != domain.OutcomeDelivered || response.ProviderCode != "250" {
		t.Fatalf("ответ: %+v", response)
	}
	if len(port.sent) != 1 {
		t.Fatalf("ожидалась одна отправка, got %d", len(port.sent))
	}
	sent := port.sent[0]
	if sent.From != "sender@example.com" || sent.To != "lead@example.com" {
		t.Fatalf("конверт письма: %+v", sent)
	}
	if sent.Subject != "Тема" || sent.Body != "Тело" {
		t.Fatalf("содержимое письма: %+v", sent)
	}
}

func TestSend_NormalizesProviderRejection(t *testing.T) {
	cases := []struct {
		name    string
		code    int
		outcome domain.AttemptOutcome
	}{
		{"жёсткий отказ", 550, domain.OutcomeHardBounce},
		{"мягкий отказ", 421, domain.OutcomeSoftBounce},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			port := &fakePort{err: &textproto.Error{Code: tc.code, Msg: "отказ"}}
			mailer, err := New(Config{Port: port, From: "sender@example.com"})
			if err != nil {
				t.Fatal(err)
			}
			response, err := mailer.Send(context.Background(), testMessage(t))
			if err != nil {
				t.Fatalf("отказ провайдера не должен быть ошибкой транспорта: %v", err)
			}
			if response.Outcome != tc.outcome {
				t.Fatalf("исход: got %q, want %q", response.Outcome, tc.outcome)
			}
			if response.ProviderCode == "" {
				t.Fatal("ожидался код ответа провайдера")
			}
		})
	}
}

func TestSend_TransportFailureIsUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"соединение", errors.New("connection refused")},
		{"аутентификация", &textproto.Error{Code: 535, Msg: "auth failed"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			port := &fakePort{err: tc.err}
			mailer, err := New(Config{Port: port, From: "sender@example.com"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := mailer.Send(context.Background(), testMessage(t)); !errors.Is(err, ports.ErrMailerUnavailable) {
				t.Fatalf("сбой среды должен давать ErrMailerUnavailable, got %v", err)
			}
		})
	}
}

func TestSend_UnknownPostDataOutcomeIsNotRetryableUnavailable(t *testing.T) {
	port := &fakePort{err: mail.ErrOutcomeUnknown}
	mailer, err := New(Config{Port: port, From: "sender@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	response, err := mailer.Send(context.Background(), testMessage(t))
	if err != nil {
		t.Fatalf("неизвестный исход должен быть передан домену для durable фиксации: %v", err)
	}
	if response.Outcome != domain.OutcomeUnknown {
		t.Fatalf("ожидался доменный неизвестный исход, получено %+v", response)
	}
}
