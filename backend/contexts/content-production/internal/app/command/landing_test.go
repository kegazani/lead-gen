package command

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"lead-gen/backend/contexts/content-production/internal/app/linguistics"
	"lead-gen/backend/contexts/content-production/internal/domain"
	"lead-gen/contracts/integration-platform/crawl"
	"lead-gen/contracts/integration-platform/llm"
)

// fakeLLM — тестовый порт LLM: отдаёт заготовленный ответ (или очередь ответов) и
// запоминает запросы. Встраивание llm.Port закрывает неиспользуемые методы.
type fakeLLM struct {
	llm.Port
	reply    llm.ConverseReply
	replies  []llm.ConverseReply
	requests []llm.ConverseRequest
}

func (f *fakeLLM) Converse(_ context.Context, req llm.ConverseRequest) (llm.ConverseReply, llm.Usage, error) {
	f.requests = append(f.requests, req)
	if len(f.replies) == 0 {
		return f.reply, llm.Usage{}, nil
	}
	index := len(f.requests) - 1
	if index >= len(f.replies) {
		index = len(f.replies) - 1
	}
	return f.replies[index], llm.Usage{}, nil
}

// fakeCollector — тестовый движок сбора: отдаёт заданный исход.
type fakeCollector struct {
	crawl.Port
	outcome crawl.CrawlOutcome
	err     error
}

func (f fakeCollector) Crawl(context.Context, crawl.Request) (crawl.CrawlOutcome, error) {
	return f.outcome, f.err
}

// groundedReply — правдоподобный ответ модели без сведений вне материалов.
var groundedReply = llm.ConverseReply{
	Text: `{"heading":"Аналитика для бизнеса","body":"Помогаем видеть показатели бизнеса и принимать решения."}`,
}

func approvedLandingInput() GenerateLandingTexts {
	return GenerateLandingTexts{
		BusinessDescription: "Разрабатываем сайты для бизнеса",
		ProductInfo:         "Инструменты аналитики",
		Documents:           []string{"Ключевой показатель: экономия до 50%"},
		SiteText:            "Экономия до 30%",
		Tone:                "деловой",
		Language:            "русский",
		Approved:            true,
		ApprovedBy:          "operator-1",
		ApprovedAt:          time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC),
	}
}

func TestGenerateLandingTextsRequiresApproval(t *testing.T) {
	handler := NewGenerateLandingTextsHandler(linguistics.New(&fakeLLM{reply: groundedReply}))
	cmd := approvedLandingInput()
	cmd.Approved = false
	if _, err := handler.Handle(context.Background(), cmd); !errors.Is(err, domain.ErrStructureNotApproved) {
		t.Fatalf("ожидалась ErrStructureNotApproved, получено %v", err)
	}
}

func TestGenerateLandingTextsSuccess(t *testing.T) {
	handler := NewGenerateLandingTextsHandler(linguistics.New(&fakeLLM{reply: groundedReply}))
	result, err := handler.Handle(context.Background(), approvedLandingInput())
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(result.Sections) != 5 {
		t.Fatalf("ожидалось 5 блоков, получено %d: %+v", len(result.Sections), result.Sections)
	}
	if result.Title == "" || result.Headline != result.Title {
		t.Fatalf("заголовок неполон: %+v", result)
	}
	for _, section := range result.Sections {
		if section.Body == "" || section.Heading == "" {
			t.Fatalf("блок неполон: %+v", section)
		}
	}
	if len(result.Discrepancies) != 2 {
		t.Fatalf("расхождения между сайтом и документами не сообщены: %v", result.Discrepancies)
	}
}

func TestGenerateLandingTextsUnavailableWithoutLLM(t *testing.T) {
	handler := NewGenerateLandingTextsHandler(nil)
	if _, err := handler.Handle(context.Background(), approvedLandingInput()); !errors.Is(err, linguistics.ErrUnavailable) {
		t.Fatalf("ожидалась ErrUnavailable, получено %v", err)
	}
}

func TestCollectSiteUnavailableIsHonest(t *testing.T) {
	handler := NewCollectSiteHandler(nil)
	result, err := handler.Handle(context.Background(), CollectSite{StartURL: "https://client.example", MaxDepth: 1})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if result.Available {
		t.Fatal("недоступный сбор выдан за выполненный")
	}
	if strings.TrimSpace(result.Reason) == "" {
		t.Fatal("недоступность сбора не сообщена")
	}
}

func TestCollectSiteRejectsEmptyURL(t *testing.T) {
	handler := NewCollectSiteHandler(&fakeCollector{})
	if _, err := handler.Handle(context.Background(), CollectSite{}); !errors.Is(err, ErrCollectionInput) {
		t.Fatalf("ожидалась ErrCollectionInput, получено %v", err)
	}
}

func TestCollectSiteExcludesPersonalData(t *testing.T) {
	collector := &fakeCollector{outcome: crawl.CrawlOutcome{Pages: []crawl.Page{{
		URL: "https://client.example",
		Markdown: strings.Join([]string{
			"Разрабатываем сайты для бизнеса",
			"Директор: Иван Петров",
			"sales@client.example",
			"ivan.petrov@example.com",
		}, "\n"),
	}}}}
	handler := NewCollectSiteHandler(collector)
	result, err := handler.Handle(context.Background(), CollectSite{StartURL: "https://client.example", MaxPages: 5, MaxDepth: 1})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !result.Available || len(result.Pages) != 1 {
		t.Fatalf("сбор не выполнен: %+v", result)
	}
	text := result.Pages[0].Text
	for _, forbidden := range []string{"Иван Петров", "ivan.petrov@example.com"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("персональные данные %q попали в сбор: %s", forbidden, text)
		}
	}
	if !strings.Contains(text, "sales@client.example") {
		t.Fatalf("общий контакт компании потерян: %s", text)
	}
	if len(result.ExcludedPersonal) == 0 {
		t.Fatal("факт исключения персональных данных не зафиксирован")
	}
}

func TestCollectSiteReportsAuthLimitation(t *testing.T) {
	collector := &fakeCollector{err: errors.New("crawl: 403 forbidden")}
	handler := NewCollectSiteHandler(collector)
	result, err := handler.Handle(context.Background(), CollectSite{StartURL: "https://client.example"})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if result.Available || !strings.Contains(result.Reason, "авторизации") {
		t.Fatalf("ограничение по авторизации не сообщено: %+v", result)
	}
}

func TestCheckLandingTextsDetectsAndRephrases(t *testing.T) {
	texts := linguistics.New(&fakeLLM{reply: llm.ConverseReply{Text: "Переформулированный фрагмент без копирования."}})
	handler := NewCheckLandingTextsHandler(texts, nil)

	source := "Мы поставляем промышленное оборудование по всей России"
	text := "Наша компания растёт. Мы поставляем промышленное оборудование по всей России."
	result, err := handler.Handle(context.Background(), CheckLandingTexts{Text: text, Sources: []string{source}})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(result.Matches) != 1 {
		t.Fatalf("дословное совпадение не обнаружено: %+v", result.Matches)
	}
	match := result.Matches[0]
	if !strings.Contains(match.Fragment, "промышленное оборудование") {
		t.Fatalf("совпадение неполно: %+v", match)
	}
	if !match.Rephrased || match.Replacement == "" {
		t.Fatalf("фрагмент не переформулирован: %+v", match)
	}
	if !result.ExternalUnavailable {
		t.Fatal("недоступность сторонних источников не сообщена")
	}
}

func TestCheckLandingTextsKeepsRequisites(t *testing.T) {
	handler := NewCheckLandingTextsHandler(linguistics.New(&fakeLLM{reply: llm.ConverseReply{Text: "x"}}), nil)
	requisites := "ООО Ромашка ИНН 7701234567 город Москва"
	result, err := handler.Handle(context.Background(), CheckLandingTexts{
		Text:      requisites,
		Sources:   []string{requisites},
		Protected: []string{requisites},
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(result.Matches) != 0 {
		t.Fatalf("реквизиты приняты за заимствование: %+v", result.Matches)
	}
}

func TestCheckLandingTextsRewriteUnavailable(t *testing.T) {
	handler := NewCheckLandingTextsHandler(nil, nil)
	source := "Мы поставляем промышленное оборудование по всей России"
	result, err := handler.Handle(context.Background(), CheckLandingTexts{Text: source, Sources: []string{source}})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(result.Matches) == 0 {
		t.Fatal("совпадение не обнаружено")
	}
	if !result.RewriteUnavailable {
		t.Fatal("недоступность переформулирования не сообщена")
	}
}
