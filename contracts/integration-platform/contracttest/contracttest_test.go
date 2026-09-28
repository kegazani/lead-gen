package contracttest

import (
	"context"
	"errors"
	"iter"
	"testing"

	"lead-gen/contracts/integration-platform/crawl"
	"lead-gen/contracts/integration-platform/forum"
	"lead-gen/contracts/integration-platform/llm"
	"lead-gen/contracts/integration-platform/search"
)

// fakePorts — минимальный тест-дубль всех портов, которым проверяется сам
// contract suite. Он уважает отмену контекста и возвращает детерминированные
// результаты; флаги fail заставляют соответствующий порт вернуть ошибку.
type fakePorts struct {
	failSearch bool
	failCrawl  bool
	failLLM    bool
	failForum  bool
}

// fitsAssessment — канонический валидный ответ Assess для тест-дубля.
func fitsAssessment() llm.Assessment {
	return llm.Assessment{
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
}

var _ search.Port = (*fakePorts)(nil)
var _ crawl.Port = (*fakePorts)(nil)
var _ llm.Port = (*fakePorts)(nil)
var _ forum.Port = (*fakePorts)(nil)

func (f *fakePorts) Search(ctx context.Context, _ search.Query) ([]search.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.failSearch {
		return nil, errors.New("провайдер поиска недоступен")
	}
	return []search.Result{{URL: "https://example.com", Title: "t", Snippet: "s"}}, nil
}

func (f *fakePorts) Crawl(ctx context.Context, _ crawl.Request) (crawl.CrawlOutcome, error) {
	if err := ctx.Err(); err != nil {
		return crawl.CrawlOutcome{}, err
	}
	if f.failCrawl {
		return crawl.CrawlOutcome{}, errors.New("провайдер обхода недоступен")
	}
	return crawl.CrawlOutcome{Pages: []crawl.Page{{URL: "https://example.com", Markdown: "# t"}}}, nil
}

func (f *fakePorts) Extract(ctx context.Context, _ crawl.ExtractRequest) ([]crawl.ContactCandidate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.failCrawl {
		return nil, errors.New("провайдер экстракции недоступен")
	}
	return []crawl.ContactCandidate{{Name: "Иван", Email: "ivan@example.com"}}, nil
}

func (f *fakePorts) Plan(ctx context.Context, _ llm.PlanRequest) ([]llm.Target, llm.Usage, error) {
	if err := ctx.Err(); err != nil {
		return nil, llm.Usage{}, err
	}
	if f.failLLM {
		return nil, llm.Usage{}, errors.New("провайдер LLM недоступен")
	}
	return []llm.Target{{Query: "q1"}}, llm.Usage{}, nil
}

func (f *fakePorts) Suggest(ctx context.Context, _ llm.SuggestRequest) ([]llm.Suggestion, llm.Usage, error) {
	if err := ctx.Err(); err != nil {
		return nil, llm.Usage{}, err
	}
	if f.failLLM {
		return nil, llm.Usage{}, errors.New("провайдер LLM недоступен")
	}
	return []llm.Suggestion{{Sector: "Логистика", Niche: "Сборные грузы", SeedQueries: []string{"перевозки сборных грузов"}}}, llm.Usage{}, nil
}

func (f *fakePorts) Interpret(ctx context.Context, r llm.InterpretRequest) (llm.Interpretation, llm.Usage, error) {
	if err := ctx.Err(); err != nil {
		return llm.Interpretation{}, llm.Usage{}, err
	}
	if f.failLLM {
		return llm.Interpretation{}, llm.Usage{}, errors.New("провайдер LLM недоступен")
	}
	if err := r.Validate(); err != nil {
		return llm.Interpretation{}, llm.Usage{}, err
	}
	return llm.Interpretation{
		TargetLead:  "логистические компании",
		NeedSignals: []string{"нужна автоматизация логистики"},
	}, llm.Usage{}, nil
}

func (f *fakePorts) Assess(ctx context.Context, r llm.AssessRequest) (llm.Assessment, llm.Usage, error) {
	if err := ctx.Err(); err != nil {
		return llm.Assessment{}, llm.Usage{}, err
	}
	if f.failLLM {
		return llm.Assessment{}, llm.Usage{}, errors.New("провайдер LLM недоступен")
	}
	if err := r.Validate(); err != nil {
		return llm.Assessment{}, llm.Usage{}, err
	}
	return fitsAssessment(), llm.Usage{}, nil
}

func (f *fakePorts) Converse(ctx context.Context, _ llm.ConverseRequest) (llm.ConverseReply, llm.Usage, error) {
	if err := ctx.Err(); err != nil {
		return llm.ConverseReply{}, llm.Usage{}, err
	}
	if f.failLLM {
		return llm.ConverseReply{}, llm.Usage{}, errors.New("провайдер LLM недоступен")
	}
	return llm.ConverseReply{Text: "какой регион нужен?"}, llm.Usage{}, nil
}

func (f *fakePorts) ConverseStream(ctx context.Context, _ llm.ConverseRequest) iter.Seq2[llm.ConverseDelta, error] {
	return func(yield func(llm.ConverseDelta, error) bool) {
		if err := ctx.Err(); err != nil {
			yield(llm.ConverseDelta{}, err)
			return
		}
		if f.failLLM {
			yield(llm.ConverseDelta{}, errors.New("провайдер LLM недоступен"))
			return
		}
		reply := &llm.ConverseReply{Text: "какой регион нужен?"}
		if !yield(llm.ConverseDelta{Text: reply.Text}, nil) {
			return
		}
		yield(llm.ConverseDelta{Reply: reply}, nil)
	}
}

func (f *fakePorts) SearchSignals(ctx context.Context, _ forum.Query) ([]forum.Signal, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.failForum {
		return nil, errors.New("провайдер форумов недоступен")
	}
	return []forum.Signal{{Topic: "тема обсуждения", SourceURL: "https://example.com/t/1"}}, nil
}

// TestSearchPortContract прогоняет suite порта search против тест-дубля.
func TestSearchPortContract(t *testing.T) {
	RunSearchPortContract(t, func(t *testing.T) search.Port {
		t.Helper()
		return &fakePorts{}
	})
}

// TestCrawlPortContract прогоняет suite порта crawl против тест-дубля.
func TestCrawlPortContract(t *testing.T) {
	RunCrawlPortContract(t, func(t *testing.T) crawl.Port {
		t.Helper()
		return &fakePorts{}
	})
}

// TestLLMPortContract прогоняет suite порта llm против тест-дубля.
func TestLLMPortContract(t *testing.T) {
	RunLLMPortContract(t, func(t *testing.T) llm.Port {
		t.Helper()
		return &fakePorts{}
	})
}

// TestLLMSuggestContract прогоняет suite операции Suggest порта llm против
// тест-дубля.
func TestLLMSuggestContract(t *testing.T) {
	RunLLMSuggestContract(t, func(t *testing.T) llm.Port {
		t.Helper()
		return &fakePorts{}
	})
}

// TestLLMConverseContract прогоняет suite операции Converse порта llm против
// тест-дубля.
func TestLLMConverseContract(t *testing.T) {
	RunLLMConverseContract(t, func(t *testing.T) llm.Port {
		t.Helper()
		return &fakePorts{}
	})
}

// TestLLMConverseStreamContract прогоняет suite потоковой операции порта llm
// против тест-дубля.
func TestLLMConverseStreamContract(t *testing.T) {
	RunLLMConverseStreamContract(t, func(t *testing.T) llm.Port {
		t.Helper()
		return &fakePorts{}
	})
}

// TestLLMInterpretContract прогоняет suite операции Interpret порта llm против
// тест-дубля.
func TestLLMInterpretContract(t *testing.T) {
	RunLLMInterpretContract(t, func(t *testing.T) llm.Port {
		t.Helper()
		return &fakePorts{}
	})
}

// TestLLMAssessContract прогоняет suite операции Assess порта llm против
// тест-дубля.
func TestLLMAssessContract(t *testing.T) {
	RunLLMAssessContract(t, func(t *testing.T) llm.Port {
		t.Helper()
		return &fakePorts{}
	})
}

// TestForumPortContract прогоняет suite порта forum против тест-дубля.
func TestForumPortContract(t *testing.T) {
	RunForumPortContract(t, func(t *testing.T) forum.Port {
		t.Helper()
		return &fakePorts{}
	})
}

// TestFakeFailureReturnsPlainError подтверждает, что отказ адаптера — обычная
// ошибка Go, а не паника и не тип из SDK поставщика: потребитель видит только
// error, а не внутренний тип библиотеки.
func TestFakeFailureReturnsPlainError(t *testing.T) {
	f := &fakePorts{failSearch: true, failCrawl: true, failLLM: true, failForum: true}

	if _, err := f.Search(context.Background(), search.Query{Text: "q"}); err == nil {
		t.Fatal("ожидалась ошибка поиска")
	}
	if _, err := f.Crawl(context.Background(), crawl.Request{StartURL: "u"}); err == nil {
		t.Fatal("ожидалась ошибка обхода")
	}
	if _, err := f.Extract(context.Background(), crawl.ExtractRequest{URL: "u"}); err == nil {
		t.Fatal("ожидалась ошибка экстракции")
	}
	if _, _, err := f.Plan(context.Background(), llm.PlanRequest{Query: "q"}); err == nil {
		t.Fatal("ожидалась ошибка планирования")
	}
	if _, _, err := f.Suggest(context.Background(), llm.SuggestRequest{Description: "d"}); err == nil {
		t.Fatal("ожидалась ошибка подбора ниш")
	}
	if _, _, err := f.Converse(context.Background(), llm.ConverseRequest{History: []llm.Message{{Role: llm.RoleUser, Content: "q"}}}); err == nil {
		t.Fatal("ожидалась ошибка диалога")
	}
	if _, _, err := f.Interpret(context.Background(), llm.InterpretRequest{Query: "q"}); err == nil {
		t.Fatal("ожидалась ошибка интерпретации")
	}
	if _, _, err := f.Assess(context.Background(), llm.AssessRequest{JobID: "job-1"}); err == nil {
		t.Fatal("ожидалась ошибка оценки")
	}
	if _, err := f.SearchSignals(context.Background(), forum.Query{Text: "q"}); err == nil {
		t.Fatal("ожидалась ошибка поиска сигналов")
	}
}
