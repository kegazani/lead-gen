package command

import (
	"context"
	"fmt"
	"strings"

	"lead-gen/contracts/integration-platform/documentparse"
)

// ParseDocuments — команда приёма и разбора предоставленных документов
// (capability landing-page-generator, требование «Приём материалов клиента»).
//
// Документы (например, коммерческое предложение) разбираются движком сбора данных
// за портом documentparse.Port, который живёт вне этого контекста и впрыскивается
// точкой сборки. Извлечённые тексты возвращаются в Documents и передаются
// генерации наполнения как материалы: без порта разбор честно недоступен, а
// недоступность сообщается, а не подменяется пустым успехом.
type ParseDocuments struct {
	// Sources — предоставленные документы. Непусто.
	Sources []DocumentSource
}

// DocumentSource — один предоставленный документ: источник и, при необходимости,
// подсказка формата.
type DocumentSource struct {
	// Source — адрес документа. Обязателен.
	Source string `json:"source"`
	// Format — подсказка формата (например, pdf). Необязательна.
	Format string `json:"format,omitempty"`
}

// ParsedDocument — разобранный документ: источник, формат и извлечённый текст.
type ParsedDocument struct {
	Source string `json:"source"`
	Format string `json:"format,omitempty"`
	Text   string `json:"text"`
}

// DocumentFailure — документ, который разобрать не удалось: честный отказ по
// конкретному источнику, не скрывающий его за общим успехом.
type DocumentFailure struct {
	Source string `json:"source"`
	Reason string `json:"reason"`
}

// ParseDocumentsResult — исход разбора. Available=false означает честную
// недоступность движка: разбор не выполнен.
type ParseDocumentsResult struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	// Documents — извлечённые тексты: готовы к передаче генерации наполнения
	// (command.GenerateLandingTexts.Documents / query.ProposeLandingStructure.Documents).
	Documents []string `json:"documents,omitempty"`
	// Parsed — разобранные документы с источником и форматом.
	Parsed []ParsedDocument `json:"parsed,omitempty"`
	// Failures — документы, которые разобрать не удалось.
	Failures []DocumentFailure `json:"failures,omitempty"`
}

// ParseDocumentsHandler — use case приёма и разбора документов.
type ParseDocumentsHandler struct {
	parser documentparse.Port
}

// NewParseDocumentsHandler собирает use case поверх порта разбора документов.
func NewParseDocumentsHandler(parser documentparse.Port) *ParseDocumentsHandler {
	return &ParseDocumentsHandler{parser: parser}
}

// Handle проверяет вход и разбирает каждый документ через движок. Порт не
// сконфигурирован — честная недоступность; разбор конкретного документа не удался —
// честная ошибка по этому источнику, остальные всё равно разбираются.
func (h *ParseDocumentsHandler) Handle(ctx context.Context, cmd ParseDocuments) (ParseDocumentsResult, error) {
	if len(cmd.Sources) == 0 {
		return ParseDocumentsResult{}, fmt.Errorf("%w: документы не предоставлены", ErrDocumentInput)
	}

	result := ParseDocumentsResult{}
	if h.parser == nil {
		result.Reason = "движок разбора документов не сконфигурирован: разбор недоступен, работа продолжается на предоставленных вручную материалах"
		return result, nil
	}
	result.Available = true

	for _, source := range cmd.Sources {
		trimmed := strings.TrimSpace(source.Source)
		if trimmed == "" {
			result.Failures = append(result.Failures, DocumentFailure{Source: source.Source, Reason: "источник документа не задан"})
			continue
		}
		document, err := h.parser.ParseDocument(ctx, documentparse.Request{Source: trimmed, Format: strings.TrimSpace(source.Format)})
		if err != nil {
			result.Failures = append(result.Failures, DocumentFailure{Source: trimmed, Reason: documentFailureReason(err)})
			continue
		}
		text := strings.TrimSpace(document.Text)
		if text == "" {
			result.Failures = append(result.Failures, DocumentFailure{Source: trimmed, Reason: "документ разобран, но текст не извлечён"})
			continue
		}
		result.Documents = append(result.Documents, text)
		result.Parsed = append(result.Parsed, ParsedDocument{
			Source: trimmed,
			Format: strings.TrimSpace(source.Format),
			Text:   text,
		})
	}
	return result, nil
}

// documentFailureReason называет причину отказа по документу, не притворяясь
// успехом. Технические детали движка сохраняются для диагностики, но результат
// остаётся честным отказом по конкретному источнику.
func documentFailureReason(err error) string {
	return "документ недоступен или не удалось разобрать: " + err.Error()
}
