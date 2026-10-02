package communityintelligence

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	persistence "lead-gen/backend/contexts/community-intelligence/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/community-intelligence/internal/adapters/telegram"
	"lead-gen/backend/contexts/community-intelligence/internal/app/command"
	"lead-gen/contracts/community-intelligence/communitydigest"
	"lead-gen/contracts/community-intelligence/communityreadcommand"
)

func TestCommunityReadCommandBuildsDigestAndCommunityDigestReadsIt(t *testing.T) {
	now := time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)
	registry := persistence.NewRegistry()
	if err := registry.Register("t-1", testSource(t, "c-1"), 0); err != nil {
		t.Fatalf("Register: %v", err)
	}
	reader := telegram.NewDeterministic()
	reader.SetCommunity("c-1", 100, true, []telegram.Message{
		{ID: "m-1", Author: "author-1", Text: "ссылка https://t.me/join и нужна цена", PostedAt: now.Add(-time.Hour)},
	})

	slice, err := Build(Config{Registry: registry, Reader: reader, Clock: testClock{now: now}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	read := NewCommunityReadCommand(slice)
	result, err := read.Read(context.Background(), communityreadcommand.ReadCommand{
		CommunityID: "c-1",
		TenantID:    "t-1",
		From:        now.Add(-24 * time.Hour),
		To:          now,
	})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if result.DigestID == "" || result.TopicCount != 1 {
		t.Fatalf("результат чтения: %+v", result)
	}

	digestPort := NewCommunityDigest(slice)
	view, err := digestPort.Get(context.Background(), communitydigest.GetQuery{DigestID: result.DigestID, TenantID: "t-1"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(view.Topics) != 1 || view.CommunityID != "c-1" {
		t.Fatalf("выжимка: %+v", view)
	}
	quote := view.Topics[0].Quotes[0]
	if strings.Contains(quote, "t.me") || strings.Contains(quote, "author-1") {
		t.Fatalf("цитата должна быть обезличена: %q", quote)
	}
}

func TestCommunityDigestHidesForeignTenant(t *testing.T) {
	now := time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)
	registry := persistence.NewRegistry()
	if err := registry.Register("t-1", testSource(t, "c-1"), 0); err != nil {
		t.Fatalf("Register: %v", err)
	}
	reader := telegram.NewDeterministic()
	reader.SetCommunity("c-1", 100, true, []telegram.Message{
		{ID: "m-1", Author: "author-1", Text: "нужна цена", PostedAt: now.Add(-time.Hour)},
	})
	slice, err := Build(Config{Registry: registry, Reader: reader, Clock: testClock{now: now}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	result, err := slice.ReadCommunity.Handle(context.Background(), command.ReadCommunity{
		CommunityID: "c-1",
		TenantID:    "t-1",
		From:        now.Add(-24 * time.Hour),
		To:          now,
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	port := NewCommunityDigest(slice)
	_, err = port.Get(context.Background(), communitydigest.GetQuery{DigestID: result.DigestID.String(), TenantID: "t-2"})
	if !errors.Is(err, communitydigest.ErrNotFound) {
		t.Fatalf("ожидался ErrNotFound, получено %v", err)
	}
}

func TestCommunityReadCommandRejectsUnknownCommunity(t *testing.T) {
	slice, err := Build(Config{Registry: persistence.NewRegistry()})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	read := NewCommunityReadCommand(slice)
	now := time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)
	_, err = read.Read(context.Background(), communityreadcommand.ReadCommand{
		CommunityID: "c-404",
		TenantID:    "t-1",
		From:        now.Add(-time.Hour),
		To:          now,
	})
	if !errors.Is(err, communityreadcommand.ErrRejected) {
		t.Fatalf("ожидался ErrRejected, получено %v", err)
	}
}

func TestCommunityReadCommandWithoutSessionIsUnavailable(t *testing.T) {
	registry := persistence.NewRegistry()
	if err := registry.Register("t-1", testSource(t, "c-1"), 0); err != nil {
		t.Fatalf("Register: %v", err)
	}
	slice, err := Build(Config{Registry: registry})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	read := NewCommunityReadCommand(slice)
	now := time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)
	_, err = read.Read(context.Background(), communityreadcommand.ReadCommand{
		CommunityID: "c-1",
		TenantID:    "t-1",
		From:        now.Add(-time.Hour),
		To:          now,
	})
	if !errors.Is(err, communityreadcommand.ErrUnavailable) {
		t.Fatalf("ожидался ErrUnavailable, получено %v", err)
	}
}

func TestNilSliceContractsAreUnavailable(t *testing.T) {
	if _, err := NewCommunityDigest(nil).Get(context.Background(), communitydigest.GetQuery{}); !errors.Is(err, communitydigest.ErrUnavailable) {
		t.Fatalf("ожидался ErrUnavailable, получено %v", err)
	}
	if _, err := NewCommunityReadCommand(nil).Read(context.Background(), communityreadcommand.ReadCommand{}); !errors.Is(err, communityreadcommand.ErrUnavailable) {
		t.Fatalf("ожидался ErrUnavailable, получено %v", err)
	}
}
