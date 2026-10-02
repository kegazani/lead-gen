package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"lead-gen/backend/contexts/publishing/internal/domain"
)

var fixedAt = time.Date(2026, time.March, 4, 12, 0, 0, 0, time.UTC)

func newTarget(t *testing.T, platform, account string) domain.PublicationTarget {
	t.Helper()
	target, err := domain.NewPublicationTarget(platform, account)
	if err != nil {
		t.Fatalf("NewPublicationTarget: %v", err)
	}
	return target
}

func newMaterial(t *testing.T, target domain.PublicationTarget, body string) domain.Material {
	t.Helper()
	material, err := domain.NewMaterial(target, "Заголовок", body)
	if err != nil {
		t.Fatalf("NewMaterial: %v", err)
	}
	return material
}

func newDraft(t *testing.T, target domain.PublicationTarget, body string) *domain.Publication {
	t.Helper()
	publication, err := domain.NewPublication(domain.NewPublicationParams{
		ID:             "pub-1",
		TenantID:       "tenant-1",
		Digest:         domain.DigestRef{ID: "digest-1", CommunityID: "community-1", Period: "2026-W10"},
		Target:         target,
		Material:       newMaterial(t, target, body),
		IdempotencyKey: "key-1",
		At:             fixedAt,
	})
	if err != nil {
		t.Fatalf("NewPublication: %v", err)
	}
	return publication
}

func TestNewPublicationRejectsMissingFields(t *testing.T) {
	target := newTarget(t, "linkedin", "acct")
	material := newMaterial(t, target, "текст")

	cases := map[string]domain.NewPublicationParams{
		"пустой id":      {TenantID: "t", Digest: domain.DigestRef{ID: "d", CommunityID: "c", Period: "p"}, Target: target, Material: material, IdempotencyKey: "k", At: fixedAt},
		"пустой ключ":    {ID: "p", TenantID: "t", Digest: domain.DigestRef{ID: "d", CommunityID: "c", Period: "p"}, Target: target, Material: material, At: fixedAt},
		"пустая выжимка": {ID: "p", TenantID: "t", Target: target, Material: material, IdempotencyKey: "k", At: fixedAt},
		"без времени":    {ID: "p", TenantID: "t", Digest: domain.DigestRef{ID: "d", CommunityID: "c", Period: "p"}, Target: target, Material: material, IdempotencyKey: "k"},
	}
	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := domain.NewPublication(params); err == nil {
				t.Fatalf("ожидалась ошибка, получено nil")
			}
		})
	}
}

func TestPlatformClosedSet(t *testing.T) {
	if _, err := domain.ParsePlatform("instagram"); !errors.Is(err, domain.ErrUnknownPlatform) {
		t.Fatalf("instagram: ошибка = %v, ожидалась ErrUnknownPlatform", err)
	}
	for _, platform := range []string{"dzen", "linkedin", "telegram"} {
		if _, err := domain.ParsePlatform(platform); err != nil {
			t.Fatalf("%s: неожиданная ошибка %v", platform, err)
		}
	}
}

func TestMaterialShortenedToPlatformLimit(t *testing.T) {
	target := newTarget(t, "linkedin", "acct")
	limit := target.Platform().MaxBody()
	material := newMaterial(t, target, strings.Repeat("я", limit+50))
	if !material.Shortened() {
		t.Fatal("материал не помечен сокращённым")
	}
	if got := len([]rune(material.Body())); got > limit {
		t.Fatalf("длина материала %d превышает предел %d", got, limit)
	}
}

func TestPublishRequiresApproval(t *testing.T) {
	publication := newDraft(t, newTarget(t, "telegram", "channel"), "текст")
	err := publication.MarkPublished(domain.PublishParams{
		Actor: "operator-1", At: fixedAt, Address: "https://example/1", EventID: "evt-1",
	})
	if !errors.Is(err, domain.ErrApprovalRequired) {
		t.Fatalf("ошибка = %v, ожидалась ErrApprovalRequired", err)
	}
}

func TestApprovePublishEmitsMaterialPublished(t *testing.T) {
	publication := newDraft(t, newTarget(t, "telegram", "channel"), "текст")
	if err := publication.Approve("operator-1", fixedAt); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if publication.Status() != domain.StatusApproved {
		t.Fatalf("статус = %q, ожидался approved", publication.Status())
	}
	if err := publication.MarkPublished(domain.PublishParams{
		Actor: "operator-2", At: fixedAt, Address: "https://tg.example/channel/pub-1",
		EventID: "evt-1", CorrelationID: "corr-1",
	}); err != nil {
		t.Fatalf("MarkPublished: %v", err)
	}
	if publication.Status() != domain.StatusPublished {
		t.Fatalf("статус = %q, ожидался published", publication.Status())
	}
	events := publication.DomainEvents()
	if len(events) != 1 {
		t.Fatalf("событий = %d, ожидалось 1", len(events))
	}
	published, ok := events[0].(domain.MaterialPublished)
	if !ok || published.Name() != domain.EventMaterialPublished {
		t.Fatalf("событие = %#v, ожидалось material-published", events[0])
	}
	if published.Address == "" || published.ApprovedBy != "operator-1" {
		t.Fatalf("событие без адреса или утвердившего: %#v", published)
	}
	if len(publication.PullDomainEvents()) != 1 || len(publication.DomainEvents()) != 0 {
		t.Fatal("PullDomainEvents не очистил буфер")
	}
}

func TestRejectIsTerminal(t *testing.T) {
	publication := newDraft(t, newTarget(t, "dzen", "acct"), "текст")
	if err := publication.Reject("operator-1", fixedAt, "не по формату"); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	if err := publication.Approve("operator-1", fixedAt); !errors.Is(err, domain.ErrTerminalState) {
		t.Fatalf("ошибка = %v, ожидалась ErrTerminalState", err)
	}
}

func TestRejectRequiresReason(t *testing.T) {
	publication := newDraft(t, newTarget(t, "dzen", "acct"), "текст")
	if err := publication.Reject("operator-1", fixedAt, "  "); !errors.Is(err, domain.ErrReasonRequired) {
		t.Fatalf("ошибка = %v, ожидалась ErrReasonRequired", err)
	}
}

func TestMarkFailedRequiresReason(t *testing.T) {
	publication := newDraft(t, newTarget(t, "dzen", "acct"), "текст")
	if err := publication.Approve("operator-1", fixedAt); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := publication.MarkFailed(domain.FailParams{Actor: "operator-1", At: fixedAt}); !errors.Is(err, domain.ErrReasonRequired) {
		t.Fatalf("ошибка = %v, ожидалась ErrReasonRequired", err)
	}
	if err := publication.MarkFailed(domain.FailParams{Actor: "operator-1", At: fixedAt, Reason: "отказ площадки"}); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	if publication.Status() != domain.StatusFailed {
		t.Fatalf("статус = %q, ожидался failed", publication.Status())
	}
	if len(publication.DomainEvents()) != 0 {
		t.Fatal("отказ площадки не должен эмитить material-published")
	}
}

func TestRehydrateRoundTrip(t *testing.T) {
	publication := newDraft(t, newTarget(t, "linkedin", "acct"), "текст")
	if err := publication.Approve("operator-1", fixedAt); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	snapshot := publication.Snapshot()
	restored, err := domain.RehydratePublication(snapshot)
	if err != nil {
		t.Fatalf("RehydratePublication: %v", err)
	}
	if restored.Status() != domain.StatusApproved || restored.IdempotencyKey() != "key-1" {
		t.Fatalf("восстановленное состояние не совпало: %+v", restored)
	}

	snapshot.Status = domain.StatusPublished
	if _, err := domain.RehydratePublication(snapshot); !errors.Is(err, domain.ErrInvalidSnapshot) {
		t.Fatalf("ошибка = %v, ожидалась ErrInvalidSnapshot", err)
	}
}
