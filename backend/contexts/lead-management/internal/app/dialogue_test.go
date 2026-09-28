package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"lead-gen/backend/platform/observability/observ"
	"lead-gen/contracts/integration-platform/llm"
)

// TestStreamReplyYieldsDeltasAndFinal проверяет, что потоковый ход отдаёт фрагменты
// текста и завершающий кадр с ответом.
func TestStreamReplyYieldsDeltasAndFinal(t *testing.T) {
	llmFake := &fakeLLM{converse: []llm.ConverseReply{{Text: "какой регион?", Ready: true}}}
	d := &ConverseDialogue{LLM: llmFake}
	history := []llm.Message{{Role: llm.RoleUser, Content: "ищем логистов"}}

	var text string
	var final *llm.ConverseReply
	for delta, err := range d.StreamReply(context.Background(), history) {
		if err != nil {
			t.Fatalf("StreamReply: %v", err)
		}
		text += delta.Text
		if delta.Reply != nil {
			final = delta.Reply
		}
	}
	if text != "какой регион?" {
		t.Fatalf("склеенный текст = %q, ожидалось %q", text, "какой регион?")
	}
	if final == nil || !final.Ready {
		t.Fatalf("нет завершающего кадра готовности: %+v", final)
	}
}

// TestStreamReplyUnavailable проверяет, что без LLM поток отдаёт единственный
// кадр-сбой, а не панику и не пустой результат.
func TestStreamReplyUnavailable(t *testing.T) {
	d := &ConverseDialogue{}
	var gotErr error
	for _, err := range d.StreamReply(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "q"}}) {
		if err != nil {
			gotErr = err
			break
		}
	}
	if !errors.Is(gotErr, errDialogueUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась errDialogueUnavailable", gotErr)
	}
}

// TestStreamReplyLimit проверяет, что при достижении предела ходов поток отдаёт
// единственный завершающий кадр с сообщением о пределе и не обращается к модели.
func TestStreamReplyLimit(t *testing.T) {
	llmFake := &fakeLLM{}
	d := &ConverseDialogue{LLM: llmFake, MaxTurns: 1}
	history := []llm.Message{
		{Role: llm.RoleUser, Content: "1"},
		{Role: llm.RoleAssistant, Content: "?"},
		{Role: llm.RoleUser, Content: "2"},
	}

	var final *llm.ConverseReply
	var frames int
	for delta, err := range d.StreamReply(context.Background(), history) {
		if err != nil {
			t.Fatalf("StreamReply: %v", err)
		}
		frames++
		if delta.Reply != nil {
			final = delta.Reply
		}
	}
	if frames != 1 || final == nil || final.Text != dialogueLimitMessage {
		t.Fatalf("кадров %d, финальный кадр %+v", frames, final)
	}
	if llmFake.converseCalls != 0 {
		t.Fatalf("при пределе ходов модель не должна вызываться, вызовов %d", llmFake.converseCalls)
	}
}

func TestConverseDialogueReply(t *testing.T) {
	llmFake := &fakeLLM{converse: []llm.ConverseReply{{
		Text:    "Какой регион нужен?",
		Targets: []llm.Target{{Query: "логистические компании"}},
	}}}
	d := &ConverseDialogue{LLM: llmFake, MaxTurns: 8}

	history := []llm.Message{{Role: llm.RoleUser, Content: "ищем логистов"}}
	reply, err := d.Reply(context.Background(), history)
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply.Text != "Какой регион нужен?" {
		t.Fatalf("реплика: получено %q", reply.Text)
	}
	if len(reply.Targets) != 1 || reply.Targets[0].Query != "логистические компании" {
		t.Fatalf("черновик целей искажён: %+v", reply.Targets)
	}
	if llmFake.converseCalls != 1 {
		t.Fatalf("Converse вызван %d раз, ожидался 1", llmFake.converseCalls)
	}
}

func TestConverseDialogueEnforcesTurnLimit(t *testing.T) {
	llmFake := &fakeLLM{converse: []llm.ConverseReply{{Text: "ещё вопрос?"}}}
	d := &ConverseDialogue{LLM: llmFake, MaxTurns: 2}

	history := []llm.Message{
		{Role: llm.RoleUser, Content: "раз"},
		{Role: llm.RoleAssistant, Content: "уточните"},
		{Role: llm.RoleUser, Content: "два"},
		{Role: llm.RoleAssistant, Content: "уточните ещё"},
		{Role: llm.RoleUser, Content: "три"},
	}
	reply, err := d.Reply(context.Background(), history)
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply.Text != dialogueLimitMessage {
		t.Fatalf("после предела ожидалось сообщение о пределе, получено %q", reply.Text)
	}
	if llmFake.converseCalls != 0 {
		t.Fatalf("Converse не должен вызываться после предела, вызван %d раз", llmFake.converseCalls)
	}
}

func TestConverseDialogueRejectsEmptyHistory(t *testing.T) {
	d := &ConverseDialogue{LLM: &fakeLLM{}}
	if _, err := d.Reply(context.Background(), nil); err == nil {
		t.Fatal("пустая история должна быть отвергнута")
	}
}

func TestConverseDialogueUnavailableWithoutLLM(t *testing.T) {
	d := &ConverseDialogue{}
	if _, err := d.Reply(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "q"}}); err == nil {
		t.Fatal("без LLM диалог должен быть недоступен")
	}
}

func TestConverseDialogueLogsSafeFieldsOnly(t *testing.T) {
	var buf strings.Builder
	llmFake := &fakeLLM{converse: []llm.ConverseReply{{Text: "секретная реплика", Targets: []llm.Target{{Query: "q"}}}}}
	d := &ConverseDialogue{LLM: llmFake, Logger: observ.NewLoggerWithWriter(&buf)}

	if _, err := d.Reply(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "секретный запрос оператора"}}); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if strings.Contains(buf.String(), "секретная реплика") || strings.Contains(buf.String(), "секретный запрос") {
		t.Fatalf("лог содержит текст реплик: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "ход диалога") {
		t.Fatalf("в логе нет записи о ходе диалога: %s", buf.String())
	}
}
