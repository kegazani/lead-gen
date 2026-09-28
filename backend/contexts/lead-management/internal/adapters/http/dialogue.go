package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/contracts/integration-platform/llm"
)

// errHistoryInvalid — история диалога от клиента не разбирается.
var errHistoryInvalid = errors.New("httpapi: история диалога некорректна")

// dialogueMessage — одно сообщение диалога в wire-формате клиента. Роль хранится
// строками llm.RoleUser/llm.RoleAssistant, чтобы транскрипт без преобразований
// доходил до порта LLM.
type dialogueMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// dialogueTarget — цель черновика плана в wire-формате клиента.
type dialogueTarget struct {
	Query string `json:"query,omitempty"`
	URL   string `json:"url,omitempty"`
}

// decodeHistory разбирает JSON-транскрипт из формы. Пустая строка даёт пустую
// историю; некорректный JSON — ошибку. Сервер историю не хранит: она приходит в
// каждом запросе и уходит обратно в форму.
func decodeHistory(raw string) ([]dialogueMessage, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var messages []dialogueMessage
	if err := json.Unmarshal([]byte(raw), &messages); err != nil {
		return nil, errHistoryInvalid
	}
	return messages, nil
}

// encodeHistory кодирует транскрипт в JSON для скрытого поля формы.
func encodeHistory(messages []dialogueMessage) string {
	if len(messages) == 0 {
		return ""
	}
	raw, err := json.Marshal(messages)
	if err != nil {
		return ""
	}
	return string(raw)
}

// decodeTargets разбирает JSON-черновик плана из формы; пустая строка и
// некорректный JSON дают пустой черновик.
func decodeTargets(raw string) []dialogueTarget {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var targets []dialogueTarget
	if err := json.Unmarshal([]byte(raw), &targets); err != nil {
		return nil
	}
	return targets
}

// decodePlanTargets разбирает форму плана в цели плана задания и проверяет её как
// недоверенный ввод: неразбираемая форма и цель, нарушающая «ровно один из
// query/url», дают ошибку. Пустая форма — легальный случай «плана ещё нет».
func decodePlanTargets(raw string) ([]ports.PlanTarget, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var targets []ports.PlanTarget
	if err := json.Unmarshal([]byte(raw), &targets); err != nil {
		return nil, fmt.Errorf("форма плана не разбирается: %w", err)
	}
	for i, t := range targets {
		if err := t.Validate(); err != nil {
			return nil, fmt.Errorf("цель плана %d: %w", i+1, err)
		}
	}
	return targets, nil
}

// encodeTargets кодирует черновик плана в JSON для скрытого поля формы.
func encodeTargets(targets []dialogueTarget) string {
	if len(targets) == 0 {
		return ""
	}
	raw, err := json.Marshal(targets)
	if err != nil {
		return ""
	}
	return string(raw)
}

// decodeManualTargets разбирает ручной ввод целей. Поддерживаются две формы:
// JSON-массив целей (та же форма, что и черновик плана, — decodePlanTargets) и
// список строк (по одной цели на строку): строка, начинающаяся со схемы http(s)
// или //, считается стартовым URL, иначе — поисковым запросом. Пустой ввод даёт
// пустой набор (цели определяются планированием по запросу). Любая форма
// проверяется как недоверенный ввод.
func decodeManualTargets(raw string) ([]ports.PlanTarget, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	if strings.HasPrefix(trimmed, "[") {
		return decodePlanTargets(trimmed)
	}
	lines := strings.Split(trimmed, "\n")
	targets := make([]ports.PlanTarget, 0, len(lines))
	for i, line := range lines {
		value := strings.TrimSpace(line)
		if value == "" {
			continue
		}
		var t ports.PlanTarget
		if isTargetURL(value) {
			t = ports.PlanTarget{URL: value}
		} else {
			t = ports.PlanTarget{Query: value}
		}
		if err := t.Validate(); err != nil {
			return nil, fmt.Errorf("своя цель %d: %w", i+1, err)
		}
		targets = append(targets, t)
	}
	return targets, nil
}

// isTargetURL сообщает, задаёт ли строка стартовый URL (а не поисковый запрос).
func isTargetURL(value string) bool {
	lower := strings.ToLower(value)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "//")
}

// toLLMHistory переводит транскрипт клиента в сообщения порта LLM.
func toLLMHistory(messages []dialogueMessage) []llm.Message {
	out := make([]llm.Message, 0, len(messages))
	for _, m := range messages {
		out = append(out, llm.Message{Role: m.Role, Content: m.Content})
	}
	return out
}

// fromLLMTargets переводит черновик целей порта в wire-формат клиента.
func fromLLMTargets(targets []llm.Target) []dialogueTarget {
	out := make([]dialogueTarget, 0, len(targets))
	for _, t := range targets {
		out = append(out, dialogueTarget{Query: t.Query, URL: t.URL})
	}
	return out
}

// criteriaFromHistory собирает критерии запуска из ходов оператора: финальный
// Plan получает текст из ответов оператора, а не транскрипт целиком.
func criteriaFromHistory(messages []dialogueMessage) string {
	parts := make([]string, 0, len(messages))
	for _, m := range messages {
		if m.Role != llm.RoleUser {
			continue
		}
		if text := strings.TrimSpace(m.Content); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

// assistantMessagesToRows отображает транскрипт в строки ленты диалога.
func assistantMessagesToRows(messages []dialogueMessage) []dialogueMessageRow {
	rows := make([]dialogueMessageRow, 0, len(messages))
	for _, m := range messages {
		role := "agent"
		if m.Role == llm.RoleUser {
			role = "operator"
		}
		rows = append(rows, dialogueMessageRow{Role: role, Text: m.Content})
	}
	return rows
}

// assistantTargetsToRows отображает черновик плана в строки панели плана.
func assistantTargetsToRows(targets []dialogueTarget) []dialogueTargetRow {
	rows := make([]dialogueTargetRow, 0, len(targets))
	for _, t := range targets {
		if query := strings.TrimSpace(t.Query); query != "" {
			rows = append(rows, dialogueTargetRow{Kind: "Запрос", Value: query})
			continue
		}
		if url := strings.TrimSpace(t.URL); url != "" {
			rows = append(rows, dialogueTargetRow{Kind: "URL", Value: url})
		}
	}
	return rows
}
