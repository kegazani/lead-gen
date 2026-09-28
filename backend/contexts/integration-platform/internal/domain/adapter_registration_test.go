package domain

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

var testAt = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

func mustRegistration(t *testing.T, id, name, version string, capabilities ...CapabilityName) *AdapterRegistration {
	t.Helper()
	registration, err := RegisterAdapter(RegisterAdapterParams{
		ID:           AdapterID(id),
		Manifest:     mustManifest(t, name, version, capabilities...),
		RegisteredAt: testAt,
	})
	if err != nil {
		t.Fatalf("регистрация адаптера: %v", err)
	}
	return registration
}

// Регистрация создаётся и эмитит adapter-registered без полей секретов.
func TestRegisterAdapter_EmitsEvent(t *testing.T) {
	registration := mustRegistration(t, "adapter-1", "firecrawl", "2.0.0", CapabilitySearch, CapabilityCrawl)

	if registration.ID() != "adapter-1" || registration.Name() != "firecrawl" {
		t.Fatalf("идентификатор/имя: %q/%q", registration.ID(), registration.Name())
	}

	events := registration.DomainEvents()
	if len(events) != 1 {
		t.Fatalf("число событий: %d", len(events))
	}
	event, ok := events[0].(AdapterRegistered)
	if !ok {
		t.Fatalf("тип события: %T", events[0])
	}
	if event.AdapterID != "adapter-1" || event.AdapterName != "firecrawl" || event.ManifestVersion != "2.0.0" {
		t.Fatalf("поля события: %+v", event)
	}
	if len(event.Capabilities) != 2 {
		t.Fatalf("возможности события: %v", event.Capabilities)
	}
	if rendered := fmt.Sprintf("%+v", event); strings.Contains(strings.ToLower(rendered), "secret") {
		t.Fatalf("событие не должно нести секретов: %s", rendered)
	}
}

// Регистрация с пустым manifest не создаётся.
func TestRegisterAdapter_RejectsEmptyManifest(t *testing.T) {
	_, err := RegisterAdapter(RegisterAdapterParams{
		ID:           "adapter-1",
		Manifest:     Manifest{},
		RegisteredAt: testAt,
	})
	if !errors.Is(err, ErrEmptyManifestCapabilities) {
		t.Fatalf("получено %v", err)
	}
}

// PullDomainEvents очищает буфер событий.
func TestRegisterAdapter_PullClearsEvents(t *testing.T) {
	registration := mustRegistration(t, "adapter-1", "firecrawl", "1.0.0", CapabilitySearch)
	if pulled := registration.PullDomainEvents(); len(pulled) != 1 {
		t.Fatalf("вычитано событий: %d", len(pulled))
	}
	if remaining := registration.DomainEvents(); len(remaining) != 0 {
		t.Fatalf("буфер не очищен: %d", len(remaining))
	}
}

// Require отвечает только на объявленные возможности.
func TestRegistration_Require(t *testing.T) {
	registration := mustRegistration(t, "adapter-1", "firecrawl", "1.0.0", CapabilitySearch)
	if err := registration.Require(CapabilitySearch); err != nil {
		t.Fatalf("объявленная возможность: %v", err)
	}
	err := registration.Require(CapabilitySend)
	if !errors.Is(err, ErrCapabilityNotDeclared) {
		t.Fatalf("необъявленная возможность: %v", err)
	}
	if !strings.Contains(err.Error(), "firecrawl") {
		t.Fatalf("диагностика должна называть адаптер: %v", err)
	}
}

// Восстановление регистрации проверяет инварианты и не эмитит событий.
func TestRehydrateAdapterRegistration(t *testing.T) {
	registration := mustRegistration(t, "adapter-1", "firecrawl", "1.0.0", CapabilitySearch)
	restored, err := RehydrateAdapterRegistration(registration.Snapshot())
	if err != nil {
		t.Fatalf("восстановление: %v", err)
	}
	if restored.Name() != "firecrawl" || !restored.Declares(CapabilitySearch) {
		t.Fatalf("восстановленная регистрация: %+v", restored.Snapshot())
	}
	if events := restored.DomainEvents(); len(events) != 0 {
		t.Fatalf("восстановление не должно эмитить события: %d", len(events))
	}

	broken := registration.Snapshot()
	broken.Manifest = Manifest{}
	if _, err := RehydrateAdapterRegistration(broken); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("противоречивый снимок: %v", err)
	}
}
