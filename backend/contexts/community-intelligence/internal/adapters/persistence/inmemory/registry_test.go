package inmemory

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/community-intelligence/internal/domain"
	"lead-gen/backend/contexts/community-intelligence/internal/ports"
)

func registrySource(t *testing.T, id domain.CommunityID) domain.CommunitySource {
	t.Helper()
	source, err := domain.NewCommunitySource(domain.NewCommunitySourceParams{
		ID:         id,
		Title:      "Сообщество " + id.String(),
		Kind:       domain.SourceUserAccount,
		Authorized: true,
	})
	if err != nil {
		t.Fatalf("NewCommunitySource: %v", err)
	}
	return source
}

func TestRegistryFindRegistered(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register("t-1", registrySource(t, "c-1"), 0); err != nil {
		t.Fatalf("Register: %v", err)
	}
	source, err := registry.FindByID(context.Background(), "t-1", "c-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if source.ID() != "c-1" {
		t.Fatalf("источник %q", source.ID())
	}
}

func TestRegistryHidesForeignTenant(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register("t-1", registrySource(t, "c-1"), 0); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := registry.FindByID(context.Background(), "t-2", "c-1"); !errors.Is(err, ports.ErrCommunityNotFound) {
		t.Fatalf("ожидался ErrCommunityNotFound, получено %v", err)
	}
}

func TestRegistryRejectsUnauthorizedSource(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register("t-1", domain.CommunitySource{}, 0); !errors.Is(err, domain.ErrSourceNotAuthorized) {
		t.Fatalf("ожидался ErrSourceNotAuthorized, получено %v", err)
	}
}

func TestRegistryTrackedIsSorted(t *testing.T) {
	registry := NewRegistry()
	for _, id := range []domain.CommunityID{"c-2", "c-1"} {
		if err := registry.Register("t-1", registrySource(t, id), 5); err != nil {
			t.Fatalf("Register: %v", err)
		}
	}
	tracked, err := registry.Tracked(context.Background())
	if err != nil {
		t.Fatalf("Tracked: %v", err)
	}
	if len(tracked) != 2 || tracked[0].CommunityID != "c-1" || tracked[1].CommunityID != "c-2" {
		t.Fatalf("отслеживаемые: %+v", tracked)
	}
	if tracked[0].MaxMessages != 5 {
		t.Fatalf("MaxMessages = %d", tracked[0].MaxMessages)
	}
}
