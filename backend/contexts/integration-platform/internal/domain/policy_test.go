package domain

import (
	"errors"
	"strings"
	"testing"
)

// Политика выбирает первый адаптер, объявивший все обязательные возможности.
func TestSelectAdapter_Success(t *testing.T) {
	searchOnly := mustRegistration(t, "a-1", "search-only", "1.0.0", CapabilitySearch)
	searchCrawl := mustRegistration(t, "a-2", "search-crawl", "1.0.0", CapabilitySearch, CapabilityCrawl)

	selection, err := SelectAdapter(
		[]*AdapterRegistration{searchOnly, searchCrawl},
		AdapterSelectionRequest{Contract: "provider-port", Required: []CapabilityName{CapabilitySearch, CapabilityCrawl}},
	)
	if err != nil {
		t.Fatalf("выбор: %v", err)
	}
	if selection.Adapter != searchCrawl {
		t.Fatalf("выбран %q", selection.Adapter.Name())
	}
	if selection.Contract != "provider-port" {
		t.Fatalf("контракт: %q", selection.Contract)
	}
}

// Названный адаптер выбирается явно; незарегистрированный — диагностика без
// секретов.
func TestSelectAdapter_Preferred(t *testing.T) {
	adapter := mustRegistration(t, "a-1", "firecrawl", "1.0.0", CapabilitySearch)
	name, _ := ParseAdapterName("firecrawl")

	selection, err := SelectAdapter([]*AdapterRegistration{adapter}, AdapterSelectionRequest{
		Contract:  "provider-port",
		Required:  []CapabilityName{CapabilitySearch},
		Preferred: &name,
	})
	if err != nil {
		t.Fatalf("выбор: %v", err)
	}
	if selection.Adapter != adapter {
		t.Fatalf("выбран %q", selection.Adapter.Name())
	}

	unknown, _ := ParseAdapterName("missing")
	_, err = SelectAdapter([]*AdapterRegistration{adapter}, AdapterSelectionRequest{
		Contract:  "provider-port",
		Required:  []CapabilityName{CapabilitySearch},
		Preferred: &unknown,
	})
	if !errors.Is(err, ErrAdapterNotFound) {
		t.Fatalf("неизвестный адаптер: %v", err)
	}
	if !strings.Contains(err.Error(), "provider-port") || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("диагностика должна называть контракт и адаптер: %v", err)
	}
}

// Недостающая обязательная возможность — отказ.
func TestSelectAdapter_MissingCapability(t *testing.T) {
	adapter := mustRegistration(t, "a-1", "firecrawl", "1.0.0", CapabilitySearch)
	_, err := SelectAdapter([]*AdapterRegistration{adapter}, AdapterSelectionRequest{
		Contract: "provider-port",
		Required: []CapabilityName{CapabilitySearch, CapabilitySend},
	})
	if !errors.Is(err, ErrAdapterNotFound) {
		t.Fatalf("недостающая возможность: %v", err)
	}
}

// Пустой набор обязательных возможностей отвергается.
func TestSelectAdapter_EmptyRequired(t *testing.T) {
	_, err := SelectAdapter(nil, AdapterSelectionRequest{Contract: "provider-port"})
	if !errors.Is(err, ErrEmptyRequiredCapabilities) {
		t.Fatalf("получено %v", err)
	}
}
