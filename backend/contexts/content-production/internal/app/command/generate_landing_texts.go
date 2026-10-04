package command

import (
	"context"
	"fmt"
	"strings"
	"time"

	"lead-gen/backend/contexts/content-production/internal/app/linguistics"
	"lead-gen/backend/contexts/content-production/internal/domain"
)

// GenerateLandingTexts — команда генерации текстов блоков лендинга по материалам
// клиента (capability landing-page-generator, требование «Тексты наполнения»).
//
// Тексты порождает capability linguistics: своего механизма генерации сценарий не
// содержит. Генерация запускается только после утверждения структуры оператором.
// Сведения, которых нет в материалах (числа, сроки, гарантии, результаты), не
// добавляются: доменная проверка отвергает такой текст, а не выдаёт его за
// проверенный.
type GenerateLandingTexts struct {
	// Материалы клиента. Обязательно описание бизнеса.
	SiteURL             string
	BusinessDescription string
	ProductInfo         string
	Documents           []string

	// SiteText — сведения, собранные с сайта клиента. Задан — сверяются с
	// документами; расхождения сообщаются оператору без выбора источника.
	SiteText string

	// Tone — тональность текстов; пусто — значение по умолчанию промпта.
	Tone string
	// Language — язык текстов; пусто — русский.
	Language string

	// Approved — структура показана и утверждена оператором. Без утверждения
	// генерация не запускается.
	Approved bool
	// ApprovedBy и ApprovedAt фиксируют автора и время утверждения.
	ApprovedBy domain.ActorID
	ApprovedAt time.Time
}

// LandingSectionView — порождённый текст блока.
type LandingSectionView struct {
	Role    string `json:"role"`
	Heading string `json:"heading"`
	Body    string `json:"body"`
}

// GenerateLandingTextsResult — порождённое наполнение лендинга.
type GenerateLandingTextsResult struct {
	Title    string               `json:"title"`
	Headline string               `json:"headline"`
	Intro    string               `json:"intro"`
	Sections []LandingSectionView `json:"sections"`
	// Discrepancies — расхождения между сайтом и документами: система сообщает о
	// них и не выбирает источник сама.
	Discrepancies []string `json:"discrepancies,omitempty"`
	// TotalTokens — суммарный расход токенов LLM по блокам.
	TotalTokens int `json:"total_tokens"`
}

// GenerateLandingTextsHandler — use case генерации текстов.
type GenerateLandingTextsHandler struct {
	texts *linguistics.Generator
}

// NewGenerateLandingTextsHandler собирает use case поверх capability linguistics.
func NewGenerateLandingTextsHandler(texts *linguistics.Generator) *GenerateLandingTextsHandler {
	return &GenerateLandingTextsHandler{texts: texts}
}

// Handle строит структуру по материалам, проверяет утверждение и порождает текст
// каждого блока через linguistics. Без утверждённой структуры или без порта LLM
// возвращается отказ: текст не выдумывается.
func (h *GenerateLandingTextsHandler) Handle(ctx context.Context, cmd GenerateLandingTexts) (GenerateLandingTextsResult, error) {
	materials, err := domain.NewLandingMaterials(cmd.SiteURL, cmd.BusinessDescription, cmd.ProductInfo, cmd.Documents)
	if err != nil {
		return GenerateLandingTextsResult{}, err
	}
	structure, err := domain.BuildLandingStructure(materials)
	if err != nil {
		return GenerateLandingTextsResult{}, err
	}
	if cmd.Approved {
		if err := structure.Approve(cmd.ApprovedBy, cmd.ApprovedAt); err != nil {
			return GenerateLandingTextsResult{}, err
		}
	}
	if err := structure.EnsureApproved(); err != nil {
		return GenerateLandingTextsResult{}, err
	}
	if h.texts == nil {
		return GenerateLandingTextsResult{}, linguistics.ErrUnavailable
	}

	result := GenerateLandingTextsResult{
		Sections:      make([]LandingSectionView, 0, len(structure.Blocks())),
		Discrepancies: domain.DetectDiscrepancies(cmd.SiteText, cmd.Documents),
	}
	var offerHeading, firstHeading string
	for index, block := range structure.Blocks() {
		generated, err := h.texts.Generate(ctx, linguistics.GenerateRequest{
			Role:      block.Role(),
			Materials: materials,
			Tone:      cmd.Tone,
			Language:  cmd.Language,
		})
		if err != nil {
			return GenerateLandingTextsResult{}, fmt.Errorf("блок %q: %w", block.Label(), err)
		}
		result.Sections = append(result.Sections, LandingSectionView{
			Role:    string(block.Role()),
			Heading: block.Label(),
			Body:    generated.Body,
		})
		result.TotalTokens += generated.Usage.TotalTokens
		if index == 0 {
			firstHeading = generated.Heading
			result.Intro = generated.Body
		}
		if block.Role() == domain.LandingBlockOffer {
			offerHeading = generated.Heading
		}
	}

	result.Title = firstNonEmpty(offerHeading, firstHeading)
	result.Headline = result.Title
	if strings.TrimSpace(result.Title) == "" {
		result.Title = firstLine(materials.BusinessDescription())
		result.Headline = result.Title
	}
	return result, nil
}

// firstNonEmpty возвращает первую непустую строку после обрезки пробелов.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// firstLine возвращает первую непустую строку текста.
func firstLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
