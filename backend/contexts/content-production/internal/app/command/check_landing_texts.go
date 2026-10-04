package command

import (
	"context"

	"lead-gen/backend/contexts/content-production/internal/app/linguistics"
	"lead-gen/backend/contexts/content-production/internal/domain"
	"lead-gen/backend/contexts/content-production/internal/ports"
)

// CheckLandingTexts — команда проверки сформированных текстов на заимствование
// (capability landing-page-generator, требование «Проверка на заимствование»).
//
// Текст сравнивается с материалами клиента (доступны локально) и со сторонними
// источниками (движок 3.1, за портом). Обнаруженный фрагмент переформулируется
// capability linguistics и не выдаётся дословно. Реквизиты, наименование компании
// и общеупотребительные термины не переформулируются: совпадение здесь законно.
//
// Без подключённого порта сторонних источников сравнение с ними честно недоступно —
// проверка не выдаётся за полную.
type CheckLandingTexts struct {
	// Text — проверяемый текст наполнения.
	Text string
	// Sources — исходные материалы клиента для сравнения.
	Sources []string
	// Protected — исключения: наименование, реквизиты, общеупотребительные термины.
	Protected []string
	// Language — язык переформулирования; пусто — русский.
	Language string
	// Tone — тональность переформулирования; пусто — значение по умолчанию промпта.
	Tone string
}

// LandingTextMatch — обнаруженный фрагмент и результат его замены.
type LandingTextMatch struct {
	Fragment    string `json:"fragment"`
	Replacement string `json:"replacement,omitempty"`
	// Rephrased — фрагмент заменён; false означает, что замена не выполнена.
	Rephrased bool `json:"rephrased"`
}

// CheckLandingTextsResult — итог проверки на заимствование.
type CheckLandingTextsResult struct {
	Matches []LandingTextMatch `json:"matches"`
	// ExternalUnavailable — сравнение со сторонними источниками недоступно.
	ExternalUnavailable bool   `json:"external_unavailable"`
	ExternalReason      string `json:"external_reason,omitempty"`
	// RewriteUnavailable — совпадения есть, но переформулирование недоступно.
	RewriteUnavailable bool `json:"rewrite_unavailable"`
}

// CheckLandingTextsHandler — use case проверки на заимствование.
type CheckLandingTextsHandler struct {
	texts   *linguistics.Generator
	sources ports.PlagiarismSource
}

// NewCheckLandingTextsHandler собирает use case поверх capability linguistics и
// порта сторонних источников.
func NewCheckLandingTextsHandler(texts *linguistics.Generator, sources ports.PlagiarismSource) *CheckLandingTextsHandler {
	return &CheckLandingTextsHandler{texts: texts, sources: sources}
}

// Handle находит дословные совпадения и переформулирует их. Источники и
// недоступность переформулирования не скрываются: результат называет, что именно
// проверено, а что нет.
func (h *CheckLandingTextsHandler) Handle(ctx context.Context, cmd CheckLandingTexts) (CheckLandingTextsResult, error) {
	var result CheckLandingTextsResult

	set := make(map[string]struct{})
	var fragments []string
	collect := func(items []string) {
		for _, item := range items {
			if _, ok := set[item]; ok {
				continue
			}
			set[item] = struct{}{}
			fragments = append(fragments, item)
		}
	}
	collect(domain.VerbatimMatches(cmd.Text, cmd.Sources, cmd.Protected))

	if h.sources == nil {
		result.ExternalUnavailable = true
		result.ExternalReason = ports.ErrSourcesUnavailable.Error()
	} else {
		external, err := h.sources.Match(ctx, cmd.Text)
		if err != nil {
			result.ExternalUnavailable = true
			result.ExternalReason = err.Error()
		} else {
			collect(external)
		}
	}

	if h.texts == nil && len(fragments) > 0 {
		result.RewriteUnavailable = true
	}
	for _, fragment := range fragments {
		match := LandingTextMatch{Fragment: fragment}
		if h.texts != nil {
			replacement, _, err := h.texts.Rephrase(ctx, linguistics.RephraseRequest{
				Text:     fragment,
				Language: cmd.Language,
				Tone:     cmd.Tone,
			})
			if err != nil {
				result.RewriteUnavailable = true
			} else if replacement != "" && replacement != fragment {
				match.Replacement = replacement
				match.Rephrased = true
			}
		}
		result.Matches = append(result.Matches, match)
	}
	return result, nil
}
