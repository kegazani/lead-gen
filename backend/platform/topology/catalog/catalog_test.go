package catalog

import "testing"

// TestReadRealCatalog проверяет разбор настоящего каталога: 12 сервисов и 62
// связи: прежние 51 и 11 research связей opt-in размещения.
func TestReadRealCatalog(t *testing.T) {
	document, err := Read("../../../../architecture/domain-catalog.yaml")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(document.Services) != 12 {
		t.Errorf("сервисов: %d, ожидалось 12", len(document.Services))
	}
	if len(document.Relationships) != 62 {
		t.Errorf("связей: %d, ожидалось 62", len(document.Relationships))
	}
	for _, service := range document.Services {
		if service.ID == "" || service.ContextID == "" {
			t.Errorf("сервис без id/context: %+v", service)
		}
		if len(service.Roles) == 0 {
			t.Errorf("сервис %q без ролей", service.ID)
		}
	}
}

// TestParseRejectsStructuralDefects проверяет, что структурные дефекты артефакта
// отвергаются, а не проходят молча.
func TestParseRejectsStructuralDefects(t *testing.T) {
	cases := map[string]string{
		"пустой каталог":       `{"contexts":[],"relationships":[]}`,
		"контекст без service": `{"contexts":[{"id":"a"}],"relationships":[]}`,
		"контекст без id":      `{"contexts":[{"service":"a"}],"relationships":[]}`,
		"повтор сервиса":       `{"contexts":[{"id":"a","service":"s"},{"id":"b","service":"s"}],"relationships":[]}`,
		"связь без id":         `{"contexts":[{"id":"a","service":"s"}],"relationships":[{"upstream":"a","downstream":"a"}]}`,
		"повтор связи":         `{"contexts":[{"id":"a","service":"s"}],"relationships":[{"id":"r"},{"id":"r"}]}`,
		"некорректный JSON":    `{`,
	}
	for name, content := range cases {
		if _, err := Parse([]byte(content)); err == nil {
			t.Errorf("%s: дефект не отвергнут", name)
		}
	}
}

// TestParseKeepsUnknownEndpoints проверяет, что ссылка на неизвестный контекст не
// роняет разбор: её разрешает построитель графа (fail-closed).
func TestParseKeepsUnknownEndpoints(t *testing.T) {
	document, err := Parse([]byte(
		`{"contexts":[{"id":"a","service":"a"}],` +
			`"relationships":[{"id":"r","upstream":"a","downstream":"ghost","integration":"event",` +
			`"contract":"c","transport":"async","mechanism":"amqp"}]}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(document.Relationships) != 1 || document.Relationships[0].DownstreamContext != "ghost" {
		t.Fatalf("связь с неизвестным концом потеряна: %+v", document.Relationships)
	}
}

// expectedResearchEdges фиксирует владельцев и опубликованные порты сборки
// deploy/research-client.json; произвольный in-process здесь не разрешается.
func expectedResearchEdges() map[string][6]string {
	return map[string][6]string{
		"research-mission-commands-for-requests":   {"agent-orchestration", "request-fulfillment", "command", "research-mission", "sync", "in-process"},
		"research-mission-status-for-requests":     {"agent-orchestration", "request-fulfillment", "query", "research-mission", "sync", "in-process"},
		"research-plans-for-requests":              {"agent-orchestration", "request-fulfillment", "command", "research-plan", "sync", "in-process"},
		"research-memory-writes-for-agents":        {"lead-management", "agent-orchestration", "command", "research-memory", "sync", "in-process"},
		"research-memory-reads-for-agents":         {"lead-management", "agent-orchestration", "query", "research-memory", "sync", "in-process"},
		"research-dossiers-for-requests":           {"lead-management", "request-fulfillment", "query", "research-memory", "sync", "in-process"},
		"research-acquisition-for-agents":          {"integration-platform", "agent-orchestration", "command", "research-acquisition", "sync", "in-process"},
		"research-acquisition-receipts-for-agents": {"integration-platform", "agent-orchestration", "query", "research-acquisition", "sync", "in-process"},
		"research-models-for-agents":               {"integration-platform", "agent-orchestration", "command", "provider-port", "sync", "in-process"},
		"research-identity-commands-for-requests":  {"lead-management", "request-fulfillment", "command", "research-memory", "sync", "in-process"},
		"research-models-for-memory":               {"integration-platform", "lead-management", "command", "research-extraction", "sync", "in-process"},
	}
}

func TestResearchRelationshipsKeepExactOwnerAndTransport(t *testing.T) {
	want := expectedResearchEdges()
	document, err := Read("../../../../architecture/domain-catalog.yaml")
	if err != nil {
		t.Fatal(err)
	}
	edges := document.Relationships
	for _, edge := range edges {
		expected, exists := want[edge.ID]
		if !exists {
			if edge.Mechanism == "in-process" {
				t.Errorf("неожиданная in-process связь %s", edge.ID)
			}
			continue
		}
		actual := [6]string{edge.UpstreamContext, edge.DownstreamContext, edge.Integration, edge.Contract, edge.Transport, edge.Mechanism}
		if actual != expected {
			t.Errorf("связь %s: %v, ожидалось %v", edge.ID, actual, expected)
		}
		delete(want, edge.ID)
	}
	if len(want) != 0 {
		t.Errorf("research связи отсутствуют: %v", want)
	}
}
