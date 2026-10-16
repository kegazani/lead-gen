package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func inProcessFixture(t *testing.T) (string, Catalog) {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"deploy/client/services.yaml":       "processes:\n  - name: client\n    mechanism: in-process\n    services:\n      - id: consumer\n        roles: [api, worker]\n",
		"contracts/owner/item/item.go":      "package item\ntype Port interface { Read() }\n",
		"backend/contexts/owner/service.go": "package owner\nfunc New() {}\n",
		"backend/cmd/app/runtime.go":        "package main\nimport owner \"lead-gen/backend/contexts/owner\"\nfunc build() { owner.New() }\n",
	}
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	assembly := InProcessAssembly{Version: 1, ID: "client", Process: "client", ActivationFlag: "client-enabled", Deployment: "deploy/client/services.yaml", CompositionRoot: "backend/cmd/app/runtime.go", RequiredRoles: []DeployService{{ID: "consumer", Roles: []string{roleAPI, roleWorker}}}, Bindings: []InProcessBinding{{Owner: "owner", Contract: "item", InterfaceFile: "contracts/owner/item/item.go", InterfaceName: "Port", ConstructorFile: "backend/contexts/owner/service.go", Constructor: "New"}}}
	raw, err := json.Marshal(assembly)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "deploy/client.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return root, Catalog{Contexts: []Context{{ID: "owner", Contracts: []string{"item"}}, {ID: "consumer", Roles: []string{roleAPI, roleWorker}}}, Relationships: []Relationship{{ID: "items", Upstream: "owner", Downstream: "consumer", Contract: "item", Integration: "query", Transport: transportSync, Mechanism: mechanismInProcess, CoLocation: "client"}}}
}

func TestInProcessAllowsDeclaredOwnerInterfaceInClientAssembly(t *testing.T) {
	root, catalog := inProcessFixture(t)
	for _, kind := range []string{"query", "command"} {
		catalog.Relationships[0].Integration = kind
		if got := validateRelationshipTransports(catalog); len(got) > 0 {
			t.Fatal(got)
		}
		if got := validateInProcessBindings(root, catalog); len(got) > 0 {
			t.Fatal(got)
		}
	}
}

func TestInProcessRejectsMissingDeploymentConstraintsAndOwnerBinding(t *testing.T) {
	for _, tc := range []struct{ name, path, content, want string }{
		{"missing-required-role", "deploy/client/services.yaml", "processes:\n  - name: client\n    mechanism: in-process\n    services:\n      - id: consumer\n        roles: [api]\n", "обязательная роль consumer/worker"},
		{"wrong-process-mode", "deploy/client/services.yaml", "processes:\n  - name: client\n    mechanism: http-json\n    services:\n      - id: consumer\n        roles: [api, worker]\n", "не объявлен как in-process"},
		{"missing-interface", "contracts/owner/item/item.go", "package item\ntype Port struct{}\n", "опубликованный интерфейс"},
		{"missing-constructor-call", "backend/cmd/app/runtime.go", "package main\nfunc build() {}\n", "не импортирует публичный пакет"},
		{"missing-constructor", "backend/contexts/owner/service.go", "package owner\nfunc Other() {}\n", "публичный конструктор"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, catalog := inProcessFixture(t)
			if err := os.WriteFile(filepath.Join(root, tc.path), []byte(tc.content), 0600); err != nil {
				t.Fatal(err)
			}
			if got := validateInProcessBindings(root, catalog); !containsProblem(got, tc.want) {
				t.Fatalf("ожидалась ошибка %q: %v", tc.want, got)
			}
		})
	}
	root, catalog := inProcessFixture(t)
	catalog.Relationships[0].CoLocation = ""
	if got := validateInProcessBindings(root, catalog); !containsProblem(got, "co_location") {
		t.Fatal(got)
	}
	catalog.Relationships[0].CoLocation = "absent"
	if got := validateInProcessBindings(root, catalog); !containsProblem(got, "deployment-описание отсутствует") {
		t.Fatal(got)
	}
}

func TestInProcessNeverReplacesEventOrPolicyAMQP(t *testing.T) {
	root, catalog := inProcessFixture(t)
	for _, kind := range []string{"event", "policy"} {
		catalog.Relationships[0].Integration = kind
		catalog.Relationships[0].Transport = transportAsync
		got := validateRelationshipTransports(catalog)
		if !containsProblem(got, `mechanism "in-process" несовместим`) {
			t.Fatalf("%s принято: %v", kind, got)
		}
		if got := validateInProcessBindings(root, catalog); !containsProblem(got, "разрешён только sync command/query") {
			t.Fatalf("%s принято: %v", kind, got)
		}
	}
}

func TestInProcessRejectsUnownedContractAndConsumerOutsideAssembly(t *testing.T) {
	root, catalog := inProcessFixture(t)
	catalog.Contexts[0].Contracts = nil
	if got := validateInProcessBindings(root, catalog); !containsProblem(got, "контракт не опубликован") {
		t.Fatal(got)
	}
	catalog.Contexts[0].Contracts = []string{"item"}
	catalog.Relationships[0].Downstream = "other"
	if got := validateInProcessBindings(root, catalog); !containsProblem(got, "не размещён в процессе") {
		t.Fatal(got)
	}
}

func TestInProcessRejectsConstructorPathOutsidePublicOwner(t *testing.T) {
	root, catalog := inProcessFixture(t)
	path := filepath.Join(root, "deploy/client.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.ReplaceAll(string(raw), "backend/contexts/owner/service.go", "backend/cmd/app/runtime.go"))
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if got := validateInProcessBindings(root, catalog); !containsProblem(got, "вне разрешённого раздела") {
		t.Fatal(got)
	}
}

func TestInProcessConsumerLibraryRequiresActualPublicConstructor(t *testing.T) {
	root, catalog := inProcessFixture(t)
	catalog.Contexts = append(catalog.Contexts, Context{ID: "library", Contracts: []string{"reader"}})
	catalog.Relationships[0].Downstream = "library"
	files := map[string]string{
		"contracts/library/reader/reader.go":  "package reader\ntype Port interface { Read() }\n",
		"backend/contexts/library/service.go": "package library\nfunc NewReader() {}\n",
	}
	for name, value := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(root, "deploy/client.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var assembly InProcessAssembly
	if err = json.Unmarshal(raw, &assembly); err != nil {
		t.Fatal(err)
	}
	assembly.Bindings = append(assembly.Bindings, InProcessBinding{Owner: "library", Contract: "reader", InterfaceFile: "contracts/library/reader/reader.go", InterfaceName: "Port", ConstructorFile: "backend/contexts/library/service.go", Constructor: "NewReader"})
	raw, err = json.Marshal(assembly)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if got := validateInProcessBindings(root, catalog); !containsProblem(got, "не размещён в процессе") {
		t.Fatal(got)
	}
	composition := "package main\nimport owner \"lead-gen/backend/contexts/owner\"\nimport library \"lead-gen/backend/contexts/library\"\nfunc build() { owner.New(); library.NewReader() }\n"
	if err = os.WriteFile(filepath.Join(root, "backend/cmd/app/runtime.go"), []byte(composition), 0600); err != nil {
		t.Fatal(err)
	}
	if got := validateInProcessBindings(root, catalog); len(got) != 0 {
		t.Fatal(got)
	}
	catalog.Contexts[2].Contracts = nil
	if got := validateInProcessBindings(root, catalog); !containsProblem(got, "не размещён в процессе") {
		t.Fatal(got)
	}
}

func TestInProcessPreservesSenderAPIIsolation(t *testing.T) {
	root, catalog := inProcessFixture(t)
	catalog.Contexts[1].Roles = append(catalog.Contexts[1].Roles, roleSender)
	deployment := "processes:\n  - name: client\n    mechanism: in-process\n    services:\n      - id: consumer\n        roles: [api, worker, sender]\n"
	if err := os.WriteFile(filepath.Join(root, "deploy/client/services.yaml"), []byte(deployment), 0600); err != nil {
		t.Fatal(err)
	}
	if got := validateInProcessBindings(root, catalog); !containsProblem(got, "не совмещает sender с API") {
		t.Fatal(got)
	}
}
