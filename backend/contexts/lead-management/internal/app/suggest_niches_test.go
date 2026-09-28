package app

import (
	"context"
	"testing"

	"lead-gen/contracts/integration-platform/llm"
)

func TestSuggestNiches(t *testing.T) {
	s := &SuggestNiches{LLM: &fakeLLM{suggestions: []llm.Suggestion{
		{Sector: "Логистика", Niche: "Сборные грузы", SeedQueries: []string{"перевозки сборных грузов"}},
	}}}

	got, err := s.Suggest(context.Background(), "помоги найти клиентов")
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if len(got) != 1 || got[0].Sector != "Логистика" || got[0].Niche != "Сборные грузы" {
		t.Fatalf("предложения: %+v", got)
	}
}

func TestSuggestNichesEmptyDescription(t *testing.T) {
	s := &SuggestNiches{LLM: &fakeLLM{}}
	if _, err := s.Suggest(context.Background(), "   "); err != errEmptyDescription {
		t.Fatalf("пустое описание: ошибка %v, ожидалась errEmptyDescription", err)
	}
}
