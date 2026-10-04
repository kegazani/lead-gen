package messagedelivery

import (
	"context"
	"testing"

	"lead-gen/backend/contexts/message-delivery/internal/adapters/mailer"
)

// Тема и тело команды доходят до порта отправки: реальному провайдеру нужно
// содержимое письма, а не только идентификаторы.
func TestSlice_ContentReachesMailer(t *testing.T) {
	memory := mailer.New()
	slice, err := Build(Params{Mailer: memory})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	command := validPortCommand()
	command.Subject = "Тема письма"
	command.Body = "Тело письма"

	if _, err := NewDeliveryCommandPort(slice).Deliver(context.Background(), command); err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	sent := memory.Sent()
	if len(sent) != 1 {
		t.Fatalf("ожидалась одна отправка, got %d", len(sent))
	}
	if sent[0].Subject != "Тема письма" || sent[0].Body != "Тело письма" {
		t.Fatalf("содержимое не дошло до порта: %+v", sent[0])
	}
	if sent[0].To.String() != "lead@example.com" || sent[0].SenderDomain.String() != "mail.example.com" {
		t.Fatalf("адрес или домен не переданы: %+v", sent[0])
	}
}

// Команда без содержимого остаётся допустимой: поля необязательны.
func TestSlice_CommandWithoutContentAccepted(t *testing.T) {
	slice, err := Build(Params{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := NewDeliveryCommandPort(slice).Deliver(context.Background(), validPortCommand()); err != nil {
		t.Fatalf("команда без содержимого должна быть принята: %v", err)
	}
}
