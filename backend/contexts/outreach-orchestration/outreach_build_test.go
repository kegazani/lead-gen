package outreachorchestration_test

import (
	"context"
	"strings"
	"testing"

	composition "lead-gen/backend/contexts/outreach-orchestration"
	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
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

// noRiskFacts — тестовый двойник внешнего предиката риска: черновики без
// признаков. Это осознанный двойник теста, а не заглушка продукта: на продуктовом
// пути предикат собирает NewRiskFacts поверх портов владельцев.
func noRiskFacts(context.Context, domain.RiskFactsRequest) ([]domain.RiskReason, error) {
	return nil, nil
}

// TestBuildAcceptsExplicitRiskFacts — явно переданный предикат признаний риска
// принимается: вызывающий задаёт его осознанно, а не получает скрытый дефолт.
func TestBuildAcceptsExplicitRiskFacts(t *testing.T) {
	slice, err := composition.Build(composition.Params{Risk: noRiskFacts})
	if err != nil {
		t.Fatalf("сборка с явным предикатом риска: %v", err)
	}
	if slice == nil {
		t.Fatal("сборка с явным предикатом риска вернула nil-срез")
	}
}
