package app

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/integration-platform/internal/domain"
)

// Политика выбирает зарегистрированный адаптер по обязательным возможностям.
func TestSelectAdapter_SelectsRegistered(t *testing.T) {
	repository := newRegistrationRepo()
	register := NewRegisterAdapterHandler(repository)
	if _, err := register.Handle(context.Background(), registerCommand(
		"adapter-1", "firecrawl", domain.CapabilitySearch, domain.CapabilityCrawl,
	)); err != nil {
		t.Fatalf("регистрация: %v", err)
	}

	handler := NewSelectAdapterHandler(repository)
	selection, err := handler.Handle(context.Background(), SelectAdapterCommand{
		Contract: "provider-port",
		Required: []domain.CapabilityName{domain.CapabilitySearch},
	})
	if err != nil {
		t.Fatalf("выбор: %v", err)
	}
	if selection.Adapter.Name() != "firecrawl" {
		t.Fatalf("выбран адаптер %q", selection.Adapter.Name())
	}
}

// Неизвестный адаптер и недостающая возможность завершают проверку до обработки
// рабочих запросов.
func TestSelectAdapter_FailsBeforeWork(t *testing.T) {
	repository := newRegistrationRepo()
	register := NewRegisterAdapterHandler(repository)
	if _, err := register.Handle(context.Background(), registerCommand("adapter-1", "firecrawl")); err != nil {
		t.Fatalf("регистрация: %v", err)
	}
	handler := NewSelectAdapterHandler(repository)

	unknown := domain.AdapterName("missing")
	if _, err := handler.Handle(context.Background(), SelectAdapterCommand{
		Contract:  "provider-port",
		Required:  []domain.CapabilityName{domain.CapabilitySearch},
		Preferred: &unknown,
	}); !errors.Is(err, domain.ErrAdapterNotFound) {
		t.Fatalf("неизвестный адаптер: %v", err)
	}

	if _, err := handler.Handle(context.Background(), SelectAdapterCommand{
		Contract: "provider-port",
		Required: []domain.CapabilityName{domain.CapabilitySend},
	}); !errors.Is(err, domain.ErrAdapterNotFound) {
		t.Fatalf("недостающая возможность: %v", err)
	}
}
