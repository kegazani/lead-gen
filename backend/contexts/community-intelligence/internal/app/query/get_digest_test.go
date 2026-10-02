package query

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/community-intelligence/internal/domain"
	"lead-gen/backend/contexts/community-intelligence/internal/ports"
)

// fakeRepo — хранилище выжимок в памяти.
type fakeRepo struct {
	digests map[domain.DigestID]*domain.CommunityDigest
}

func (f *fakeRepo) Save(_ context.Context, digest *domain.CommunityDigest) error {
	if f.digests == nil {
		f.digests = map[domain.DigestID]*domain.CommunityDigest{}
	}
	f.digests[digest.ID()] = digest
	return nil
}

func (f *fakeRepo) FindByID(_ context.Context, id domain.DigestID) (*domain.CommunityDigest, error) {
	digest, ok := f.digests[id]
	if !ok {
		return nil, ports.ErrDigestNotFound
	}
	return digest, nil
}

func (f *fakeRepo) ListByCommunity(_ context.Context, community domain.CommunityID) ([]*domain.CommunityDigest, error) {
	var out []*domain.CommunityDigest
	for _, digest := range f.digests {
		if digest.Source().ID() == community {
			out = append(out, digest)
		}
	}
	return out, nil
}

func buildDigest(t *testing.T, tenant domain.TenantID) *domain.CommunityDigest {
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
	from := time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)
	period, err := domain.NewDigestPeriod(from, from.Add(24*time.Hour))
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
		ID:            "d-1",
		TenantID:      tenant,
		Source:        source,
		Period:        period,
		Topics:        topics,
		CommunitySize: 100,
		At:            from.Add(time.Hour),
		EventID:       "e-1",
	})
	if err != nil {
		t.Fatalf("NewDigest: %v", err)
	}
	return digest
}

func TestGetDigestReturnsAnonymizedView(t *testing.T) {
	repo := &fakeRepo{}
	if err := repo.Save(context.Background(), buildDigest(t, "t-1")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	handler := NewGetDigestHandler(repo)
	view, err := handler.Handle(context.Background(), GetDigest{DigestID: "d-1", TenantID: "t-1"})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if view.CommunityID != "c-1" || len(view.Topics) != 1 {
		t.Fatalf("представление: %+v", view)
	}
	if view.Topics[0].Sources[0].MessageID != "m-1" {
		t.Fatalf("источник не сохранён: %+v", view.Topics[0].Sources)
	}
}

func TestGetDigestHidesForeignTenant(t *testing.T) {
	repo := &fakeRepo{}
	if err := repo.Save(context.Background(), buildDigest(t, "t-1")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	handler := NewGetDigestHandler(repo)
	_, err := handler.Handle(context.Background(), GetDigest{DigestID: "d-1", TenantID: "t-2"})
	if !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("ожидался ErrTenantMismatch, получено %v", err)
	}
}

func TestListDigestsFiltersForeignTenant(t *testing.T) {
	repo := &fakeRepo{}
	if err := repo.Save(context.Background(), buildDigest(t, "t-1")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	handler := NewListDigestsHandler(repo)
	same, err := handler.Handle(context.Background(), ListDigests{CommunityID: "c-1", TenantID: "t-1"})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(same) != 1 {
		t.Fatalf("выжимок своего тенанта = %d", len(same))
	}
	foreign, err := handler.Handle(context.Background(), ListDigests{CommunityID: "c-1", TenantID: "t-2"})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(foreign) != 0 {
		t.Fatalf("выжимок чужого тенанта = %d", len(foreign))
	}
}
