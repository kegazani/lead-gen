package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var contextMarker = regexp.MustCompile(`CTX:([a-z][a-z0-9]*(?:-[a-z0-9]+)*)`)

type Catalog struct {
	Version          int            `json:"version"`
	Contexts         []Context      `json:"contexts"`
	Entities         []Entity       `json:"entities"`
	Aggregates       []Aggregate    `json:"aggregates"`
	Relationships    []Relationship `json:"relationships"`
	RequiredDiagrams []Diagram      `json:"required_diagrams"`
}

type Context struct {
	ID           string   `json:"id"`
	Capabilities []string `json:"capabilities"`
	Contracts    []string `json:"contracts"`
}

type Entity struct {
	ID           string `json:"id"`
	OwnerContext string `json:"owner_context"`
}

type Aggregate struct {
	ID           string   `json:"id"`
	OwnerContext string   `json:"owner_context"`
	RootEntity   string   `json:"root_entity"`
	Entities     []string `json:"entities"`
	ValueObjects []string `json:"value_objects"`
	DomainEvents []string `json:"domain_events"`
	Policies     []string `json:"policies"`
}

type Relationship struct {
	ID          string `json:"id"`
	Upstream    string `json:"upstream"`
	Downstream  string `json:"downstream"`
	Integration string `json:"integration"`
	Contract    string `json:"contract"`
}

type Diagram struct {
	ID       string   `json:"id"`
	Path     string   `json:"path"`
	Contexts []string `json:"contexts"`
}

func loadJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func validateSchema(value any, schema, root map[string]any, path string) []string {
	if reference, ok := schema["$ref"].(string); ok {
		resolved, err := resolveReference(root, reference)
		if err != nil {
			return []string{fmt.Sprintf("%s: %v", path, err)}
		}
		return validateSchema(value, resolved, root, path)
	}

	var problems []string
	if expected, ok := schema["type"].(string); ok && !typeMatches(value, expected) {
		return []string{fmt.Sprintf("%s: ожидался тип %s", path, expected)}
	}
	if enum, ok := schema["enum"].([]any); ok && !containsJSON(enum, value) {
		problems = append(problems, fmt.Sprintf("%s: значение не входит в enum", path))
	}

	switch typed := value.(type) {
	case string:
		if minimum, ok := number(schema["minLength"]); ok && len([]rune(typed)) < int(minimum) {
			problems = append(problems, fmt.Sprintf("%s: строка короче minLength", path))
		}
		if pattern, ok := schema["pattern"].(string); ok {
			re, err := regexp.Compile(pattern)
			if err != nil || !re.MatchString(typed) {
				problems = append(problems, fmt.Sprintf("%s: значение не соответствует pattern", path))
			}
		}
	case float64:
		if minimum, ok := number(schema["minimum"]); ok && typed < minimum {
			problems = append(problems, fmt.Sprintf("%s: значение меньше minimum", path))
		}
	case []any:
		if minimum, ok := number(schema["minItems"]); ok && len(typed) < int(minimum) {
			problems = append(problems, fmt.Sprintf("%s: элементов меньше minItems", path))
		}
		if maximum, ok := number(schema["maxItems"]); ok && len(typed) > int(maximum) {
			problems = append(problems, fmt.Sprintf("%s: элементов больше maxItems", path))
		}
		if unique, _ := schema["uniqueItems"].(bool); unique && hasDuplicateJSON(typed) {
			problems = append(problems, fmt.Sprintf("%s: элементы должны быть уникальны", path))
		}
		if itemSchema, ok := schema["items"].(map[string]any); ok {
			for index, item := range typed {
				problems = append(problems, validateSchema(item, itemSchema, root, fmt.Sprintf("%s[%d]", path, index))...)
			}
		}
	case map[string]any:
		properties, _ := schema["properties"].(map[string]any)
		if required, ok := schema["required"].([]any); ok {
			for _, item := range required {
				key, _ := item.(string)
				if _, found := typed[key]; !found {
					problems = append(problems, fmt.Sprintf("%s: отсутствует обязательное поле %q", path, key))
				}
			}
		}
		if additional, ok := schema["additionalProperties"].(bool); ok && !additional {
			for key := range typed {
				if _, found := properties[key]; !found {
					problems = append(problems, fmt.Sprintf("%s.%s: неизвестное поле", path, key))
				}
			}
		}
		for key, child := range properties {
			childSchema, ok := child.(map[string]any)
			childValue, found := typed[key]
			if ok && found {
				problems = append(problems, validateSchema(childValue, childSchema, root, path+"."+key)...)
			}
		}
	}
	return problems
}

func resolveReference(root map[string]any, reference string) (map[string]any, error) {
	if !strings.HasPrefix(reference, "#/") {
		return nil, fmt.Errorf("неподдерживаемая ссылка схемы %q", reference)
	}
	var node any = root
	for _, raw := range strings.Split(strings.TrimPrefix(reference, "#/"), "/") {
		part := strings.ReplaceAll(strings.ReplaceAll(raw, "~1", "/"), "~0", "~")
		object, ok := node.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("некорректная ссылка схемы %q", reference)
		}
		node, ok = object[part]
		if !ok {
			return nil, fmt.Errorf("неизвестная ссылка схемы %q", reference)
		}
	}
	resolved, ok := node.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("ссылка схемы %q не указывает на объект", reference)
	}
	return resolved, nil
}

func typeMatches(value any, expected string) bool {
	switch expected {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "integer":
		n, ok := value.(float64)
		return ok && math.Trunc(n) == n
	case "boolean":
		_, ok := value.(bool)
		return ok
	default:
		return false
	}
}

func number(value any) (float64, bool) {
	n, ok := value.(float64)
	return n, ok
}

func containsJSON(values []any, target any) bool {
	wanted, _ := json.Marshal(target)
	for _, value := range values {
		encoded, _ := json.Marshal(value)
		if string(encoded) == string(wanted) {
			return true
		}
	}
	return false
}

func hasDuplicateJSON(values []any) bool {
	seen := make(map[string]bool)
	for _, value := range values {
		encoded, _ := json.Marshal(value)
		key := string(encoded)
		if seen[key] {
			return true
		}
		seen[key] = true
	}
	return false
}

func discoverCapabilities(root string) (map[string]bool, error) {
	capabilities := make(map[string]bool)
	roots := []string{filepath.Join(root, "openspec", "specs")}
	changeRoots, err := filepath.Glob(filepath.Join(root, "openspec", "changes", "*", "specs"))
	if err != nil {
		return nil, err
	}
	roots = append(roots, changeRoots...)
	for _, specsRoot := range roots {
		if _, err := os.Stat(specsRoot); errors.Is(err, os.ErrNotExist) {
			continue
		}
		err := filepath.WalkDir(specsRoot, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || entry.Name() != "spec.md" {
				return nil
			}
			relative, err := filepath.Rel(specsRoot, filepath.Dir(path))
			if err != nil {
				return err
			}
			capabilities[filepath.ToSlash(relative)] = true
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return capabilities, nil
}

func validateCatalog(catalog Catalog, capabilities map[string]bool) []string {
	var problems []string
	contextIDs := make(map[string]bool)
	contracts := make(map[string]map[string]bool)
	capabilityOwners := make(map[string][]string)
	for _, context := range catalog.Contexts {
		if contextIDs[context.ID] {
			problems = append(problems, fmt.Sprintf("contexts: повторный id %q", context.ID))
		}
		contextIDs[context.ID] = true
		contracts[context.ID] = stringSet(context.Contracts)
		for _, capability := range context.Capabilities {
			capabilityOwners[capability] = append(capabilityOwners[capability], context.ID)
		}
	}

	entityOwners := make(map[string]string)
	for _, entity := range catalog.Entities {
		if _, exists := entityOwners[entity.ID]; exists {
			problems = append(problems, fmt.Sprintf("entities: повторный id %q; у сущности должен быть один владелец", entity.ID))
		}
		entityOwners[entity.ID] = entity.OwnerContext
		if !contextIDs[entity.OwnerContext] {
			problems = append(problems, fmt.Sprintf("entities.%s: неизвестный owner_context %q", entity.ID, entity.OwnerContext))
		}
	}

	aggregateIDs := make(map[string]bool)
	contextAggregates := make(map[string]int)
	entityAggregates := make(map[string][]string)
	dddIDs := map[string]map[string]string{
		"value_objects": {}, "domain_events": {}, "policies": {},
	}
	for _, aggregate := range catalog.Aggregates {
		if aggregateIDs[aggregate.ID] {
			problems = append(problems, fmt.Sprintf("aggregates: повторный id %q", aggregate.ID))
		}
		aggregateIDs[aggregate.ID] = true
		contextAggregates[aggregate.OwnerContext]++
		if !contextIDs[aggregate.OwnerContext] {
			problems = append(problems, fmt.Sprintf("aggregates.%s: неизвестный owner_context %q", aggregate.ID, aggregate.OwnerContext))
		}
		rootOwner, rootKnown := entityOwners[aggregate.RootEntity]
		if !rootKnown {
			problems = append(problems, fmt.Sprintf("aggregates.%s: неизвестный root_entity %q", aggregate.ID, aggregate.RootEntity))
		} else if rootOwner != aggregate.OwnerContext {
			problems = append(problems, fmt.Sprintf("aggregates.%s: root_entity %q принадлежит context %q", aggregate.ID, aggregate.RootEntity, rootOwner))
		}
		members := stringSet(aggregate.Entities)
		if !members[aggregate.RootEntity] {
			problems = append(problems, fmt.Sprintf("aggregates.%s: root_entity %q отсутствует в entities", aggregate.ID, aggregate.RootEntity))
		}
		for _, entityID := range aggregate.Entities {
			owner, known := entityOwners[entityID]
			if !known {
				problems = append(problems, fmt.Sprintf("aggregates.%s: неизвестная entity %q", aggregate.ID, entityID))
			} else if owner != aggregate.OwnerContext {
				problems = append(problems, fmt.Sprintf("aggregates.%s: entity %q принадлежит context %q", aggregate.ID, entityID, owner))
			}
			entityAggregates[entityID] = append(entityAggregates[entityID], aggregate.ID)
		}
		registerDDD := func(kind string, values []string) {
			for _, id := range values {
				if previous, exists := dddIDs[kind][id]; exists {
					problems = append(problems, fmt.Sprintf("aggregates.%s.%s: id %q уже объявлен в aggregate %q", aggregate.ID, kind, id, previous))
				} else {
					dddIDs[kind][id] = aggregate.ID
				}
			}
		}
		registerDDD("value_objects", aggregate.ValueObjects)
		registerDDD("domain_events", aggregate.DomainEvents)
		registerDDD("policies", aggregate.Policies)
	}
	for entityID := range entityOwners {
		owners := entityAggregates[entityID]
		if len(owners) != 1 {
			problems = append(problems, fmt.Sprintf("entity %q: ожидался один aggregate, найдено %v", entityID, owners))
		}
	}
	for _, context := range catalog.Contexts {
		if contextAggregates[context.ID] == 0 {
			problems = append(problems, fmt.Sprintf("contexts.%s: не объявлено ни одного агрегата", context.ID))
		}
	}

	for capability := range capabilities {
		owners := capabilityOwners[capability]
		if len(owners) != 1 {
			problems = append(problems, fmt.Sprintf("capability %q: ожидался один primary context, найдено %v", capability, owners))
		}
	}
	for capability, owners := range capabilityOwners {
		if !capabilities[capability] {
			problems = append(problems, fmt.Sprintf("catalog: capability %q отсутствует в OpenSpec", capability))
		}
		if len(owners) > 1 {
			problems = append(problems, fmt.Sprintf("capability %q: несколько primary contexts %v", capability, owners))
		}
	}

	relationshipIDs := make(map[string]bool)
	relationshipKeys := make(map[string]bool)
	for _, relationship := range catalog.Relationships {
		if relationshipIDs[relationship.ID] {
			problems = append(problems, fmt.Sprintf("relationships: повторный id %q", relationship.ID))
		}
		relationshipIDs[relationship.ID] = true
		if !contextIDs[relationship.Upstream] {
			problems = append(problems, fmt.Sprintf("relationships.%s: неизвестный upstream %q", relationship.ID, relationship.Upstream))
		}
		if !contextIDs[relationship.Downstream] {
			problems = append(problems, fmt.Sprintf("relationships.%s: неизвестный downstream %q", relationship.ID, relationship.Downstream))
		}
		if relationship.Upstream == relationship.Downstream {
			problems = append(problems, fmt.Sprintf("relationships.%s: self-loop запрещён", relationship.ID))
		}
		if upstreamContracts, known := contracts[relationship.Upstream]; known && !upstreamContracts[relationship.Contract] {
			problems = append(problems, fmt.Sprintf("relationships.%s: контракт %q не объявлен upstream %q", relationship.ID, relationship.Contract, relationship.Upstream))
		}
		key := strings.Join([]string{relationship.Upstream, relationship.Downstream, relationship.Contract, relationship.Integration}, "\x00")
		if relationshipKeys[key] {
			problems = append(problems, fmt.Sprintf("relationships: повторная связь %q", relationship.ID))
		}
		relationshipKeys[key] = true
	}

	diagramIDs := make(map[string]bool)
	for _, diagram := range catalog.RequiredDiagrams {
		if diagramIDs[diagram.ID] {
			problems = append(problems, fmt.Sprintf("required_diagrams: повторный id %q", diagram.ID))
		}
		diagramIDs[diagram.ID] = true
		for _, contextID := range diagram.Contexts {
			if !contextIDs[contextID] {
				problems = append(problems, fmt.Sprintf("required_diagrams.%s: неизвестный context %q", diagram.ID, contextID))
			}
		}
	}
	sort.Strings(problems)
	return problems
}

func stringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}

func validateDiagrams(root string, catalog Catalog) []string {
	var problems []string
	knownContexts := make(map[string]bool)
	for _, context := range catalog.Contexts {
		knownContexts[context.ID] = true
	}
	for _, diagram := range catalog.RequiredDiagrams {
		path := filepath.Join(root, filepath.FromSlash(diagram.Path))
		data, err := os.ReadFile(path)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: обязательная диаграмма отсутствует", diagram.Path))
			continue
		}
		markers := make(map[string]bool)
		for _, match := range contextMarker.FindAllStringSubmatch(string(data), -1) {
			markers[match[1]] = true
		}
		for _, contextID := range diagram.Contexts {
			if !markers[contextID] {
				problems = append(problems, fmt.Sprintf("%s: отсутствует маркер CTX:%s", diagram.Path, contextID))
			}
		}
		for contextID := range markers {
			if !knownContexts[contextID] {
				problems = append(problems, fmt.Sprintf("%s: неизвестный маркер CTX:%s", diagram.Path, contextID))
			}
		}
	}
	sort.Strings(problems)
	return problems
}

func run(root, catalogPath, schemaPath string) (Catalog, []string) {
	var rawCatalog any
	var schema map[string]any
	if err := loadJSON(catalogPath, &rawCatalog); err != nil {
		return Catalog{}, []string{err.Error()}
	}
	if err := loadJSON(schemaPath, &schema); err != nil {
		return Catalog{}, []string{err.Error()}
	}
	problems := validateSchema(rawCatalog, schema, schema, "$")
	var catalog Catalog
	data, _ := json.Marshal(rawCatalog)
	if err := json.Unmarshal(data, &catalog); err != nil {
		problems = append(problems, err.Error())
		return catalog, problems
	}
	capabilities, err := discoverCapabilities(root)
	if err != nil {
		problems = append(problems, err.Error())
	} else {
		problems = append(problems, validateCatalog(catalog, capabilities)...)
	}
	problems = append(problems, validateDiagrams(root, catalog)...)
	return catalog, problems
}

func defaultRoot() string {
	workingDirectory, err := os.Getwd()
	if err != nil {
		return "."
	}
	if _, err := os.Stat(filepath.Join(workingDirectory, "architecture")); err == nil {
		return workingDirectory
	}
	return filepath.Clean(filepath.Join(workingDirectory, "..", ".."))
}

func main() {
	rootFlag := flag.String("root", defaultRoot(), "корень репозитория")
	catalogFlag := flag.String("catalog", "", "путь к каталогу")
	schemaFlag := flag.String("schema", "", "путь к JSON Schema")
	flag.Parse()

	root, err := filepath.Abs(*rootFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "archlint:", err)
		os.Exit(1)
	}
	catalogPath := *catalogFlag
	if catalogPath == "" {
		catalogPath = filepath.Join(root, "architecture", "domain-catalog.yaml")
	}
	schemaPath := *schemaFlag
	if schemaPath == "" {
		schemaPath = filepath.Join(root, "architecture", "domain-catalog.schema.json")
	}
	catalog, problems := run(root, catalogPath, schemaPath)
	if len(problems) > 0 {
		fmt.Fprintf(os.Stderr, "archlint: найдено ошибок: %d\n", len(problems))
		for _, problem := range problems {
			fmt.Fprintln(os.Stderr, "-", problem)
		}
		os.Exit(1)
	}
	capabilities, _ := discoverCapabilities(root)
	fmt.Printf("archlint: OK — контекстов: %d, агрегатов: %d, сущностей: %d, capability: %d, связей: %d\n",
		len(catalog.Contexts), len(catalog.Aggregates), len(catalog.Entities), len(capabilities), len(catalog.Relationships))
}
