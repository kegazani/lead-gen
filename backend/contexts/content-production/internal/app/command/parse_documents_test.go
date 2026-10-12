package command

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"lead-gen/backend/contexts/content-production/internal/app/linguistics"
	"lead-gen/contracts/integration-platform/crawl"
	"lead-gen/contracts/integration-platform/documentparse"
)

// fakeParser — тестовый порт разбора документов: отдаёт заготовленный текст по
// источнику и умеет возвращать ошибку по отдельным источникам.
type fakeParser struct {
	texts map[string]string
	errs  map[string]error
	calls []documentparse.Request
}

func (f *fakeParser) ParseDocument(_ context.Context, r documentparse.Request) (documentparse.Document, error) {
	f.calls = append(f.calls, r)
	if err := f.errs[r.Source]; err != nil {
		return documentparse.Document{}, err
	}
	text, ok := f.texts[r.Source]
	if !ok {
		return documentparse.Document{}, errors.New("источник не найден в тестовом движке")
	}
	return documentparse.Document{Source: r.Source, Format: r.Format, Text: text}, nil
}

// TestParseDocumentsExtractsTexts проверяет, что предоставленные документы реально
// разбираются через порт, а извлечённые тексты возвращаются готовыми к генерации.
func TestParseDocumentsExtractsTexts(t *testing.T) {
	parser := &fakeParser{texts: map[string]string{
		"https://client.example/kp.pdf": "Коммерческое предложение: сроки и стоимость по запросу.",
	}}
	handler := NewParseDocumentsHandler(parser)

	result, err := handler.Handle(context.Background(), ParseDocuments{Sources: []DocumentSource{
		{Source: "https://client.example/kp.pdf", Format: "pdf"},
	}})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !result.Available {
		t.Fatal("разбор сконфигурированного движка помечен недоступным")
	}
	if len(result.Documents) != 1 || !strings.Contains(result.Documents[0], "Коммерческое предложение") {
		t.Fatalf("извлечённый текст не получен: %+v", result.Documents)
	}
	if len(result.Parsed) != 1 || result.Parsed[0].Format != "pdf" {
		t.Fatalf("разобранный документ неполон: %+v", result.Parsed)
	}
	if len(parser.calls) != 1 || parser.calls[0].Source != "https://client.example/kp.pdf" {
		t.Fatalf("порт вызван неверно: %+v", parser.calls)
	}
}

// TestParseDocumentsFeedGeneration проверяет критерий задачи 1.2: разобранные
// сведения доступны генерации. Без документов блок подтверждения не создаётся; с
// разобранными документами он появляется — значит, извлечённый текст дошёл до
// построения наполнения.
func TestParseDocumentsFeedGeneration(t *testing.T) {
	parser := &fakeParser{texts: map[string]string{
		"https://client.example/kp.pdf": "Подтверждение: внедрено у 30 клиентов.",
	}}
	parseHandler := NewParseDocumentsHandler(parser)
	parsed, err := parseHandler.Handle(context.Background(), ParseDocuments{Sources: []DocumentSource{
		{Source: "https://client.example/kp.pdf", Format: "pdf"},
	}})
	if err != nil {
		t.Fatalf("разбор: %v", err)
	}

	generator := NewGenerateLandingTextsHandler(linguistics.New(&fakeLLM{reply: groundedReply}))
	base := GenerateLandingTexts{
		BusinessDescription: "Разрабатываем сайты для бизнеса",
		ProductInfo:         "Инструменты аналитики",
		Approved:            true,
		ApprovedBy:          "operator-1",
		ApprovedAt:          time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC),
	}

	withoutDocs := base
	if result, err := generator.Handle(context.Background(), withoutDocs); err != nil {
		t.Fatalf("генерация без документов: %v", err)
	} else if len(result.Sections) != 4 {
		t.Fatalf("без документов ожидалось 4 блока, получено %d", len(result.Sections))
	}

	withDocs := base
	withDocs.Documents = parsed.Documents
	result, err := generator.Handle(context.Background(), withDocs)
	if err != nil {
		t.Fatalf("генерация с разобранными документами: %v", err)
	}
	if len(result.Sections) != 5 {
		t.Fatalf("с разобранными документами ожидалось 5 блоков (включая подтверждение), получено %d: %+v", len(result.Sections), result.Sections)
	}
}

// TestParseDocumentsUnavailableWithoutParser проверяет честную недоступность: без
// порта разбор не выдаётся за выполненный.
func TestParseDocumentsUnavailableWithoutParser(t *testing.T) {
	handler := NewParseDocumentsHandler(nil)
	result, err := handler.Handle(context.Background(), ParseDocuments{Sources: []DocumentSource{{Source: "https://client.example/kp.pdf"}}})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if result.Available {
		t.Fatal("разбор без порта выдан за выполненный")
	}
	if strings.TrimSpace(result.Reason) == "" {
		t.Fatal("недоступность разбора не сообщена")
	}
}

// TestParseDocumentsRejectsEmptyInput проверяет, что пустой вход отвергается.
func TestParseDocumentsRejectsEmptyInput(t *testing.T) {
	handler := NewParseDocumentsHandler(&fakeParser{})
	if _, err := handler.Handle(context.Background(), ParseDocuments{}); !errors.Is(err, ErrDocumentInput) {
		t.Fatalf("ожидалась ErrDocumentInput, получено %v", err)
	}
}

// TestParseDocumentsReportsFailuresPerSource проверяет честный отказ по
// конкретному документу: сбой одного источника не скрывает остальные.
func TestParseDocumentsReportsFailuresPerSource(t *testing.T) {
	parser := &fakeParser{
		texts: map[string]string{"https://client.example/ok.pdf": "Успешно разобранный документ."},
		errs:  map[string]error{"https://client.example/bad.pdf": errors.New("движок: 415 неподдерживаемый формат")},
	}
	handler := NewParseDocumentsHandler(parser)

	result, err := handler.Handle(context.Background(), ParseDocuments{Sources: []DocumentSource{
		{Source: "https://client.example/ok.pdf", Format: "pdf"},
		{Source: "https://client.example/bad.pdf", Format: "docx"},
		{Source: "   "},
	}})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(result.Documents) != 1 {
		t.Fatalf("ожидался один разобранный документ, получено %d", len(result.Documents))
	}
	if len(result.Failures) != 2 {
		t.Fatalf("ожидалось два отказа, получено %d: %+v", len(result.Failures), result.Failures)
	}
	for _, failure := range result.Failures {
		if strings.TrimSpace(failure.Reason) == "" {
			t.Fatalf("причина отказа не сообщена: %+v", failure)
		}
	}
}

// TestParseDocumentsReportsEmptyText проверяет, что разбор без текста не выдаётся
// за содержимое.
func TestParseDocumentsReportsEmptyText(t *testing.T) {
	parser := &fakeParser{texts: map[string]string{"https://client.example/empty.pdf": "   "}}
	handler := NewParseDocumentsHandler(parser)
	result, err := handler.Handle(context.Background(), ParseDocuments{Sources: []DocumentSource{{Source: "https://client.example/empty.pdf"}}})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(result.Documents) != 0 || len(result.Failures) != 1 {
		t.Fatalf("пустой документ выдан за разобранный: %+v", result)
	}
}

// recordingCollector — тестовый движок сбора, запоминающий запрос обхода: нужен,
// чтобы проверить, что границы обхода доходят до порта без изменений. Встроенное
// crawl.Port закрывает неиспользуемый метод Extract.
type recordingCollector struct {
	crawl.Port
	requests []crawl.Request
	outcome  crawl.CrawlOutcome
}

func (c *recordingCollector) Crawl(_ context.Context, r crawl.Request) (crawl.CrawlOutcome, error) {
	c.requests = append(c.requests, r)
	return c.outcome, nil
}

// TestCollectSitePassesAndRecordsBounds проверяет критерий задачи 2.1: границы
// обхода передаются в порт без изменений и фиксируются в результате.
func TestCollectSitePassesAndRecordsBounds(t *testing.T) {
	collector := &recordingCollector{outcome: crawl.CrawlOutcome{Pages: []crawl.Page{{URL: "https://client.example", Markdown: "О компании"}}}}
	handler := NewCollectSiteHandler(collector)

	result, err := handler.Handle(context.Background(), CollectSite{
		StartURL:       "https://client.example",
		MaxPages:       7,
		MaxDepth:       2,
		MaxTimeSeconds: 90,
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(collector.requests) != 1 {
		t.Fatalf("ожидался один обход, получено %d", len(collector.requests))
	}
	request := collector.requests[0]
	if request.StartURL != "https://client.example" || request.MaxPages != 7 || request.MaxDepth != 2 || request.MaxTimeSeconds != 90 {
		t.Fatalf("границы обхода не переданы в порт: %+v", request)
	}
	if result.MaxPages != 7 || result.MaxDepth != 2 || result.MaxTimeSeconds != 90 {
		t.Fatalf("границы обхода не зафиксированы в результате: %+v", result)
	}
}
