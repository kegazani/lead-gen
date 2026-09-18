package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func canonicalCatalog(t *testing.T) Catalog {
	t.Helper()
	var catalog Catalog
	if err := loadJSON(filepath.Join(repositoryRoot(t), "architecture", "domain-catalog.yaml"), &catalog); err != nil {
		t.Fatal(err)
	}
	return catalog
}

func cloneCatalog(t *testing.T, source Catalog) Catalog {
	t.Helper()
	data, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	var clone Catalog
	if err := json.Unmarshal(data, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func containsProblem(problems []string, fragment string) bool {
	for _, problem := range problems {
		if strings.Contains(problem, fragment) {
			return true
		}
	}
	return false
}

func TestSchemaAcceptsCanonicalCatalog(t *testing.T) {
	root := repositoryRoot(t)
	var catalog any
	var schema map[string]any
	if err := loadJSON(filepath.Join(root, "architecture", "domain-catalog.yaml"), &catalog); err != nil {
		t.Fatal(err)
	}
	if err := loadJSON(filepath.Join(root, "architecture", "domain-catalog.schema.json"), &schema); err != nil {
		t.Fatal(err)
	}
	if problems := validateSchema(catalog, schema, schema, "$"); len(problems) != 0 {
		t.Fatalf("schema rejected canonical catalog: %v", problems)
	}
}

func TestCanonicalCatalogPassesSemanticValidation(t *testing.T) {
	root := repositoryRoot(t)
	capabilities, err := discoverCapabilities(root)
	if err != nil {
		t.Fatal(err)
	}
	if problems := validateCatalog(canonicalCatalog(t), capabilities); len(problems) != 0 {
		t.Fatalf("canonical catalog is invalid: %v", problems)
	}
}

func TestAggregateReferencesAndUniquenessAreValidated(t *testing.T) {
	catalog := cloneCatalog(t, canonicalCatalog(t))
	catalog.Aggregates[0].OwnerContext = "missing-context"
	catalog.Aggregates[0].RootEntity = "missing-entity"
	catalog.Aggregates[1].ValueObjects = append(catalog.Aggregates[1].ValueObjects, catalog.Aggregates[0].ValueObjects[0])
	catalog.Aggregates[1].Entities = append(catalog.Aggregates[1].Entities, catalog.Aggregates[2].Entities[0])
	problems := validateCatalog(catalog, map[string]bool{})
	for _, fragment := range []string{"неизвестный owner_context", "неизвестный root_entity", "уже объявлен", "принадлежит context"} {
		if !containsProblem(problems, fragment) {
			t.Errorf("expected %q in %v", fragment, problems)
		}
	}
}

func TestDuplicateEntityAndRelationshipSelfLoopAreRejected(t *testing.T) {
	catalog := cloneCatalog(t, canonicalCatalog(t))
	catalog.Entities = append(catalog.Entities, catalog.Entities[0])
	catalog.Relationships[0].Downstream = catalog.Relationships[0].Upstream
	problems := validateCatalog(catalog, map[string]bool{})
	if !containsProblem(problems, "повторный id") || !containsProblem(problems, "self-loop") {
		t.Fatalf("expected duplicate and self-loop errors: %v", problems)
	}
}

func TestCapabilityCoverageIsValidated(t *testing.T) {
	catalog := cloneCatalog(t, canonicalCatalog(t))
	catalog.Contexts[0].Capabilities = nil
	problems := validateCatalog(catalog, map[string]bool{"domain-architecture": true})
	if !containsProblem(problems, `capability "domain-architecture"`) {
		t.Fatalf("expected missing capability error: %v", problems)
	}
}

func TestContextWithoutAggregatesIsRejected(t *testing.T) {
	catalog := cloneCatalog(t, canonicalCatalog(t))
	if len(catalog.Aggregates) == 0 {
		t.Fatal("canonical catalog declares no aggregates")
	}
	contextID := catalog.Aggregates[0].OwnerContext
	remaining := catalog.Aggregates[:0]
	for _, aggregate := range catalog.Aggregates {
		if aggregate.OwnerContext != contextID {
			remaining = append(remaining, aggregate)
		}
	}
	catalog.Aggregates = remaining
	problems := validateCatalog(catalog, map[string]bool{})
	if !containsProblem(problems, "contexts."+contextID+":") || !containsProblem(problems, "не объявлено ни одного агрегата") {
		t.Fatalf("expected missing aggregate error for context %q: %v", contextID, problems)
	}
}

func TestDiagramMarkersAreValidated(t *testing.T) {
	root := t.TempDir()
	catalog := canonicalCatalog(t)
	catalog.RequiredDiagrams = catalog.RequiredDiagrams[:1]
	problems := validateDiagrams(root, catalog)
	if !containsProblem(problems, "обязательная диаграмма отсутствует") {
		t.Fatalf("expected missing diagram: %v", problems)
	}

	diagram := catalog.RequiredDiagrams[0]
	path := filepath.Join(root, filepath.FromSlash(diagram.Path))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	markers := make([]string, 0, len(diagram.Contexts)+1)
	for _, contextID := range diagram.Contexts {
		markers = append(markers, "' CTX:"+contextID)
	}
	markers = append(markers, "' CTX:unknown-context")
	if err := os.WriteFile(path, []byte(strings.Join(markers, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	problems = validateDiagrams(root, catalog)
	if !containsProblem(problems, "неизвестный маркер") {
		t.Fatalf("expected unknown marker: %v", problems)
	}
}
