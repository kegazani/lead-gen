package telegram

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/community-intelligence/internal/domain"
	"lead-gen/backend/contexts/community-intelligence/internal/ports"
)

func sourceFixture(t *testing.T, id domain.CommunityID) domain.CommunitySource {
	t.Helper()
	source, err := domain.NewCommunitySource(domain.NewCommunitySourceParams{
		ID:         id,
		Title:      "Сообщество",
		Kind:       domain.SourceUserAccount,
		Authorized: true,
	})
	if err != nil {
		t.Fatalf("NewCommunitySource: %v", err)
	}
	return source
}

func TestDeterministicFiltersByPeriodAndLimit(t *testing.T) {
	day := time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)
	period, err := domain.NewDigestPeriod(day, day.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("NewDigestPeriod: %v", err)
	}
	reader := NewDeterministic()
	reader.SetCommunity("c-1", 42, true, []Message{
		{ID: "m-1", Author: "a-1", Text: "старое", PostedAt: day.Add(-time.Hour)},
		{ID: "m-2", Author: "a-2", Text: "в окне", PostedAt: day.Add(time.Hour)},
		{ID: "m-3", Author: "a-3", Text: "позже окна", PostedAt: day.Add(48 * time.Hour)},
	})

	result, err := reader.ReadCommunity(context.Background(), ports.ReadRequest{
		Source: sourceFixture(t, "c-1"),
		Period: period,
	})
	if err != nil {
		t.Fatalf("ReadCommunity: %v", err)
	}
	if len(result.Messages) != 1 || result.Messages[0].MessageID != "m-2" {
		t.Fatalf("сообщения: %+v", result.Messages)
	}
	if result.CommunitySize != 42 || !result.ParticipantsAvailable {
		t.Fatalf("метаданные: size=%d available=%v", result.CommunitySize, result.ParticipantsAvailable)
	}
}

func TestDeterministicRespectsMaxMessages(t *testing.T) {
	day := time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)
	period, err := domain.NewDigestPeriod(day, day.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("NewDigestPeriod: %v", err)
	}
	reader := NewDeterministic()
	reader.SetCommunity("c-1", 42, true, []Message{
		{ID: "m-2", Text: "второе", PostedAt: day.Add(2 * time.Hour)},
		{ID: "m-1", Text: "первое", PostedAt: day.Add(time.Hour)},
	})
	result, err := reader.ReadCommunity(context.Background(), ports.ReadRequest{
		Source:      sourceFixture(t, "c-1"),
		Period:      period,
		MaxMessages: 1,
	})
	if err != nil {
		t.Fatalf("ReadCommunity: %v", err)
	}
	if len(result.Messages) != 1 || result.Messages[0].MessageID != "m-1" {
		t.Fatalf("ожидалось самое раннее сообщение: %+v", result.Messages)
	}
}

func TestDeterministicRejectsUnknownCommunity(t *testing.T) {
	day := time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)
	period, err := domain.NewDigestPeriod(day, day.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("NewDigestPeriod: %v", err)
	}
	_, err = NewDeterministic().ReadCommunity(context.Background(), ports.ReadRequest{
		Source: sourceFixture(t, "c-1"),
		Period: period,
	})
	if !errors.Is(err, ports.ErrCommunityUnavailable) {
		t.Fatalf("ожидался ErrCommunityUnavailable, получено %v", err)
	}
}

func TestDeterministicRejectsInvalidSession(t *testing.T) {
	day := time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)
	period, err := domain.NewDigestPeriod(day, day.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("NewDigestPeriod: %v", err)
	}
	reader := NewDeterministic()
	reader.SessionValid = false
	_, err = reader.ReadCommunity(context.Background(), ports.ReadRequest{
		Source: sourceFixture(t, "c-1"),
		Period: period,
	})
	if !errors.Is(err, ports.ErrSessionRequired) {
		t.Fatalf("ожидался ErrSessionRequired, получено %v", err)
	}
}

func TestUnavailableAlwaysRequiresSession(t *testing.T) {
	day := time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)
	period, err := domain.NewDigestPeriod(day, day.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("NewDigestPeriod: %v", err)
	}
	_, err = Unavailable{}.ReadCommunity(context.Background(), ports.ReadRequest{
		Source: sourceFixture(t, "c-1"),
		Period: period,
	})
	if !errors.Is(err, ports.ErrSessionRequired) {
		t.Fatalf("ожидался ErrSessionRequired, получено %v", err)
	}
}
