package main

import (
	"os"
	"path/filepath"
	"testing"
)

func topologyCatalog(contexts []Context, relationships []Relationship) Catalog {
	return Catalog{Contexts: contexts, Relationships: relationships}
}

func TestServicePerContextAcceptsOneServicePerContext(t *testing.T) {
	catalog := topologyCatalog([]Context{
		{ID: "lead-management", Service: "lead-management", Roles: []string{roleAPI, roleWorker}},
		{ID: "message-delivery", Service: "message-delivery", Roles: []string{roleSender, roleWorker, roleSchedule}},
	}, nil)
	if problems := validateServicePerContext(catalog); len(problems) != 0 {
		t.Fatalf("valid catalog rejected: %v", problems)
	}
}

func TestServicePerContextRejectsMissingMismatchedAndDuplicateService(t *testing.T) {
	catalog := topologyCatalog([]Context{
		{ID: "lead-management", Service: "", Roles: []string{roleAPI}},
		{ID: "message-delivery", Service: "other-service", Roles: []string{roleWorker}},
		{ID: "analytics", Service: "other-service", Roles: []string{roleWorker}},
	}, nil)
	problems := validateServicePerContext(catalog)
	for _, fragment := range []string{
		"не объявлен service",
		"должен совпадать с id контекста",
		"сервис обязан быть уникален",
		"должен быть ровно один сервис на контекст",
	} {
		if !containsProblem(problems, fragment) {
			t.Errorf("expected %q in %v", fragment, problems)
		}
	}
}

func TestContextRolesAcceptAllowedValues(t *testing.T) {
	catalog := topologyCatalog([]Context{
		{ID: "integration-platform", Service: "integration-platform", Roles: []string{roleAPI, roleIngest, roleSender, roleWorker, roleChains, roleSchedule}},
		{ID: "data-governance", Service: "data-governance", Roles: []string{roleAPI, roleWorker}},
	}, nil)
	if problems := validateContextRoles(catalog); len(problems) != 0 {
		t.Fatalf("valid roles rejected: %v", problems)
	}
}

func TestContextRolesRejectEmptyInvalidAndForbiddenSender(t *testing.T) {
	catalog := topologyCatalog([]Context{
		{ID: "analytics", Service: "analytics", Roles: nil},
		{ID: "billing", Service: "billing", Roles: []string{"cron"}},
		{ID: "data-governance", Service: "data-governance", Roles: []string{roleSender}},
	}, nil)
	problems := validateContextRoles(catalog)
	for _, fragment := range []string{
		"roles пусты",
		`недопустимая роль "cron"`,
		"роль sender запрещена",
	} {
		if !containsProblem(problems, fragment) {
			t.Errorf("expected %q in %v", fragment, problems)
		}
	}
}

func TestRelationshipTransportsAreConsistent(t *testing.T) {
	catalog := topologyCatalog(nil, []Relationship{
		{ID: "outreach-to-delivery", Integration: "command", Transport: transportSync, Mechanism: mechanismHTTPJSON},
		{ID: "lead-to-audience", Integration: "query", Transport: transportSync, Mechanism: mechanismHTTPJSON},
		{ID: "delivery-to-observation", Integration: "event", Transport: transportAsync, Mechanism: mechanismAMQP},
		{ID: "governance-policy-for-leads", Integration: "policy", Transport: transportAsync, Mechanism: mechanismAMQP},
	})
	if problems := validateRelationshipTransports(catalog); len(problems) != 0 {
		t.Fatalf("valid transports rejected: %v", problems)
	}
}

func TestRelationshipTransportMissingAndIncompatibleAreRejected(t *testing.T) {
	catalog := topologyCatalog(nil, []Relationship{
		{ID: "no-transport", Integration: "command", Mechanism: mechanismHTTPJSON},
		{ID: "wrong-transport", Integration: "event", Transport: transportSync, Mechanism: mechanismHTTPJSON},
		{ID: "no-mechanism", Integration: "query", Transport: transportSync},
		{ID: "unknown-integration", Integration: "broadcast"},
	})
	problems := validateRelationshipTransports(catalog)
	for _, fragment := range []string{
		"relationships.no-transport: не объявлен transport",
		`relationships.wrong-transport: transport "sync" несовместим`,
		`relationships.no-mechanism: не объявлен mechanism`,
		`relationships.unknown-integration: неизвестный тип интеграции`,
	} {
		if !containsProblem(problems, fragment) {
			t.Errorf("expected %q in %v", fragment, problems)
		}
	}
}

func deployFixture() DeployConfig {
	return DeployConfig{Processes: []DeployProcess{
		{Name: "core", Mechanism: "in-process", Services: []DeployService{
			{ID: "outreach-orchestration", Roles: []string{roleAPI, roleWorker, roleChains, roleSchedule}},
			{ID: "message-delivery", Roles: []string{roleWorker}},
		}},
		{Name: "delivery", Mechanism: "in-process", Services: []DeployService{
			{ID: "message-delivery", Roles: []string{roleSender}},
		}},
		{Name: "analytics", Mechanism: mechanismHTTPJSON, Services: []DeployService{
			{ID: "analytics", Roles: []string{roleAPI}},
		}},
	}}
}

func deployCatalog() Catalog {
	return topologyCatalog([]Context{
		{ID: "outreach-orchestration", Service: "outreach-orchestration", Roles: []string{roleAPI, roleWorker, roleChains, roleSchedule}},
		{ID: "message-delivery", Service: "message-delivery", Roles: []string{roleSender, roleWorker, roleSchedule}},
		{ID: "analytics", Service: "analytics", Roles: []string{roleAPI, roleWorker}},
	}, nil)
}

func TestDeployMatchesCatalog(t *testing.T) {
	if problems := validateDeploy(deployCatalog(), deployFixture()); len(problems) != 0 {
		t.Fatalf("consistent deploy rejected: %v", problems)
	}
}

func TestDeployRejectsUnknownServiceAndForeignRole(t *testing.T) {
	catalog := deployCatalog()
	deploy := DeployConfig{Processes: []DeployProcess{
		{Name: "core", Mechanism: "in-process", Services: []DeployService{
			{ID: "unknown-service", Roles: []string{roleWorker}},
			{ID: "message-delivery", Roles: []string{roleAPI}},
		}},
	}}
	problems := validateDeploy(catalog, deploy)
	for _, fragment := range []string{
		`неизвестный сервис "unknown-service"`,
		`роль "api" не объявлена сервисом`,
	} {
		if !containsProblem(problems, fragment) {
			t.Errorf("expected %q in %v", fragment, problems)
		}
	}
}

func TestDeployAcceptsRolesSplitAcrossProcesses(t *testing.T) {
	catalog := deployCatalog()
	deploy := DeployConfig{Processes: []DeployProcess{
		{Name: "core", Mechanism: "in-process", Services: []DeployService{
			{ID: "outreach-orchestration", Roles: []string{roleAPI, roleWorker, roleChains}},
			{ID: "message-delivery", Roles: []string{roleWorker, roleSchedule}},
			{ID: "analytics", Roles: []string{roleAPI, roleWorker}},
		}},
		{Name: "delivery", Mechanism: "in-process", Services: []DeployService{
			{ID: "outreach-orchestration", Roles: []string{roleSchedule}},
			{ID: "message-delivery", Roles: []string{roleSender}},
		}},
	}}
	if problems := validateDeploy(catalog, deploy); len(problems) != 0 {
		t.Fatalf("roles split across processes rejected: %v", problems)
	}
}

func TestDeployRejectsRoleDeclaredInTwoProcesses(t *testing.T) {
	catalog := deployCatalog()
	deploy := DeployConfig{Processes: []DeployProcess{
		{Name: "core", Mechanism: "in-process", Services: []DeployService{
			{ID: "outreach-orchestration", Roles: []string{roleAPI, roleWorker, roleChains, roleSchedule}},
			{ID: "message-delivery", Roles: []string{roleWorker, roleSchedule}},
			{ID: "analytics", Roles: []string{roleAPI, roleWorker}},
		}},
		{Name: "delivery", Mechanism: "in-process", Services: []DeployService{
			{ID: "message-delivery", Roles: []string{roleSender}},
			{ID: "analytics", Roles: []string{roleWorker}},
		}},
	}}
	problems := validateDeploy(catalog, deploy)
	if !containsProblem(problems, `роль "worker" сервиса "analytics" объявлена в нескольких процессах`) {
		t.Fatalf("expected duplicate-role error: %v", problems)
	}
}

func TestDeployRejectsSenderAndAPIInSameProcess(t *testing.T) {
	catalog := deployCatalog()
	deploy := DeployConfig{Processes: []DeployProcess{
		{Name: "core", Mechanism: "in-process", Services: []DeployService{
			{ID: "outreach-orchestration", Roles: []string{roleAPI, roleWorker, roleChains, roleSchedule}},
			{ID: "message-delivery", Roles: []string{roleSender, roleWorker, roleSchedule}},
			{ID: "analytics", Roles: []string{roleAPI, roleWorker}},
		}},
	}}
	problems := validateDeploy(catalog, deploy)
	if !containsProblem(problems, `deploy.core: процесс совмещает роли "sender" и "api"`) {
		t.Fatalf("expected sender/api isolation error: %v", problems)
	}
}

func TestDeployAcceptsSenderIsolatedFromAPI(t *testing.T) {
	catalog := deployCatalog()
	deploy := deployFixture()
	for _, process := range deploy.Processes {
		roles := make(map[string]bool)
		for _, service := range process.Services {
			for _, role := range service.Roles {
				roles[role] = true
			}
		}
		if roles[roleSender] && roles[roleAPI] {
			t.Fatalf("fixture mixes sender and api in process %q", process.Name)
		}
	}
	if problems := validateDeploy(catalog, deploy); len(problems) != 0 {
		t.Fatalf("sender-isolated deploy rejected: %v", problems)
	}
}

func TestDeployRejectsMechanismIncompatibleWithRole(t *testing.T) {
	catalog := deployCatalog()
	deploy := DeployConfig{Processes: []DeployProcess{
		{Name: "core", Mechanism: mechanismHTTPJSON, Services: []DeployService{
			{ID: "outreach-orchestration", Roles: []string{roleWorker}},
			{ID: "message-delivery", Roles: []string{roleSender}},
		}},
		{Name: "analytics", Mechanism: mechanismAMQP, Services: []DeployService{
			{ID: "analytics", Roles: []string{roleAPI}},
		}},
	}}
	problems := validateDeploy(catalog, deploy)
	if !containsProblem(problems, "не совпадает с режимом роли") {
		t.Fatalf("expected mechanism mismatch: %v", problems)
	}
}

func TestProductionDeployMatchesCatalog(t *testing.T) {
	root := repositoryRoot(t)
	deploy, err := loadDeploy(filepath.Join(root, "deploy", "services.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if problems := validateDeploy(canonicalCatalog(t), deploy); len(problems) != 0 {
		t.Fatalf("production deploy is inconsistent with catalog: %v", problems)
	}
}

func TestParseDeployConfigFlowAndBlockLists(t *testing.T) {
	data := []byte(`# комментарий
processes:
  - name: core
    services:
      - id: outreach-orchestration
        roles: [api, worker]
        mechanism: http-json
      - id: message-delivery
        roles:
          - sender
          - worker
        mechanism: amqp
`)
	config, err := parseDeployConfig(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Processes) != 1 || config.Processes[0].Name != "core" {
		t.Fatalf("unexpected processes: %+v", config.Processes)
	}
	services := config.Processes[0].Services
	if len(services) != 2 {
		t.Fatalf("unexpected services: %+v", services)
	}
	if services[0].ID != "outreach-orchestration" || len(services[0].Roles) != 2 || services[0].Mechanism != mechanismHTTPJSON {
		t.Fatalf("unexpected first service: %+v", services[0])
	}
	if services[1].ID != "message-delivery" || len(services[1].Roles) != 2 || services[1].Roles[0] != roleSender || services[1].Mechanism != mechanismAMQP {
		t.Fatalf("unexpected second service: %+v", services[1])
	}
}

// writeModule создаёт contexts/<id>/go.mod с заданным содержимым.
func writeModule(t *testing.T, root, id, content string) {
	t.Helper()
	dir := filepath.Join(root, "backend", "contexts", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func moduleCatalog() Catalog {
	return topologyCatalog([]Context{
		{ID: "outreach-orchestration", Service: "outreach-orchestration", Roles: []string{roleAPI, roleWorker}},
		{ID: "message-delivery", Service: "message-delivery", Roles: []string{roleSender, roleWorker}},
	}, nil)
}

func TestServiceModulesAcceptMatchingModule(t *testing.T) {
	root := t.TempDir()
	writeModule(t, root, "outreach-orchestration", "module lead-gen/backend/contexts/outreach-orchestration\n\ngo 1.25\n")
	if problems := validateServiceModules(root, moduleCatalog()); len(problems) != 0 {
		t.Fatalf("valid module rejected: %v", problems)
	}
}

func TestServiceModulesAcceptAbsentContextsDir(t *testing.T) {
	if problems := validateServiceModules(t.TempDir(), moduleCatalog()); len(problems) != 0 {
		t.Fatalf("absent contexts dir rejected: %v", problems)
	}
}

func TestServiceModulesRejectUnknownDirectoryAndWrongModulePath(t *testing.T) {
	root := t.TempDir()
	writeModule(t, root, "outreach-orchestration", "module lead-gen/backend/contexts/other\n")
	writeModule(t, root, "unknown-service", "module lead-gen/backend/contexts/unknown-service\n")
	problems := validateServiceModules(root, moduleCatalog())
	for _, fragment := range []string{
		`backend/contexts/outreach-orchestration/go.mod: объявляет module "lead-gen/backend/contexts/other", ожидалось "lead-gen/backend/contexts/outreach-orchestration"`,
		"backend/contexts/unknown-service: каталог модуля не соответствует ни одному сервису каталога",
	} {
		if !containsProblem(problems, fragment) {
			t.Errorf("expected %q in %v", fragment, problems)
		}
	}
}

func TestServiceModulesRejectMissingGoMod(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "backend", "contexts", "outreach-orchestration"), 0o755); err != nil {
		t.Fatal(err)
	}
	problems := validateServiceModules(root, moduleCatalog())
	if !containsProblem(problems, "отсутствует go.mod") {
		t.Fatalf("expected missing go.mod error: %v", problems)
	}
}

func TestProductionServiceModulesMatchCatalog(t *testing.T) {
	root := repositoryRoot(t)
	if problems := validateServiceModules(root, canonicalCatalog(t)); len(problems) != 0 {
		t.Fatalf("production contexts/ incompatible with catalog: %v", problems)
	}
}
