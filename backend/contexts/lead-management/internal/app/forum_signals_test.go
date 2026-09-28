package app

import (
	"context"
	"testing"

	"lead-gen/contracts/integration-platform/forum"
)

func TestSearchForumSignals(t *testing.T) {
	s := &SearchForumSignals{Forum: &fakeForum{signals: []forum.Signal{
		{Topic: "тема", SourceURL: "https://example.com/t/1"},
	}}}

	got, err := s.Search(context.Background(), "боли агентств")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("сигналов %d, ожидался 1", len(got))
	}
}

func TestSearchForumSignalsEmptyQuery(t *testing.T) {
	s := &SearchForumSignals{Forum: &fakeForum{}}
	if _, err := s.Search(context.Background(), "  "); err != errEmptyQuery {
		t.Fatalf("пустой запрос: ошибка %v, ожидалась errEmptyQuery", err)
	}
}

func TestAnonymizeSignalsExcludesIdentifiers(t *testing.T) {
	signals := []forum.Signal{
		{Topic: "обычная тема", SourceURL: "https://example.com/t/1"},
		{Topic: "тема с адресом", Summary: "пишите на ivan@example.com", SourceURL: "https://example.com/t/2"},
		{Topic: "тема с телефоном", Summary: "звоните +7 999 123-45-67", SourceURL: "https://example.com/t/3"},
	}

	got := anonymizeSignals(signals)
	if len(got) != 1 || got[0].Topic != "обычная тема" {
		t.Fatalf("после анонимизации ожидался 1 сигнал, получено %+v", got)
	}
}
