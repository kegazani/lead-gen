package fake

import (
	"testing"

	"lead-gen/backend/contexts/integration-platform/internal/ports"
	"lead-gen/backend/contexts/integration-platform/internal/ports/contracttest"
	"lead-gen/contracts/integration-platform/integrationerror"
)

// harness адаптирует тестовый адаптер к управляющей поверхности contract suite.
// Сам пакет fake не импортирует contracttest: перевод формы исхода живёт в тесте.
type harness struct {
	*Provider
}

// Apply переводит сценарий suite в настройки адаптера.
func (h harness) Apply(scenario contracttest.Scenario) {
	h.Configure(Scenario{
		Kind:       OutcomeKind(scenario.Outcome),
		Category:   integrationerror.Category(scenario.Category),
		ExternalID: scenario.ExternalID,
		Payload:    scenario.Payload,
		Reason:     scenario.Reason,
		Delay:      scenario.Delay,
	})
}

// Port возвращает адаптер как интерфейс контракта.
func (h harness) Port() ports.ProviderPort { return h.Provider }

// Общий contract suite порта проходит против тестового адаптера.
func TestProviderContract(t *testing.T) {
	contracttest.RunProviderContract(t, func(t *testing.T) contracttest.ProviderHarness {
		t.Helper()
		return harness{New(Config{
			Secrets: []string{"sk-live-AAA", "tok-777", "https://api.internal.example"},
		})}
	})
}
