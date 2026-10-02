package domain

import (
	"errors"
	"testing"
	"time"
)

func testDigest(t *testing.T) *CommunityDigest {
	t.Helper()
	source := testSource(t, "c-1")
	from := time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)
	period, err := NewDigestPeriod(from, from.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("NewDigestPeriod: %v", err)
	}
	topics, err := BuildTopics([]Candidate{
		candidate("Нужна цена", "m-1"),
	}, BuildTopicsOptions{CommunitySize: 100, QuoteThreshold: 10})
	if err != nil {
		t.Fatalf("BuildTopics: %v", err)
	}
	digest, err := NewDigest(NewDigestParams{
		ID:            "d-1",
		TenantID:      "t-1",
		Source:        source,
		Period:        period,
		Topics:        topics,
		CommunitySize: 100,
		At:            from.Add(time.Hour),
		EventID:       "e-1",
		CorrelationID: "corr-1",
	})
	if err != nil {
		t.Fatalf("NewDigest: %v", err)
	}
	return digest
}

func TestNewDigestEmitsCreatedEvent(t *testing.T) {
	digest := testDigest(t)
	events := digest.DomainEvents()
	if len(events) != 1 {
		t.Fatalf("ожидалось 1 событие, получено %d", len(events))
	}
	event, ok := events[0].(CommunityDigestCreated)
	if !ok {
		t.Fatalf("тип события %T", events[0])
	}
	if event.Name() != EventCommunityDigestCreated {
		t.Fatalf("имя события %q", event.Name())
	}
	if event.DigestID != "d-1" || event.CommunityID != "c-1" {
		t.Fatalf("событие ссылается не на ту выжимку: %+v", event)
	}
	if event.TopicCount != 1 {
		t.Fatalf("число тем в событии = %d", event.TopicCount)
	}
	if !event.Header().OccurredAt.Equal(digest.CreatedAt()) {
		t.Fatal("время события должно совпадать со временем выжимки")
	}
}

func TestNewDigestRequiresTopics(t *testing.T) {
	source := testSource(t, "c-1")
	from := time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)
	period, err := NewDigestPeriod(from, from.Add(time.Hour))
	if err != nil {
		t.Fatalf("NewDigestPeriod: %v", err)
	}
	_, err = NewDigest(NewDigestParams{
		ID:       "d-1",
		TenantID: "t-1",
		Source:   source,
		Period:   period,
		At:       from.Add(time.Minute),
		EventID:  "e-1",
	})
	if !errors.Is(err, ErrNoMessages) {
		t.Fatalf("ожидался ErrNoMessages, получено %v", err)
	}
}

func TestNewDigestRequiresAuthorizedSource(t *testing.T) {
	from := time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)
	period, err := NewDigestPeriod(from, from.Add(time.Hour))
	if err != nil {
		t.Fatalf("NewDigestPeriod: %v", err)
	}
	_, err = NewDigest(NewDigestParams{
		ID:       "d-1",
		TenantID: "t-1",
		Source:   CommunitySource{},
		Period:   period,
		Topics:   []Topic{{name: "тема", mentions: 1}},
		At:       from.Add(time.Minute),
		EventID:  "e-1",
	})
	if !errors.Is(err, ErrSourceNotAuthorized) {
		t.Fatalf("ожидался ErrSourceNotAuthorized, получено %v", err)
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	digest := testDigest(t)
	restored, err := RehydrateDigest(digest.Snapshot())
	if err != nil {
		t.Fatalf("RehydrateDigest: %v", err)
	}
	if restored.ID() != digest.ID() || restored.Version() != digest.Version() {
		t.Fatalf("восстановлено %s v%d", restored.ID(), restored.Version())
	}
	if len(restored.DomainEvents()) != 0 {
		t.Fatal("восстановление не должно эмитить события")
	}
	if len(restored.Topics()) != 1 || restored.Topics()[0].Name() != "Нужна цена" {
		t.Fatalf("темы не восстановлены: %+v", restored.Topics())
	}
}

func TestRehydrateRejectsEmptyTopics(t *testing.T) {
	digest := testDigest(t)
	snapshot := digest.Snapshot()
	snapshot.Topics = nil
	if _, err := RehydrateDigest(snapshot); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("ожидался ErrInvalidSnapshot, получено %v", err)
	}
}
