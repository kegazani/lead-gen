package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/integration-platform/internal/domain"
)

var testTime = time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)

func registerCommand(id, name string, capabilities ...domain.CapabilityName) RegisterAdapterCommand {
	if len(capabilities) == 0 {
		capabilities = []domain.CapabilityName{domain.CapabilitySearch}
	}
	return RegisterAdapterCommand{
		AdapterID:       domain.AdapterID(id),
		AdapterName:     domain.AdapterName(name),
		ManifestVersion: "1.0.0",
		Capabilities:    capabilities,
		RegisteredAt:    testTime,
	}
}

// Регистрация создаётся, загружается обратно и несёт событие adapter-registered.
func TestRegisterAdapter_SavesAndLoads(t *testing.T) {
	repository := newRegistrationRepo()
	handler := NewRegisterAdapterHandler(repository)

	result, err := handler.Handle(context.Background(), registerCommand("adapter-1", "firecrawl"))
	if err != nil {
		t.Fatalf("регистрация: %v", err)
	}
	if !result.Created {
		t.Fatal("регистрация должна быть создана")
	}
	if len(result.Events) != 1 || result.Events[0].Name() != domain.EventAdapterRegistered {
		t.Fatalf("события: %v", result.Events)
	}

	loaded, err := repository.FindByName(context.Background(), "firecrawl")
	if err != nil {
		t.Fatalf("загрузка по имени: %v", err)
	}
	if loaded.ID() != "adapter-1" || !loaded.Declares(domain.CapabilitySearch) {
		t.Fatalf("загруженная регистрация: %+v", loaded.Snapshot())
	}
}

// Повторная регистрация того же имени отвергается без изменения существующей.
func TestRegisterAdapter_RejectsDuplicateName(t *testing.T) {
	repository := newRegistrationRepo()
	handler := NewRegisterAdapterHandler(repository)

	if _, err := handler.Handle(context.Background(), registerCommand("adapter-1", "firecrawl")); err != nil {
		t.Fatalf("первая регистрация: %v", err)
	}
	_, err := handler.Handle(context.Background(), registerCommand("adapter-2", "firecrawl"))
	if !errors.Is(err, ErrAdapterNameTaken) {
		t.Fatalf("повтор имени: %v", err)
	}

	loaded, err := repository.FindByName(context.Background(), "firecrawl")
	if err != nil {
		t.Fatalf("загрузка: %v", err)
	}
	if loaded.ID() != "adapter-1" {
		t.Fatalf("существующая регистрация изменилась: %q", loaded.ID())
	}
}
