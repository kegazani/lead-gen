package app

import (
	"context"
	"errors"
	"strings"

	"lead-gen/contracts/integration-platform/llm"
)

// errEmptyDescription — описание задачи для подбора ниш не может быть пустым.
var errEmptyDescription = errors.New("app: описание задачи обязательно")

// SuggestNiches — use case подбора секторов, ниш и готовых seed-запросов по
// описанию задачи оператора через llm.Port.Suggest. Предложения — временный
// результат app-слоя, не владение состоянием.
type SuggestNiches struct {
	LLM llm.Port
}

// Suggest возвращает предложения по описанию бизнеса/задачи.
func (s *SuggestNiches) Suggest(ctx context.Context, description string) ([]llm.Suggestion, error) {
	description = strings.TrimSpace(description)
	if description == "" {
		return nil, errEmptyDescription
	}
	suggestions, _, err := s.LLM.Suggest(ctx, llm.SuggestRequest{Description: description})
	return suggestions, err
}
