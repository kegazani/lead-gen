package domain

import (
	"errors"
	"testing"
)

func testSource(t *testing.T, id CommunityID) CommunitySource {
	t.Helper()
	source, err := NewCommunitySource(NewCommunitySourceParams{
		ID:         id,
		Title:      "Сообщество " + id.String(),
		Kind:       SourceUserAccount,
		Authorized: true,
	})
	if err != nil {
		t.Fatalf("NewCommunitySource: %v", err)
	}
	return source
}

func TestNewCommunitySourceAcceptsAuthorizedUserAccount(t *testing.T) {
	source := testSource(t, "c-1")
	if source.ID() != "c-1" {
		t.Fatalf("ID = %q", source.ID())
	}
	if !source.IsAuthorized() {
		t.Fatal("источник должен быть разрешён")
	}
	if source.Kind() != SourceUserAccount {
		t.Fatalf("Kind = %q", source.Kind())
	}
}

func TestNewCommunitySourceRejectsBot(t *testing.T) {
	_, err := NewCommunitySource(NewCommunitySourceParams{
		ID:         "c-1",
		Title:      "Канал",
		Kind:       SourceBot,
		Authorized: true,
	})
	if !errors.Is(err, ErrBotSourceRejected) {
		t.Fatalf("ожидался ErrBotSourceRejected, получено %v", err)
	}
}

func TestNewCommunitySourceRejectsUnauthorized(t *testing.T) {
	_, err := NewCommunitySource(NewCommunitySourceParams{
		ID:         "c-1",
		Title:      "Канал",
		Kind:       SourceUserAccount,
		Authorized: false,
	})
	if !errors.Is(err, ErrSourceNotAuthorized) {
		t.Fatalf("ожидался ErrSourceNotAuthorized, получено %v", err)
	}
}

func TestNewCommunitySourceRejectsUnknownKind(t *testing.T) {
	_, err := NewCommunitySource(NewCommunitySourceParams{
		ID:         "c-1",
		Title:      "Канал",
		Kind:       SourceKind("rss"),
		Authorized: true,
	})
	if !errors.Is(err, ErrUnknownSourceKind) {
		t.Fatalf("ожидался ErrUnknownSourceKind, получено %v", err)
	}
}

func TestNewCommunitySourceRejectsEmptyTitle(t *testing.T) {
	_, err := NewCommunitySource(NewCommunitySourceParams{
		ID:         "c-1",
		Title:      "   ",
		Kind:       SourceUserAccount,
		Authorized: true,
	})
	if !errors.Is(err, ErrEmptySourceTitle) {
		t.Fatalf("ожидался ErrEmptySourceTitle, получено %v", err)
	}
}

func TestRehydrateCommunitySourceRejectsBot(t *testing.T) {
	_, err := RehydrateCommunitySource(CommunitySourceSnapshot{
		ID:         "c-1",
		Title:      "Канал",
		Kind:       SourceBot,
		Authorized: true,
	})
	if !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("ожидался ErrInvalidSnapshot, получено %v", err)
	}
}
