package outreachorchestration_test

import (
	"strings"
	"testing"

	composition "lead-gen/backend/contexts/outreach-orchestration"
)

// TestBuildRequiresRiskPredicate — защита от повторения блокера: незаданный
// предикат риска раньше молча означал «рисков нет», из-за чего исключение
// рисковых черновиков в процессах api и worker было отключено. Build обязан
// отвергать такой вызов, а вызывающий — выбрать предикат явно.
func TestBuildRequiresRiskPredicate(t *testing.T) {
	slice, err := composition.Build(composition.Params{})
	if err == nil {
		t.Fatal("Build с незаданным Params.Risk вернул nil-ошибку; ожидался отказ")
	}
	if slice != nil {
		t.Fatalf("Build с незаданным Params.Risk вернул срез не nil: %+v", slice)
	}
	if !strings.Contains(err.Error(), "Params.Risk") {
		t.Fatalf("ошибка не называет незаданный параметр: %v", err)
	}
}

// TestBuildAcceptsExplicitRiskStub — явно переданная заглушка принимается: она
// сохраняет текущее поведение «рисков нет», но теперь это осознанный выбор
// вызывающего, а не скрытый дефолт.
func TestBuildAcceptsExplicitRiskStub(t *testing.T) {
	slice, err := composition.Build(composition.Params{Risk: composition.NoUpstreamRiskChecks})
	if err != nil {
		t.Fatalf("сборка с явной заглушкой риска: %v", err)
	}
	if slice == nil {
		t.Fatal("сборка с явной заглушкой риска вернула nil-срез")
	}
}
