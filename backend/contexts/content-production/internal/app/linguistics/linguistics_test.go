package linguistics

import (
	"context"
	"errors"
	"strings"
	"testing"

	"lead-gen/backend/contexts/content-production/internal/domain"
	"lead-gen/contracts/integration-platform/llm"
)

// fakeLLM — тестовый порт LLM: отдаёт заранее заданные ответы по порядку и
// запоминает запросы. Встраивание llm.Port закрывает методы, которые здесь не
// вызываются.
type fakeLLM struct {
	llm.Port
	replies  []llm.ConverseReply
	usages   []llm.Usage
	requests []llm.ConverseRequest
}

func (f *fakeLLM) Converse(_ context.Context, req llm.ConverseRequest) (llm.ConverseReply, llm.Usage, error) {
	f.requests = append(f.requests, req)
	index := len(f.requests) - 1
	if index >= len(f.replies) {
		return llm.ConverseReply{}, llm.Usage{}, errors.New("нет ответа")
	}
	usage := llm.Usage{}
	if index < len(f.usages) {
		usage = f.usages[index]
	}
	return f.replies[index], usage, nil
}

func testMaterials(t *testing.T) domain.LandingMaterials {
	t.Helper()
	materials, err := domain.NewLandingMaterials("https://site.example", "Разрабатываем сайты для бизнеса", "Продукт: инструменты аналитики", nil)
	if err != nil {
		t.Fatalf("NewLandingMaterials: %v", err)
	}
	return materials
}

func TestGenerateParsesJSONAndCarriesParams(t *testing.T) {
	port := &fakeLLM{replies: []llm.ConverseReply{{
		Text: `{"heading":"Аналитика для бизнеса","body":"Помогаем видеть показатели бизнеса и принимать решения."}`,
	}}, usages: []llm.Usage{{TotalTokens: 42}}}
	generator := New(port)

	generated, err := generator.Generate(context.Background(), GenerateRequest{
		Role:      domain.LandingBlockOffer,
		Materials: testMaterials(t),
		Tone:      "деловой",
		Language:  "английский",
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if generated.Heading != "Аналитика для бизнеса" || generated.Body == "" {
		t.Fatalf("текст разобран неполно: %+v", generated)
	}
	if generated.Usage.TotalTokens != 42 {
		t.Fatalf("расход токенов потерян: %+v", generated.Usage)
	}
	if len(port.requests) != 1 {
		t.Fatalf("вызовов LLM %d, ожидался 1", len(port.requests))
	}
	prompt := port.requests[0].History[0].Content
	for _, want := range []string{"Разрабатываем сайты для бизнеса", "деловой", "английский", domain.LandingBlockOffer.Label()} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("промпт не содержит %q: %s", want, prompt)
		}
	}
}

func TestGenerateParsesLineForm(t *testing.T) {
	port := &fakeLLM{replies: []llm.ConverseReply{{
		Text: "Заголовок: Наше предложение\n\nМы делаем работу качественно.",
	}}}
	generated, err := New(port).Generate(context.Background(), GenerateRequest{
		Role:      domain.LandingBlockAction,
		Materials: testMaterials(t),
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if generated.Heading != "Наше предложение" || generated.Body != "Мы делаем работу качественно." {
		t.Fatalf("текст разобран неполно: %+v", generated)
	}
}

func TestGenerateRefusesInventedFacts(t *testing.T) {
	port := &fakeLLM{replies: []llm.ConverseReply{{
		Text: `{"heading":"Рост","body":"Гарантируем рост продаж на 30% за 5 дней."}`,
	}}}
	_, err := New(port).Generate(context.Background(), GenerateRequest{
		Role:      domain.LandingBlockOffer,
		Materials: testMaterials(t),
	})
	if !errors.Is(err, ErrUnsourcedClaims) {
		t.Fatalf("ожидалась ErrUnsourcedClaims, получено %v", err)
	}
}

func TestGenerateUnavailableWithoutPort(t *testing.T) {
	_, err := New(nil).Generate(context.Background(), GenerateRequest{
		Role:      domain.LandingBlockOffer,
		Materials: testMaterials(t),
	})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ожидалась ErrUnavailable, получено %v", err)
	}
}

func TestGenerateEmptyReply(t *testing.T) {
	port := &fakeLLM{replies: []llm.ConverseReply{{}}}
	_, err := New(port).Generate(context.Background(), GenerateRequest{
		Role:      domain.LandingBlockOffer,
		Materials: testMaterials(t),
	})
	if !errors.Is(err, ErrEmptyReply) {
		t.Fatalf("ожидалась ErrEmptyReply, получено %v", err)
	}
}

func TestGenerateRejectsUnknownRole(t *testing.T) {
	port := &fakeLLM{replies: []llm.ConverseReply{{Text: "x"}}}
	_, err := New(port).Generate(context.Background(), GenerateRequest{
		Role:      "unknown",
		Materials: testMaterials(t),
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("ожидалась ErrInvalidRequest, получено %v", err)
	}
}

func TestRephrase(t *testing.T) {
	port := &fakeLLM{replies: []llm.ConverseReply{{Text: "  Новый текст без копирования.  "}}, usages: []llm.Usage{{TotalTokens: 7}}}
	rephrased, usage, err := New(port).Rephrase(context.Background(), RephraseRequest{Text: "Старый текст"})
	if err != nil {
		t.Fatalf("Rephrase: %v", err)
	}
	if rephrased != "Новый текст без копирования." || usage.TotalTokens != 7 {
		t.Fatalf("результат неполон: %q, %+v", rephrased, usage)
	}

	if _, _, err := New(nil).Rephrase(context.Background(), RephraseRequest{Text: "Старый текст"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ожидалась ErrUnavailable, получено %v", err)
	}
	if _, _, err := New(port).Rephrase(context.Background(), RephraseRequest{Text: "  "}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("ожидалась ErrInvalidRequest, получено %v", err)
	}
}
