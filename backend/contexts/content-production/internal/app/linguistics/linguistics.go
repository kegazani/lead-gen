// Package linguistics реализует capability linguistics в контексте
// content-production: генерацию и переформулирование текста лендинга средствами
// порта LLM владельца integration-platform.
//
// Своего механизма генерации здесь нет: порт LLM впрыскивается точкой сборки
// (cmd/lead-gen). Без порта генерация честно недоступна — текст не выдумывается.
// Ответ модели разбирается в заголовок и текст, а сведения, которых нет в
// материалах клиента (числа, сроки, гарантии, результаты), отвергаются доменной
// проверкой, а не выдаются за проверенный текст.
package linguistics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"lead-gen/backend/contexts/content-production/internal/domain"
	"lead-gen/contracts/integration-platform/llm"
)

// Ошибки capability linguistics. Генерация не подменяется выдуманным текстом:
// каждый отказ называет причину.
var (
	// ErrUnavailable — порт LLM не сконфигурирован или вызов не удался.
	ErrUnavailable = errors.New("linguistics: генерация текста недоступна: LLM не сконфигурирован")

	// ErrEmptyReply — модель не вернула текст. Пустой ответ не заменяется
	// заготовкой.
	ErrEmptyReply = errors.New("linguistics: LLM не вернул текст")

	// ErrInvalidRequest — запрос генерации некорректен (нет роли, пустой текст).
	ErrInvalidRequest = errors.New("linguistics: запрос генерации некорректен")

	// ErrUnsourcedClaims — текст содержит сведения, отсутствующие в материалах
	// клиента. Такой текст не выдаётся: места остаются незаполненными, а причина
	// сообщается оператору.
	ErrUnsourcedClaims = errors.New("linguistics: текст содержит сведения, отсутствующие в материалах клиента")
)

// Generator — генератор текста за портом LLM. nil-порт означает недоступность
// генерации.
type Generator struct {
	// LLM — порт LLM владельца integration-platform; nil — генерация недоступна.
	LLM llm.Port
}

// New собирает генератор поверх порта LLM.
func New(port llm.Port) *Generator { return &Generator{LLM: port} }

// GenerateRequest — вход генерации текста одного блока лендинга.
type GenerateRequest struct {
	// Role — роль блока: определяет назначение текста.
	Role domain.LandingBlockRole
	// Materials — материалы клиента: единственный источник сведений.
	Materials domain.LandingMaterials
	// Tone — желаемая тональность; пусто — значение по умолчанию промпта.
	Tone string
	// Language — язык текста; пусто — русский.
	Language string
}

// GeneratedText — порождённый текст блока и расход токенов вызова.
type GeneratedText struct {
	Role    domain.LandingBlockRole
	Heading string
	Body    string
	Usage   llm.Usage
}

// Generate порождает текст блока по материалам клиента, заданным тональности и
// языку. Отсутствие порта LLM, пустой ответ и сведения, которых нет в материалах,
// дают отказ: текст не выдумывается.
func (g *Generator) Generate(ctx context.Context, req GenerateRequest) (GeneratedText, error) {
	if _, err := domain.NewLandingBlock(req.Role); err != nil {
		return GeneratedText{}, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	if g == nil || g.LLM == nil {
		return GeneratedText{}, ErrUnavailable
	}

	prompt := buildBlockPrompt(req)
	reply, usage, err := g.LLM.Converse(ctx, llm.ConverseRequest{
		History: []llm.Message{{Role: llm.RoleUser, Content: prompt}},
	})
	if err != nil {
		return GeneratedText{}, fmt.Errorf("%w: вызов LLM: %v", ErrUnavailable, err)
	}

	heading, body := parseBlockReply(reply.Text)
	if strings.TrimSpace(body) == "" {
		return GeneratedText{}, ErrEmptyReply
	}
	if claims := domain.UnsourcedClaims(strings.TrimSpace(heading+"\n"+body), req.Materials); len(claims) > 0 {
		return GeneratedText{}, fmt.Errorf("%w: %s", ErrUnsourcedClaims, strings.Join(claims, ", "))
	}
	return GeneratedText{
		Role:    req.Role,
		Heading: strings.TrimSpace(heading),
		Body:    strings.TrimSpace(body),
		Usage:   usage,
	}, nil
}

// RephraseRequest — вход переформулирования фрагмента.
type RephraseRequest struct {
	// Text — фрагмент, подлежащий переформулированию.
	Text string
	// Language — язык результата; пусто — русский.
	Language string
	// Tone — желаемая тональность; пусто — значение по умолчанию промпта.
	Tone string
}

// Rephrase переформулирует фрагмент, сохраняя смысл и не добавляя сведений.
// Отсутствие порта LLM и пустой ответ дают отказ: фрагмент не подменяется.
func (g *Generator) Rephrase(ctx context.Context, req RephraseRequest) (string, llm.Usage, error) {
	text := strings.TrimSpace(req.Text)
	if text == "" {
		return "", llm.Usage{}, fmt.Errorf("%w: текст для переформулирования пуст", ErrInvalidRequest)
	}
	if g == nil || g.LLM == nil {
		return "", llm.Usage{}, ErrUnavailable
	}

	reply, usage, err := g.LLM.Converse(ctx, llm.ConverseRequest{
		History: []llm.Message{{Role: llm.RoleUser, Content: buildRephrasePrompt(req, text)}},
	})
	if err != nil {
		return "", llm.Usage{}, fmt.Errorf("%w: вызов LLM: %v", ErrUnavailable, err)
	}
	rephrased := strings.TrimSpace(reply.Text)
	if rephrased == "" {
		return "", llm.Usage{}, ErrEmptyReply
	}
	return rephrased, usage, nil
}

// buildBlockPrompt собирает промпт генерации блока: назначение блока, материалы и
// границы. В промпт не попадают персональные данные, а запрет на выдуманные
// сведения сформулирован явно.
func buildBlockPrompt(req GenerateRequest) string {
	var b strings.Builder
	b.WriteString("Ты — опытный копирайтер лендингов. Составь текст блока лендинга.\n")
	b.WriteString("Назначение блока: " + req.Role.Label() + ".\n\n")
	b.WriteString("Материалы клиента (единственный источник сведений):\n")
	if site := strings.TrimSpace(req.Materials.SiteURL()); site != "" {
		b.WriteString("Сайт: " + site + "\n")
	}
	b.WriteString("О бизнесе: " + req.Materials.BusinessDescription() + "\n")
	if product := strings.TrimSpace(req.Materials.ProductInfo()); product != "" {
		b.WriteString("О продукте или услуге: " + product + "\n")
	}
	for i, document := range req.Materials.Documents() {
		b.WriteString(fmt.Sprintf("Документ %d: %s\n", i+1, document))
	}

	language := strings.TrimSpace(req.Language)
	if language == "" {
		language = "русский"
	}
	b.WriteString("\nТребования: язык — " + language)
	if tone := strings.TrimSpace(req.Tone); tone != "" {
		b.WriteString(", тональность — " + tone)
	}
	b.WriteString(".\n")
	b.WriteString("Используй только сведения из материалов. Не приводи числа, сроки, гарантии и результаты, " +
		"если их нет в материалах: соответствующие места не заполняй.\n")
	b.WriteString("Не упоминай имена, личные контакты и фотографии сотрудников.\n")
	b.WriteString("Верни JSON-объект {\"heading\": \"...\", \"body\": \"...\"} без пояснений и Markdown.")
	return b.String()
}

// buildRephrasePrompt собирает промпт переформулирования: смысл сохраняется,
// новые сведения не добавляются.
func buildRephrasePrompt(req RephraseRequest, text string) string {
	language := strings.TrimSpace(req.Language)
	if language == "" {
		language = "русский"
	}
	var b strings.Builder
	b.WriteString("Переформулируй фрагмент текста лендинга, сохранив смысл и не добавляя новых сведений " +
		"(чисел, сроков, гарантий, результатов).\n")
	b.WriteString("Язык результата: " + language)
	if tone := strings.TrimSpace(req.Tone); tone != "" {
		b.WriteString(", тональность — " + tone)
	}
	b.WriteString(".\n\nФрагмент:\n")
	b.WriteString(text)
	b.WriteString("\n\nВерни только переформулированный текст без пояснений.")
	return b.String()
}

// blockReply — форма JSON-ответа модели.
type blockReply struct {
	Heading string `json:"heading"`
	Body    string `json:"body"`
}

// parseBlockReply разбирает ответ модели в заголовок и текст. Принимаются две
// формы: JSON-объект {"heading","body"} и текст, где первая строка — заголовок.
// Неразобранная форма честно отдаёт весь текст телом, не выдумывая заголовок.
func parseBlockReply(text string) (heading, body string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", ""
	}
	var payload blockReply
	if err := json.Unmarshal([]byte(extractJSONObject(text)), &payload); err == nil && strings.TrimSpace(payload.Body) != "" {
		return strings.TrimSpace(payload.Heading), strings.TrimSpace(payload.Body)
	}

	lines := strings.SplitN(text, "\n", 2)
	if len(lines) == 1 {
		return "", text
	}
	return trimHeadingPrefix(lines[0]), strings.TrimSpace(lines[1])
}

// extractJSONObject выделяет первый JSON-объект из ответа модели: адаптер LLM
// может вернуть обрамление вокруг объекта.
func extractJSONObject(content string) string {
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start == -1 || end == -1 || end < start {
		return content
	}
	return content[start : end+1]
}

// trimHeadingPrefix снимает необязательный префикс заголовка, если модель его
// добавила.
func trimHeadingPrefix(line string) string {
	line = strings.TrimSpace(line)
	for _, prefix := range []string{"Заголовок:", "заголовок:", "Heading:", "heading:"} {
		if rest, ok := strings.CutPrefix(line, prefix); ok {
			return strings.TrimSpace(rest)
		}
	}
	return line
}
