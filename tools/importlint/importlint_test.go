package main

import (
	"path/filepath"
	"testing"
)

// TestForbiddenEdges проверяет, что детектор находит каждое запрещённое ребро
// из отрицательных фикстур новой раскладки (contexts/<ctx>/internal, contracts,
// frontend/public).
func TestForbiddenEdges(t *testing.T) {
	cases := []struct {
		fixture  string
		imported string
	}{
		{"domain_to_adapters", "lead-gen/backend/contexts/alpha/internal/adapters"},
		{"domain_to_app", "lead-gen/backend/contexts/alpha/internal/app"},
		{"app_to_foreign_domain", "lead-gen/backend/contexts/beta/internal/domain"},
		{"foreign_persistence", "lead-gen/backend/contexts/beta/internal/adapters/persistence/postgres"},
		{"foreign_app", "lead-gen/backend/contexts/beta/internal/app"},
		{"foreign_ports", "lead-gen/backend/contexts/beta/internal/ports"},
		{"foreign_internal", "lead-gen/backend/contexts/beta/internal/ports/contracts"},
		{"foreign_root", "lead-gen/backend/contexts/beta"},
		{"edge_to_context", "lead-gen/backend/contexts/alpha"},
		{"edge_to_persistence", "lead-gen/backend/contexts/beta/internal/adapters/persistence/postgres"},
	}

	for _, testCase := range cases {
		t.Run(testCase.fixture, func(t *testing.T) {
			root := filepath.Join("testdata", testCase.fixture)
			analysis, err := analyze(root, defaultModule)
			if err != nil {
				t.Fatalf("analyze(%s): %v", root, err)
			}
			if len(analysis.Violations) != 1 {
				t.Fatalf("ожидалось 1 нарушение, получено %d: %+v", len(analysis.Violations), analysis.Violations)
			}
			if got := analysis.Violations[0].Imported; got != testCase.imported {
				t.Fatalf("импорт нарушителя: получено %q, ожидалось %q", got, testCase.imported)
			}
			if analysis.Violations[0].Reason == "" {
				t.Fatal("нарушение без причины")
			}
		})
	}
}

// TestCrossContextEdges таблично фиксирует правило межконтекстных импортов:
// чужой контекст доступен только через lead-gen/contracts/<owner>.
func TestCrossContextEdges(t *testing.T) {
	consumer := "lead-gen/backend/contexts/alpha"
	cases := []struct {
		name     string
		importer string
		imported string
		allowed  bool
	}{
		{"чужой domain", consumer + "/internal/app", "lead-gen/backend/contexts/beta/internal/domain", false},
		{"чужой app", consumer + "/internal/app", "lead-gen/backend/contexts/beta/internal/app", false},
		{"чужое хранилище", consumer + "/internal/app", "lead-gen/backend/contexts/beta/internal/adapters/persistence", false},
		{"чужое хранилище вложенное", consumer + "/internal/app", "lead-gen/backend/contexts/beta/internal/adapters/persistence/postgres", false},
		{"чужой adapters не хранилище", consumer + "/internal/app", "lead-gen/backend/contexts/beta/internal/adapters/http", false},
		{"чужой internal/ports", consumer + "/internal/app", "lead-gen/backend/contexts/beta/internal/ports", false},
		{"чужой internal/ports/contracts", consumer + "/internal/app", "lead-gen/backend/contexts/beta/internal/ports/contracts", false},
		{"чужой корневой пакет", consumer + "/internal/app", "lead-gen/backend/contexts/beta", false},
		{"чужой корневой пакет из корня потребителя", consumer, "lead-gen/backend/contexts/beta", false},
		{"контракт владельца из адаптера", consumer + "/internal/adapters/http", "lead-gen/contracts/beta", true},
		{"вложенный контракт владельца", consumer + "/internal/adapters/http", "lead-gen/contracts/beta/envelope", true},
		{"свой layer без изменений", consumer + "/internal/app", consumer + "/internal/domain", true},
		{"свой корневой пакет", consumer + "/internal/app", consumer, true},
		{"platform не контекст", consumer + "/internal/domain", "lead-gen/backend/platform", true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			reason := forbiddenLayer(parseRole(defaultModule, testCase.importer), parseRole(defaultModule, testCase.imported))
			if testCase.allowed && reason != "" {
				t.Fatalf("ребро %s -> %s должно быть допустимо, получено %q", testCase.importer, testCase.imported, reason)
			}
			if !testCase.allowed && reason == "" {
				t.Fatalf("ребро %s -> %s должно быть запрещено", testCase.importer, testCase.imported)
			}
		})
	}
}

// TestForeignContractsAllowed проверяет отсутствие ложных срабатываний на
// фикстуре, где транспортный адаптер потребителя импортирует опубликованный
// контракт владельца lead-gen/contracts/<owner>.
func TestForeignContractsAllowed(t *testing.T) {
	root := filepath.Join("testdata", "contracts_allowed")
	analysis, err := analyze(root, defaultModule)
	if err != nil {
		t.Fatalf("analyze(%s): %v", root, err)
	}
	if len(analysis.Violations) != 0 {
		t.Fatalf("импорт чужого lead-gen/contracts не должен нарушать: %+v", analysis.Violations)
	}
}

// TestEdgeCleanAllowed проверяет, что публичный edge вправе импортировать
// platform.
func TestEdgeCleanAllowed(t *testing.T) {
	root := filepath.Join("testdata", "edge_clean")
	analysis, err := analyze(root, defaultModule)
	if err != nil {
		t.Fatalf("analyze(%s): %v", root, err)
	}
	if len(analysis.Violations) != 0 {
		t.Fatalf("edge -> platform не должен нарушать: %+v", analysis.Violations)
	}
}

// TestCleanFixture проверяет отсутствие ложных срабатываний на корректной
// фикстуре: domain -> platform, app -> domain/ports, adapters -> domain/ports,
// корневой пакет контекста -> свои adapters, межконтекстный импорт только через
// contracts, cmd и edge не ограничены за своими правилами.
func TestCleanFixture(t *testing.T) {
	root := filepath.Join("testdata", "clean")
	analysis, err := analyze(root, defaultModule)
	if err != nil {
		t.Fatalf("analyze(%s): %v", root, err)
	}
	if len(analysis.Violations) != 0 {
		t.Fatalf("ложные срабатывания на чистой фикстуре: %+v", analysis.Violations)
	}
	if analysis.Files == 0 {
		t.Fatal("не разобран ни один файл фикстуры")
	}
}

// TestRealTreeClean проверяет отсутствие нарушений на реальном дереве проекта.
// Прогон также подтверждает, что testdata исключается из обхода: иначе
// отрицательные фикстуры дали бы срабатывания.
func TestRealTreeClean(t *testing.T) {
	root := defaultRoot()
	module := readModulePath(root)
	if module != defaultModule {
		t.Fatalf("путь модуля: получено %q, ожидалось %q", module, defaultModule)
	}
	analysis, err := analyze(root, module)
	if err != nil {
		t.Fatalf("analyze(%s): %v", root, err)
	}
	if len(analysis.Violations) != 0 {
		t.Fatalf("нарушения на реальном дереве: %+v", analysis.Violations)
	}
	if analysis.Files == 0 {
		t.Fatal("на реальном дереве не разобрано ни одного файла")
	}
}

// TestParseRole фиксирует разбор ролей пакетов по путям импорта новой
// раскладки.
func TestParseRole(t *testing.T) {
	cases := []struct {
		path string
		want Role
	}{
		{
			"lead-gen/backend/contexts/alpha/internal/domain",
			Role{Kind: kindContext, Internal: true, Context: "alpha", Layer: "domain"},
		},
		{
			"lead-gen/backend/contexts/alpha/internal/adapters/persistence/postgres",
			Role{Kind: kindContext, Internal: true, Context: "alpha", Layer: "adapters", Subpath: "persistence/postgres"},
		},
		{
			"lead-gen/backend/contexts/alpha/internal/ports/contracts/envelope",
			Role{Kind: kindContext, Internal: true, Context: "alpha", Layer: "ports", Subpath: "contracts/envelope"},
		},
		{
			"lead-gen/backend/contexts/alpha",
			Role{Kind: kindContext, Context: "alpha"},
		},
		{
			"lead-gen/contracts/beta",
			Role{Kind: kindContract, Owner: "beta"},
		},
		{
			"lead-gen/contracts/beta/envelope",
			Role{Kind: kindContract, Owner: "beta", Subpath: "envelope"},
		},
		{
			"lead-gen/backend/platform",
			Role{Kind: kindPlatform},
		},
		{
			"lead-gen/backend/platform/amqp",
			Role{Kind: kindPlatform},
		},
		{
			"lead-gen/backend/cmd/lead-gen",
			Role{Kind: kindCmd},
		},
		{
			"lead-gen/frontend/public",
			Role{Kind: kindEdge, Subpath: "public"},
		},
		{
			"lead-gen/frontend/components",
			Role{Kind: kindEdge, Subpath: "components"},
		},
		{
			"lead-gen/contexts/alpha",
			Role{},
		},
		{
			"lead-gen/platform",
			Role{},
		},
		{
			"lead-gen/cmd/lead-gen",
			Role{},
		},
		{
			"lead-gen/backend",
			Role{},
		},
		{
			"lead-gen/internal/platform",
			Role{},
		},
		{
			"lead-gen/internal/contexts/alpha/domain",
			Role{},
		},
		{
			"example.com/external",
			Role{},
		},
	}
	for _, testCase := range cases {
		if got := parseRole(defaultModule, testCase.path); got != testCase.want {
			t.Errorf("parseRole(%q) = %+v, ожидалось %+v", testCase.path, got, testCase.want)
		}
	}
}
