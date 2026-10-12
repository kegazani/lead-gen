package contentproduction

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"lead-gen/backend/contexts/content-production/internal/adapters/renderer/deterministic"
	"lead-gen/backend/contexts/content-production/internal/app/command"
	"lead-gen/contracts/integration-platform/crawl"
	"lead-gen/contracts/integration-platform/llm"
)

// Проверка переиспользования (задача 10.3): тексты порождаются через capability
// linguistics (порт LLM), а сбор — через движок 3.1 (порт crawl). Собственных
// механизмов генерации текста и сбора в модуле нет. Проверка двухслойная:
// статически (импорты внешних контрактов и отсутствие реализаций портов) и
// динамически (сборка среза ведёт генерацию и сбор именно через порты).

const (
	llmContract     = "lead-gen/contracts/integration-platform/llm"
	crawlContract   = "lead-gen/contracts/integration-platform/crawl"
	linguisticsPkg  = "lead-gen/backend/contexts/content-production/internal/app/linguistics"
	collectSiteFile = "internal/app/command/collect_site.go"
	generateFile    = "internal/app/command/generate_landing_texts.go"
)

// TestLandingGeneratorReusesLinguisticsAndCollectionEngine доказывает критерий
// задачи 10.3.
func TestLandingGeneratorReusesLinguisticsAndCollectionEngine(t *testing.T) {
	assertNoOwnMechanisms(t)
	assertGenerationGoesThroughLLMPort(t)
	assertCollectionGoesThroughCrawlPort(t)
}

// assertNoOwnMechanisms статически проверяет исходники модуля (без тестов):
// внешние контракты импортируются только в точках переиспользования и при сборке,
// а типов, реализующих порты внешних движков, в модуле нет.
func assertNoOwnMechanisms(t *testing.T) {
	t.Helper()

	// Единственные допустимые импортёры контрактов: точка переиспользования и
	// сборка сервиса, которая только впрыскивает порт.
	allowLLM := map[string]bool{
		"internal/app/linguistics/linguistics.go": true,
		"service.go": true,
	}
	allowCrawl := map[string]bool{
		collectSiteFile: true,
		"service.go":    true,
	}
	// Методы портов внешних движков: их реализаций в модуле быть не должно.
	engineMethods := map[string]bool{"Converse": true, "Crawl": true, "Extract": true}

	imports := map[string]map[string]bool{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel := filepath.ToSlash(path)
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		set := make(map[string]bool, len(file.Imports))
		for _, imp := range file.Imports {
			pkg, unquoteErr := strconv.Unquote(imp.Path.Value)
			if unquoteErr != nil {
				return unquoteErr
			}
			set[pkg] = true
		}
		imports[rel] = set

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil {
				continue
			}
			if engineMethods[fn.Name.Name] {
				t.Errorf("%s: собственный механизм внешнего движка — метод %s", rel, fn.Name.Name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("обход исходников модуля: %v", err)
	}
	if len(imports) == 0 {
		t.Fatal("исходники модуля не найдены")
	}

	for rel, set := range imports {
		if set[llmContract] && !allowLLM[rel] {
			t.Errorf("%s: импорт контракта LLM вне capability linguistics", rel)
		}
		if set[crawlContract] && !allowCrawl[rel] {
			t.Errorf("%s: импорт контракта сбора вне команды сбора", rel)
		}
	}
	// Точки переиспользования на месте.
	if !imports[generateFile][linguisticsPkg] {
		t.Errorf("%s не переиспользует capability linguistics", generateFile)
	}
	if !imports[collectSiteFile][crawlContract] {
		t.Errorf("%s не переиспользует контракт движка сбора", collectSiteFile)
	}
}

// assertGenerationGoesThroughLLMPort проверяет на собранном срезе, что генерация
// текста идёт через порт LLM (capability linguistics), а не мимо него.
func assertGenerationGoesThroughLLMPort(t *testing.T) {
	t.Helper()
	double := &countingLLM{reply: llm.ConverseReply{Text: `{"heading":"Заголовок","body":"Текст блока по материалам клиента."}`}}
	slice, err := Build(Config{LLM: double, Renderer: deterministic.New()})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := slice.GenerateLandingTexts.Handle(context.Background(), command.GenerateLandingTexts{
		BusinessDescription: "Разрабатываем сайты",
		Approved:            true,
		ApprovedBy:          "operator-1",
		ApprovedAt:          time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC),
	}); err != nil {
		t.Fatalf("GenerateLandingTexts: %v", err)
	}
	if double.calls == 0 {
		t.Fatal("генерация текстов не пошла через порт LLM (capability linguistics)")
	}
}

// assertCollectionGoesThroughCrawlPort проверяет на собранном срезе, что сбор
// сведений с сайта идёт через порт движка сбора (движок 3.1).
func assertCollectionGoesThroughCrawlPort(t *testing.T) {
	t.Helper()
	double := &countingCrawl{outcome: crawl.CrawlOutcome{Pages: []crawl.Page{{URL: "https://client.example", Markdown: "Компания"}}}}
	slice, err := Build(Config{SiteCollector: double, Renderer: deterministic.New()})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	result, err := slice.CollectSite.Handle(context.Background(), command.CollectSite{StartURL: "https://client.example"})
	if err != nil {
		t.Fatalf("CollectSite: %v", err)
	}
	if !result.Available {
		t.Fatalf("сбор через порт не выполнен: %+v", result)
	}
	if double.calls != 1 {
		t.Fatalf("порт сбора вызван %d раз, ожидался 1", double.calls)
	}
}

// countingLLM — тестовый двойник порта LLM: считает вызовы генерации.
type countingLLM struct {
	llm.Port
	calls int
	reply llm.ConverseReply
}

func (c *countingLLM) Converse(context.Context, llm.ConverseRequest) (llm.ConverseReply, llm.Usage, error) {
	c.calls++
	return c.reply, llm.Usage{TotalTokens: 1}, nil
}

// countingCrawl — тестовый двойник порта сбора: считает вызовы обхода.
type countingCrawl struct {
	crawl.Port
	calls   int
	outcome crawl.CrawlOutcome
}

func (c *countingCrawl) Crawl(context.Context, crawl.Request) (crawl.CrawlOutcome, error) {
	c.calls++
	return c.outcome, nil
}
