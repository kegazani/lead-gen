package main

import (
	"os"
	"path/filepath"
	"testing"
)

// infrastructureCatalog — минимальный каталог для проверок согласованности.
func infrastructureCatalog() Catalog {
	return topologyCatalog([]Context{
		{ID: "outreach-orchestration", Service: "outreach-orchestration", Roles: []string{roleAPI}},
		{ID: "data-governance", Service: "data-governance", Roles: []string{roleWorker}},
	}, nil)
}

// infrastructureDeploy — минимальный деплой для проверок согласованности.
func infrastructureDeploy() DeployConfig {
	return DeployConfig{Processes: []DeployProcess{
		{Name: "core", Mechanism: "in-process", Services: []DeployService{
			{ID: "outreach-orchestration", Roles: []string{roleAPI}},
		}},
		{Name: "platform", Mechanism: "in-process", Services: []DeployService{
			{ID: "data-governance", Roles: []string{roleWorker}},
		}},
	}}
}

func TestInfrastructureAcceptsValidFixture(t *testing.T) {
	config, err := parseInfrastructure([]byte(`nodes:
  - id: postgres-green
    kind: postgres
    label: "PostgreSQL, зелёная зона"
    zone: green
    addresses: ["postgres-green:5432"]
  - id: rabbitmq
    kind: broker
    label: "RabbitMQ"
    addresses: ["rabbitmq:5672"]
  - id: object-storage
    kind: object-storage
    label: "S3-совместимое хранилище"
  - id: external-providers
    kind: external
    label: "Внешние провайдеры"
`))
	if err != nil {
		t.Fatalf("parseInfrastructure: %v", err)
	}
	if len(config.Nodes) != 4 {
		t.Fatalf("узлов: %d, ожидалось 4", len(config.Nodes))
	}
	if problems := validateInfrastructure(infrastructureCatalog(), infrastructureDeploy(), config); len(problems) != 0 {
		t.Fatalf("валидная фикстура отвергнута: %v", problems)
	}
}

func TestInfrastructureRejectsUnknownKind(t *testing.T) {
	config, err := parseInfrastructure([]byte("nodes:\n  - id: x\n    kind: quantum\n    label: \"X\"\n    addresses: [\"x:1\"]\n"))
	if err != nil {
		t.Fatalf("parseInfrastructure: %v", err)
	}
	problems := validateInfrastructure(infrastructureCatalog(), infrastructureDeploy(), config)
	if !containsProblem(problems, `неизвестный вид "quantum"`) {
		t.Fatalf("ожидалась ошибка неизвестного вида: %v", problems)
	}
}

func TestInfrastructureRejectsEmptyAndMissingAddress(t *testing.T) {
	config, err := parseInfrastructure([]byte("nodes:\n  - id: x\n    kind: postgres\n    label: \"X\"\n    addresses: [\"\"]\n"))
	if err != nil {
		t.Fatalf("parseInfrastructure: %v", err)
	}
	problems := validateInfrastructure(infrastructureCatalog(), infrastructureDeploy(), config)
	if !containsProblem(problems, "пустой адрес") {
		t.Fatalf("ожидалась ошибка пустого адреса: %v", problems)
	}

	missing, err := parseInfrastructure([]byte("nodes:\n  - id: y\n    kind: broker\n    label: \"Y\"\n"))
	if err != nil {
		t.Fatalf("parseInfrastructure: %v", err)
	}
	problems = validateInfrastructure(infrastructureCatalog(), infrastructureDeploy(), missing)
	if !containsProblem(problems, "требует адрес") {
		t.Fatalf("ожидалась ошибка отсутствующего адреса: %v", problems)
	}
}

func TestInfrastructureRejectsUnknownServiceReference(t *testing.T) {
	config, err := parseInfrastructure([]byte("nodes:\n  - id: ghost\n    kind: service\n    label: \"Ghost\"\n"))
	if err != nil {
		t.Fatalf("parseInfrastructure: %v", err)
	}
	problems := validateInfrastructure(infrastructureCatalog(), infrastructureDeploy(), config)
	if !containsProblem(problems, "не найден в каталоге") {
		t.Fatalf("ожидалась ошибка ссылки на несуществующий сервис: %v", problems)
	}
}

func TestInfrastructureRejectsCatalogServiceCollision(t *testing.T) {
	config, err := parseInfrastructure([]byte("nodes:\n  - id: data-governance\n    kind: postgres\n    label: \"X\"\n    addresses: [\"x:1\"]\n"))
	if err != nil {
		t.Fatalf("parseInfrastructure: %v", err)
	}
	problems := validateInfrastructure(infrastructureCatalog(), infrastructureDeploy(), config)
	if !containsProblem(problems, "идентификатор совпадает с сервисом каталога") {
		t.Fatalf("ожидалась ошибка совпадения идентификатора: %v", problems)
	}
}

func TestInfrastructureParseRejectsUnknownKey(t *testing.T) {
	if _, err := parseInfrastructure([]byte("nodes:\n  - id: x\n    kind: broker\n    label: \"X\"\n    bogus: 1\n")); err == nil {
		t.Fatal("неизвестный ключ узла не отвергнут")
	}
}

func TestProductionInfrastructureMatchesCatalogAndDeploy(t *testing.T) {
	root := repositoryRoot(t)
	catalog := canonicalCatalog(t)
	deploy, err := loadDeploy(filepath.Join(root, "deploy", "services.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	infrastructure, err := loadInfrastructure(filepath.Join(root, "deploy", "infrastructure.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if problems := validateInfrastructure(catalog, deploy, infrastructure); len(problems) != 0 {
		t.Fatalf("боевая инфраструктура несогласована с каталогом/деплоем: %v", problems)
	}
}

// TestRunSurfacesInvalidInfrastructure подтверждает, что негативная фикстура даёт
// инфраструктурную ошибку в общем прогоне (а значит, ненулевой код CLI).
func TestRunSurfacesInvalidInfrastructure(t *testing.T) {
	root := repositoryRoot(t)
	fixtureRoot := t.TempDir()
	for _, relative := range []string{
		filepath.Join("architecture", "domain-catalog.yaml"),
		filepath.Join("architecture", "domain-catalog.schema.json"),
		filepath.Join("deploy", "services.yaml"),
	} {
		data, err := os.ReadFile(filepath.Join(root, relative))
		if err != nil {
			t.Fatal(err)
		}
		destination := filepath.Join(fixtureRoot, relative)
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	invalid := filepath.Join(fixtureRoot, "deploy", "infrastructure.yaml")
	if err := os.WriteFile(invalid, []byte("nodes:\n  - id: x\n    kind: quantum\n    label: \"X\"\n    addresses: [\"x:1\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, problems := run(fixtureRoot,
		filepath.Join(fixtureRoot, "architecture", "domain-catalog.yaml"),
		filepath.Join(fixtureRoot, "architecture", "domain-catalog.schema.json"))
	if !containsProblem(problems, `неизвестный вид "quantum"`) {
		t.Fatalf("инфраструктурная ошибка не попала в общий прогон: %v", problems)
	}
}
