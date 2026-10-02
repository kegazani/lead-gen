package domain

import (
	"errors"
	"strings"
	"testing"
)

func candidate(text, message string) Candidate {
	return Candidate{
		Text: text,
		Ref:  MessageRef{Community: "c-1", Message: MessageID(message)},
	}
}

func TestBuildTopicsGroupsRepeatedFormulations(t *testing.T) {
	topics, err := BuildTopics([]Candidate{
		candidate("Нужна цена", "m-1"),
		candidate("нужна цена!", "m-2"),
		candidate("  Нужна цена  ", "m-3"),
		candidate("Есть ли скидка", "m-4"),
	}, BuildTopicsOptions{CommunitySize: 100, QuoteThreshold: 10})
	if err != nil {
		t.Fatalf("BuildTopics: %v", err)
	}
	if len(topics) != 2 {
		t.Fatalf("ожидалось 2 темы, получено %d", len(topics))
	}
	if topics[0].Mentions() != 3 {
		t.Fatalf("частота первой темы = %d, ожидалось 3", topics[0].Mentions())
	}
	if len(topics[0].Sources()) != 3 {
		t.Fatalf("источников первой темы = %d, ожидалось 3", len(topics[0].Sources()))
	}
}

func TestBuildTopicsDropsQuotesBelowThreshold(t *testing.T) {
	topics, err := BuildTopics([]Candidate{
		candidate("Нужна цена", "m-1"),
	}, BuildTopicsOptions{CommunitySize: 3, QuoteThreshold: 10})
	if err != nil {
		t.Fatalf("BuildTopics: %v", err)
	}
	if len(topics[0].Quotes()) != 0 {
		t.Fatalf("цитаты ниже порога недопустимы, получено %d", len(topics[0].Quotes()))
	}
	if len(topics[0].Sources()) != 1 {
		t.Fatal("ссылка на источник сохраняется независимо от порога")
	}
}

func TestBuildTopicsIncludesQuotesAtOrAboveThreshold(t *testing.T) {
	topics, err := BuildTopics([]Candidate{
		candidate("Нужна цена", "m-1"),
	}, BuildTopicsOptions{CommunitySize: 10, QuoteThreshold: 10})
	if err != nil {
		t.Fatalf("BuildTopics: %v", err)
	}
	if len(topics[0].Quotes()) != 1 {
		t.Fatalf("ожидалась 1 цитата на пороге, получено %d", len(topics[0].Quotes()))
	}
}

func TestBuildTopicsSanitizesIdentifiers(t *testing.T) {
	topics, err := BuildTopics([]Candidate{
		candidate("Пишите @ivan_petrov или на ivan@example.com, ссылка https://t.me/join", "m-1"),
	}, BuildTopicsOptions{CommunitySize: 100, QuoteThreshold: 10})
	if err != nil {
		t.Fatalf("BuildTopics: %v", err)
	}
	quote := topics[0].Quotes()[0].Text()
	for _, forbidden := range []string{"@ivan_petrov", "ivan@example.com", "https://t.me"} {
		if strings.Contains(quote, forbidden) {
			t.Fatalf("цитата содержит идентификатор %q: %q", forbidden, quote)
		}
	}
}

func TestBuildTopicsWithoutCandidatesFails(t *testing.T) {
	if _, err := BuildTopics(nil, BuildTopicsOptions{}); !errors.Is(err, ErrNoMessages) {
		t.Fatalf("ожидался ErrNoMessages, получено %v", err)
	}
}

func TestBuildTopicsOrderIsDeterministic(t *testing.T) {
	input := []Candidate{
		candidate("Б", "m-1"),
		candidate("А", "m-2"),
	}
	first, err := BuildTopics(input, BuildTopicsOptions{CommunitySize: 100, QuoteThreshold: -1})
	if err != nil {
		t.Fatalf("BuildTopics: %v", err)
	}
	second, err := BuildTopics(input, BuildTopicsOptions{CommunitySize: 100, QuoteThreshold: -1})
	if err != nil {
		t.Fatalf("BuildTopics: %v", err)
	}
	if first[0].Name() != second[0].Name() {
		t.Fatalf("непорядок: %q против %q", first[0].Name(), second[0].Name())
	}
}

func TestSanitizeCollapsesWhitespace(t *testing.T) {
	if got := Sanitize("  цена    вопроса \n"); got != "цена вопроса" {
		t.Fatalf("Sanitize = %q", got)
	}
}
