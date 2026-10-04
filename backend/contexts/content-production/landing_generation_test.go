package contentproduction

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lead-gen/backend/contexts/content-production/internal/app/command"
	"lead-gen/contracts/integration-platform/crawl"
	"lead-gen/contracts/integration-platform/llm"
)

// stubLLM — тестовый порт LLM для сборки контекста.
type stubLLM struct {
	llm.Port
	reply llm.ConverseReply
}

func (s stubLLM) Converse(context.Context, llm.ConverseRequest) (llm.ConverseReply, llm.Usage, error) {
	return s.reply, llm.Usage{TotalTokens: 10}, nil
}

// stubCrawl — тестовый движок сбора для сборки контекста.
type stubCrawl struct {
	crawl.Port
	outcome crawl.CrawlOutcome
	err     error
}

func (s stubCrawl) Crawl(context.Context, crawl.Request) (crawl.CrawlOutcome, error) {
	return s.outcome, s.err
}

func TestLandingGenerationSliceWiring(t *testing.T) {
	slice, err := Build(Config{
		LLM: stubLLM{reply: llm.ConverseReply{
			Text: `{"heading":"Аналитика для бизнеса","body":"Помогаем видеть показатели бизнеса."}`,
		}},
		SiteCollector: stubCrawl{outcome: crawl.CrawlOutcome{Pages: []crawl.Page{{
			URL:      "https://client.example",
			Markdown: "Разрабатываем сайты для бизнеса\nsales@client.example",
		}}}},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	result, err := slice.GenerateLandingTexts.Handle(context.Background(), command.GenerateLandingTexts{
		BusinessDescription: "Разрабатываем сайты для бизнеса",
		ProductInfo:         "Инструменты аналитики",
		Documents:           []string{"Ключевой показатель"},
		Tone:                "деловой",
		Language:            "русский",
		Approved:            true,
		ApprovedBy:          "operator-1",
		ApprovedAt:          time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("GenerateLandingTexts: %v", err)
	}
	if len(result.Sections) == 0 || result.Title == "" {
		t.Fatalf("наполнение неполно: %+v", result)
	}

	server := httptest.NewServer(slice.Server)
	defer server.Close()

	// Генерация текстов.
	body := `{"business_description":"Разрабатываем сайты для бизнеса","product_info":"Инструменты аналитики",` +
		`"approved":true,"approved_by":"operator-1","approved_at":"2026-02-03T04:05:06Z"}`
	resp, err := http.Post(server.URL+"/content/landing-texts", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST landing-texts: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("статус генерации: %d", resp.StatusCode)
	}
	var generated struct {
		Title    string `json:"title"`
		Sections []struct {
			Role string `json:"role"`
			Body string `json:"body"`
		} `json:"sections"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&generated); err != nil {
		t.Fatalf("разбор ответа генерации: %v", err)
	}
	if generated.Title == "" || len(generated.Sections) == 0 {
		t.Fatalf("ответ генерации неполон: %+v", generated)
	}

	// Сбор сведений с сайта.
	collectResp, err := http.Post(server.URL+"/content/landing-collect", "application/json",
		strings.NewReader(`{"start_url":"https://client.example","max_depth":1}`))
	if err != nil {
		t.Fatalf("POST landing-collect: %v", err)
	}
	defer collectResp.Body.Close()
	if collectResp.StatusCode != http.StatusOK {
		t.Fatalf("статус сбора: %d", collectResp.StatusCode)
	}
	var collected struct {
		Available bool `json:"available"`
		Pages     []struct {
			Text string `json:"text"`
		} `json:"pages"`
	}
	if err := json.NewDecoder(collectResp.Body).Decode(&collected); err != nil {
		t.Fatalf("разбор ответа сбора: %v", err)
	}
	if !collected.Available || len(collected.Pages) != 1 {
		t.Fatalf("сбор не выполнен: %+v", collected)
	}
}

func TestLandingGenerationWithoutLLMIsUnavailable(t *testing.T) {
	slice, err := Build(Config{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	_, err = slice.GenerateLandingTexts.Handle(context.Background(), command.GenerateLandingTexts{
		BusinessDescription: "Разрабатываем сайты",
		Approved:            true,
		ApprovedBy:          "operator-1",
		ApprovedAt:          time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("генерация без порта LLM не отказала")
	}
}
