package catalog

import "testing"

// TestReadRealCatalog проверяет разбор настоящего каталога: 12 сервисов и 51
// объявленная связь (по каноническому каталогу с контекстом agent-orchestration и
// связью address-verification risk-screening).
func TestReadRealCatalog(t *testing.T) {
	document, err := Read("../../../../architecture/domain-catalog.yaml")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(document.Services) != 12 {
		t.Errorf("сервисов: %d, ожидалось 12", len(document.Services))
	}
	if len(document.Relationships) != 51 {
		t.Errorf("связей: %d, ожидалось 51", len(document.Relationships))
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
