// Package contracttest содержит общие contract suite портов
// integration-platform: search, crawl, llm, mail и forum.
//
// Suite запускается против любой реализации через фабрику и потому не зависит
// от адаптера: фейковые и реальные (Firecrawl, OpenAI-совместимый) проверяются
// одним и тем же набором проверок. Проверяются общие семантики портов: уважение
// context.Context (дедлайн и отмена), повторный вызов и валидность результатов.
// Отсутствие утечки SDK-ошибок — структурное требование порта: адаптер возвращает
// обычную ошибку Go, а не тип из библиотеки поставщика.
package contracttest

import (
	"context"
	"fmt"
	"testing"

	"lead-gen/contracts/integration-platform/crawl"
	"lead-gen/contracts/integration-platform/forum"
	"lead-gen/contracts/integration-platform/llm"
	"lead-gen/contracts/integration-platform/mail"
	"lead-gen/contracts/integration-platform/search"
)

// SearchPortFactory создаёт реализацию search.Port для одного прогона suite.
type SearchPortFactory func(t *testing.T) search.Port

// CrawlPortFactory создаёт реализацию crawl.Port для одного прогона suite.
type CrawlPortFactory func(t *testing.T) crawl.Port

// LLMPortFactory создаёт реализацию llm.Port для одного прогона suite.
type LLMPortFactory func(t *testing.T) llm.Port

// LLMInterpretFactory создаёт реализацию llm.Port для одного прогона suite
// операции Interpret.
type LLMInterpretFactory func(t *testing.T) llm.Port

// LLMAssessFactory создаёт реализацию llm.Port для одного прогона suite
// операции Assess.
type LLMAssessFactory func(t *testing.T) llm.Port

// MailPortFactory создаёт реализацию mail.Port для одного прогона suite.
type MailPortFactory func(t *testing.T) mail.Port

// ForumPortFactory создаёт реализацию forum.Port для одного прогона suite.
type ForumPortFactory func(t *testing.T) forum.Port

// RunSearchPortContract проверяет семантику порта search.
func RunSearchPortContract(t *testing.T, factory SearchPortFactory) {
	t.Helper()

	t.Run("контекст отменён", func(t *testing.T) {
		port := factory(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := port.Search(ctx, search.Query{Text: "q"}); err == nil {
			t.Fatal("ожидалась ошибка отмены контекста")
		}
	})

	t.Run("повторный вызов", func(t *testing.T) {
		port := factory(t)
		for i := 0; i < 2; i++ {
			results, err := port.Search(context.Background(), search.Query{Text: "q"})
			if err != nil {
				t.Fatalf("вызов %d: %v", i, err)
			}
			for _, r := range results {
				if err := r.Validate(); err != nil {
					t.Fatalf("вызов %d: невалидный результат: %v", i, err)
				}
			}
		}
	})
}

// RunCrawlPortContract проверяет семантику порта crawl.
func RunCrawlPortContract(t *testing.T, factory CrawlPortFactory) {
	t.Helper()

	t.Run("контекст отменён", func(t *testing.T) {
		port := factory(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := port.Crawl(ctx, crawl.Request{StartURL: "https://example.com"}); err == nil {
			t.Fatal("ожидалась ошибка отмены контекста (Crawl)")
		}
		if _, err := port.Extract(ctx, crawl.ExtractRequest{URL: "https://example.com"}); err == nil {
			t.Fatal("ожидалась ошибка отмены контекста (Extract)")
		}
	})

	t.Run("повторный вызов", func(t *testing.T) {
		port := factory(t)
		for i := 0; i < 2; i++ {
			outcome, err := port.Crawl(context.Background(), crawl.Request{StartURL: "https://example.com"})
			if err != nil {
				t.Fatalf("Crawl %d: %v", i, err)
			}
			for _, p := range outcome.Pages {
				if err := p.Validate(); err != nil {
					t.Fatalf("Crawl %d: невалидная страница: %v", i, err)
				}
			}
			if _, err := port.Extract(context.Background(), crawl.ExtractRequest{URL: "https://example.com"}); err != nil {
				t.Fatalf("Extract %d: %v", i, err)
			}
		}
	})
}

// RunMailPortContract проверяет семантику порта mail: уважение context.Context
// (дедлайн и отмена) и повторный вызов. Отсутствие утечки SDK-ошибок —
// структурное требование порта: адаптер возвращает обычную ошибку Go, а не тип
// из библиотеки поставщика.
func RunMailPortContract(t *testing.T, factory MailPortFactory) {
	t.Helper()

	message := mail.Message{From: "a@example.com", To: "b@example.com", Subject: "тема", Body: "тело"}

	t.Run("контекст отменён", func(t *testing.T) {
		port := factory(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := port.Send(ctx, message); err == nil {
			t.Fatal("ожидалась ошибка отмены контекста")
		}
	})

	t.Run("повторный вызов", func(t *testing.T) {
		port := factory(t)
		for i := 0; i < 2; i++ {
			if err := port.Send(context.Background(), message); err != nil {
				t.Fatalf("вызов %d: %v", i, err)
			}
		}
	})
}

// RunLLMPortContract проверяет семантику порта llm.
func RunLLMPortContract(t *testing.T, factory LLMPortFactory) {
	t.Helper()

	t.Run("контекст отменён", func(t *testing.T) {
		port := factory(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, _, err := port.Plan(ctx, llm.PlanRequest{Query: "q"}); err == nil {
			t.Fatal("ожидалась ошибка отмены контекста")
		}
	})

	t.Run("повторный вызов", func(t *testing.T) {
		port := factory(t)
		for i := 0; i < 2; i++ {
			targets, _, err := port.Plan(context.Background(), llm.PlanRequest{Query: "q"})
			if err != nil {
				t.Fatalf("вызов %d: %v", i, err)
			}
			for _, target := range targets {
				if err := target.Validate(); err != nil {
					t.Fatalf("вызов %d: невалидный пункт плана: %v", i, err)
				}
			}
		}
	})
}

// RunLLMSuggestContract проверяет семантику операции Suggest порта llm: уважение
// context.Context (дедлайн и отмена) и повторный вызов с валидными результатами.
func RunLLMSuggestContract(t *testing.T, factory LLMPortFactory) {
	t.Helper()

	t.Run("контекст отменён", func(t *testing.T) {
		port := factory(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, _, err := port.Suggest(ctx, llm.SuggestRequest{Description: "описание"}); err == nil {
			t.Fatal("ожидалась ошибка отмены контекста (Suggest)")
		}
	})

	t.Run("повторный вызов", func(t *testing.T) {
		port := factory(t)
		for i := 0; i < 2; i++ {
			suggestions, _, err := port.Suggest(context.Background(), llm.SuggestRequest{Description: "описание"})
			if err != nil {
				t.Fatalf("вызов %d: %v", i, err)
			}
			for _, suggestion := range suggestions {
				if err := suggestion.Validate(); err != nil {
					t.Fatalf("вызов %d: невалидное предложение: %v", i, err)
				}
			}
		}
	})
}

// RunLLMConverseContract проверяет семантику операции Converse порта llm:
// уважение context.Context (дедлайн и отмена) и повторный вызов с валидной
// репликой.
func RunLLMConverseContract(t *testing.T, factory LLMPortFactory) {
	t.Helper()

	request := llm.ConverseRequest{History: []llm.Message{{Role: llm.RoleUser, Content: "ищем логистов"}}}

	t.Run("контекст отменён", func(t *testing.T) {
		port := factory(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, _, err := port.Converse(ctx, request); err == nil {
			t.Fatal("ожидалась ошибка отмены контекста (Converse)")
		}
	})

	t.Run("повторный вызов", func(t *testing.T) {
		port := factory(t)
		for i := 0; i < 2; i++ {
			reply, _, err := port.Converse(context.Background(), request)
			if err != nil {
				t.Fatalf("вызов %d: %v", i, err)
			}
			if err := reply.Validate(); err != nil {
				t.Fatalf("вызов %d: невалидная реплика: %v", i, err)
			}
		}
	})
}

// RunLLMConverseStreamContract проверяет семантику потоковой операции
// ConverseStream порта llm: фрагменты текста складываются в реплику, завершающий
// кадр несёт готовый ответ и расход токенов, а отмена контекста доходит до
// потребителя.
func RunLLMConverseStreamContract(t *testing.T, factory LLMPortFactory) {
	t.Helper()

	request := llm.ConverseRequest{History: []llm.Message{{Role: llm.RoleUser, Content: "ищем логистов"}}}

	t.Run("завершающий кадр несёт ответ", func(t *testing.T) {
		port := factory(t)
		var text string
		var final *llm.ConverseReply
		for delta, err := range port.ConverseStream(context.Background(), request) {
			if err != nil {
				t.Fatalf("поток: %v", err)
			}
			text += delta.Text
			if delta.Reply != nil {
				final = delta.Reply
			}
		}
		if final == nil {
			t.Fatal("поток не завершился кадром с ответом")
		}
		if err := final.Validate(); err != nil {
			t.Fatalf("невалидный завершающий ответ: %v", err)
		}
		if text != final.Text {
			t.Fatalf("склеенный текст %q не совпал с репликой %q", text, final.Text)
		}
	})

	t.Run("контекст отменён", func(t *testing.T) {
		port := factory(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		var gotErr error
		for _, err := range port.ConverseStream(ctx, request) {
			if err != nil {
				gotErr = err
				break
			}
		}
		if gotErr == nil {
			t.Fatal("ожидалась ошибка отмены контекста (ConverseStream)")
		}
	})
}

// RunLLMInterpretContract проверяет семантику операции Interpret порта llm:
// пустой запрос отвергается, отмена контекста доходит до потребителя, а непустой
// запрос даёт валидные критерии релевантности. Явная неопределённость
// (Uncertainty) — допустимый ответ.
func RunLLMInterpretContract(t *testing.T, factory LLMInterpretFactory) {
	t.Helper()

	t.Run("пустой запрос", func(t *testing.T) {
		port := factory(t)
		if _, _, err := port.Interpret(context.Background(), llm.InterpretRequest{}); err == nil {
			t.Fatal("ожидалась ошибка на пустой запрос")
		}
	})

	t.Run("контекст отменён", func(t *testing.T) {
		port := factory(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, _, err := port.Interpret(ctx, llm.InterpretRequest{Query: "ищем логистов"}); err == nil {
			t.Fatal("ожидалась ошибка отмены контекста (Interpret)")
		}
	})

	t.Run("непустой запрос", func(t *testing.T) {
		port := factory(t)
		interpretation, usage, err := port.Interpret(context.Background(), llm.InterpretRequest{Query: "ищем компании, которым нужна автоматизация логистики"})
		if err != nil {
			t.Fatalf("Interpret: %v", err)
		}
		if err := interpretation.Validate(); err != nil {
			t.Fatalf("невалидная интерпретация: %v", err)
		}
		if err := checkUsage(usage); err != nil {
			t.Fatal(err)
		}
	})
}

// RunLLMAssessContract проверяет семантику операции Assess порта llm: пустой
// job-id отвергается, отмена контекста доходит до потребителя, а валидный запрос
// даёт оценку, проходящую ступенчатую валидацию. Suite не завязан на конкретный
// контент провайдера — проверяются только валидность и инварианты вердиктов.
func RunLLMAssessContract(t *testing.T, factory LLMAssessFactory) {
	t.Helper()

	request := llm.AssessRequest{
		JobID:       "job-1",
		CompanyName: "ООО Пример",
		Interpretation: llm.Interpretation{
			TargetLead:  "логистические компании",
			NeedSignals: []string{"нужна автоматизация логистики"},
		},
		Material: []llm.AssessMaterial{{URL: "https://example.com/about", Text: "мы автоматизируем логистику"}},
	}

	t.Run("пустой job id", func(t *testing.T) {
		port := factory(t)
		if _, _, err := port.Assess(context.Background(), llm.AssessRequest{}); err == nil {
			t.Fatal("ожидалась ошибка на пустой job_id")
		}
	})

	t.Run("контекст отменён", func(t *testing.T) {
		port := factory(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, _, err := port.Assess(ctx, request); err == nil {
			t.Fatal("ожидалась ошибка отмены контекста (Assess)")
		}
	})

	t.Run("валидный запрос", func(t *testing.T) {
		port := factory(t)
		for i := 0; i < 2; i++ {
			assessment, usage, err := port.Assess(context.Background(), request)
			if err != nil {
				t.Fatalf("вызов %d: %v", i, err)
			}
			if err := assessment.Validate(); err != nil {
				t.Fatalf("вызов %d: невалидная оценка: %v", i, err)
			}
			if err := checkUsage(usage); err != nil {
				t.Fatalf("вызов %d: %v", i, err)
			}
		}
	})

	t.Run("инвариант fits", func(t *testing.T) {
		a := llm.Assessment{
			Verdict:    llm.VerdictFits,
			SignalKind: llm.SignalDirect,
			Evidence: []llm.AssessmentEvidence{{
				Criterion:  "признак потребности",
				Fact:       "компания публично описала задачу",
				SourceURL:  "https://example.com/news",
				Reasoning:  "задача совпадает с потребностью из запроса",
				SignalKind: llm.SignalDirect,
			}},
		}
		if err := a.Validate(); err != nil {
			t.Fatalf("валидная оценка fits отвергнута: %v", err)
		}
	})

	t.Run("инвариант hypothesis", func(t *testing.T) {
		a := llm.Assessment{
			Verdict:    llm.VerdictHypothesis,
			SignalKind: llm.SignalProfile,
			Evidence: []llm.AssessmentEvidence{{
				Criterion:  "совпадение по профилю",
				Fact:       "отрасль совпадает",
				SourceURL:  "https://example.com/about",
				Reasoning:  "компания из нужной отрасли",
				SignalKind: llm.SignalProfile,
				Missing:    "нет заявления о потребности",
			}},
		}
		if err := a.Validate(); err != nil {
			t.Fatalf("валидная оценка hypothesis отвергнута: %v", err)
		}
	})

	t.Run("инвариант unsuitable", func(t *testing.T) {
		a := llm.Assessment{
			Verdict:    llm.VerdictUnsuitable,
			SignalKind: llm.SignalProfile,
			Evidence: []llm.AssessmentEvidence{{
				Criterion:  "исключение",
				Fact:       "компания оказывает услуги физлицам",
				SourceURL:  "https://example.com/services",
				Reasoning:  "не соответствует заданному типу клиента",
				SignalKind: llm.SignalProfile,
				Mismatch:   "обслуживает только физлиц",
			}},
		}
		if err := a.Validate(); err != nil {
			t.Fatalf("валидная оценка unsuitable отвергнута: %v", err)
		}
	})
}

// checkUsage проверяет, что расход токенов неотрицателен.
func checkUsage(u llm.Usage) error {
	if u.PromptTokens < 0 || u.CompletionTokens < 0 || u.TotalTokens < 0 {
		return fmt.Errorf("отрицательный расход токенов: %+v", u)
	}
	return nil
}

// RunForumPortContract проверяет семантику порта forum: уважение context.Context
// (дедлайн и отмена) и повторный вызов с валидными результатами. Отсутствие
// утечки SDK-ошибок — структурное требование порта: адаптер возвращает обычную
// ошибку Go, а не тип из библиотеки поставщика.
func RunForumPortContract(t *testing.T, factory ForumPortFactory) {
	t.Helper()

	t.Run("контекст отменён", func(t *testing.T) {
		port := factory(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := port.SearchSignals(ctx, forum.Query{Text: "q"}); err == nil {
			t.Fatal("ожидалась ошибка отмены контекста")
		}
	})

	t.Run("повторный вызов", func(t *testing.T) {
		port := factory(t)
		for i := 0; i < 2; i++ {
			signals, err := port.SearchSignals(context.Background(), forum.Query{Text: "q"})
			if err != nil {
				t.Fatalf("вызов %d: %v", i, err)
			}
			for _, signal := range signals {
				if err := signal.Validate(); err != nil {
					t.Fatalf("вызов %d: невалидный сигнал: %v", i, err)
				}
			}
		}
	})
}
