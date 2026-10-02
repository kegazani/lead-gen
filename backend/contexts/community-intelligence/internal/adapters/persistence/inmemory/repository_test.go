package inmemory

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/community-intelligence/internal/domain"
	"lead-gen/backend/contexts/community-intelligence/internal/ports"
)

func digestFixture(t *testing.T, id domain.DigestID, tenant domain.TenantID, at time.Time) *domain.CommunityDigest {
	t.Helper()
	source, err := domain.NewCommunitySource(domain.NewCommunitySourceParams{
		ID:         "c-1",
		Title:      "Сообщество",
		Kind:       domain.SourceUserAccount,
		Authorized: true,
	})
	if err != nil {
		t.Fatalf("NewCommunitySource: %v", err)
	}
	period, err := domain.NewDigestPeriod(at, at.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("NewDigestPeriod: %v", err)
	}
	topics, err := domain.BuildTopics([]domain.Candidate{
		{Text: "нужна цена", Ref: domain.MessageRef{Community: "c-1", Message: "m-1"}},
	}, domain.BuildTopicsOptions{CommunitySize: 100, QuoteThreshold: 10})
	if err != nil {
		t.Fatalf("BuildTopics: %v", err)
	}
	digest, err := domain.NewDigest(domain.NewDigestParams{
		ID:            id,
		TenantID:      tenant,
		Source:        source,
		Period:        period,
		Topics:        topics,
		CommunitySize: 100,
		At:            at.Add(time.Hour),
		EventID:       domain.EventID("e-" + id.String()),
	})
	if err != nil {
		t.Fatalf("NewDigest: %v", err)
	}
	return digest
}

func TestRepositorySaveAndFind(t *testing.T) {
	repo := New()
	at := time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)
	digest := digestFixture(t, "d-1", "t-1", at)
	if err := repo.Save(context.Background(), digest); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := repo.FindByID(context.Background(), "d-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if loaded.TenantID() != "t-1" || len(loaded.Topics()) != 1 {
		t.Fatalf("загружено: %+v", loaded)
	}
}

func TestRepositoryFindMissing(t *testing.T) {
	repo := New()
	if _, err := repo.FindByID(context.Background(), "d-404"); !errors.Is(err, ports.ErrDigestNotFound) {
		t.Fatalf("ожидался ErrDigestNotFound, получено %v", err)
	}
}

func TestRepositoryListByCommunityOrdersByCreation(t *testing.T) {
	repo := New()
	at := time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)
	later := digestFixture(t, "d-2", "t-1", at.Add(2*time.Hour))
	earlier := digestFixture(t, "d-1", "t-1", at)
	for _, digest := range []*domain.CommunityDigest{later, earlier} {
		if err := repo.Save(context.Background(), digest); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	list, err := repo.ListByCommunity(context.Background(), "c-1")
	if err != nil {
		t.Fatalf("ListByCommunity: %v", err)
	}
	if len(list) != 2 || list[0].ID() != "d-1" || list[1].ID() != "d-2" {
		t.Fatalf("порядок выжимок: %v", []domain.DigestID{list[0].ID(), list[1].ID()})
	}
}
