package contracts

import (
	"testing"
	"time"

	"lead-gen/backend/contexts/integration-platform/internal/domain"
	"lead-gen/contracts/integration-platform/adaptercapability"
	"lead-gen/contracts/integration-platform/integrationerror"
	"lead-gen/contracts/integration-platform/providerport"
)

var testTime = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

// Набор категорий контракта совпадает с закрытым набором домена: сверка не
// потеряет ни одну категорию и не примет лишнюю.
func TestErrorCategoriesMatchDomain(t *testing.T) {
	domainCategories := domain.AllErrorCategories()
	contractCategories := AllCategories()
	if len(domainCategories) != len(contractCategories) {
		t.Fatalf("число категорий: домен %d, контракт %d", len(domainCategories), len(contractCategories))
	}
	for _, category := range contractCategories {
		parsed, err := ParseErrorCategory(category)
		if err != nil {
			t.Fatalf("категория контракта %q не принята доменом: %v", category, err)
		}
		if !domain.IsValidErrorCategory(parsed) {
			t.Fatalf("категория %q не входит в доменный набор", parsed)
		}
	}
}

// Сверка отвергает значение контракта, недопустимое для домена.
func TestParseErrorCategoryRejectsUnknown(t *testing.T) {
	if _, err := ParseErrorCategory(integrationerror.Category("nope")); err == nil {
		t.Fatal("неизвестная категория должна отвергаться")
	}
}

// Имя возможности контракта сверяется с доменным VO.
func TestParseCapabilityName(t *testing.T) {
	known, err := ParseCapabilityName(adaptercapability.Capability{Name: "search"})
	if err != nil {
		t.Fatalf("известная возможность: %v", err)
	}
	if known != domain.CapabilitySearch {
		t.Fatalf("получено %q", known)
	}
	if _, err := ParseCapabilityName(adaptercapability.Capability{Name: "nope"}); err == nil {
		t.Fatal("неизвестная возможность должна отвергаться")
	}
}

// Тип интеграции и операция контракта сверяются с доменными VO.
func TestParseProviderFields(t *testing.T) {
	integrationType, err := ParseIntegrationType(providerport.IntegrationType("web-research"))
	if err != nil {
		t.Fatalf("тип интеграции: %v", err)
	}
	if integrationType != domain.IntegrationType("web-research") {
		t.Fatalf("тип: %q", integrationType)
	}
	if _, err := ParseIntegrationType(providerport.IntegrationType("  ")); err == nil {
		t.Fatal("пустой тип интеграции должен отвергаться")
	}
	if _, err := ParseOperation(providerport.Operation("")); err == nil {
		t.Fatal("пустая операция должна отвергаться")
	}
}

// Имена и версии схем контрактов реэкспортируются без расхождений.
func TestContractConstants(t *testing.T) {
	if ContractProviderPort != providerport.ContractName || ProviderPortSchemaVersion != providerport.SchemaVersion {
		t.Fatal("константы provider-port разошлись с публикуемым модулем")
	}
	if ContractAdapterCapability != adaptercapability.ContractName {
		t.Fatal("имя контракта adapter-capability разошлось")
	}
}

// NewProviderEnvelope проставляет имя и версию схемы контракта.
func TestNewProviderEnvelope(t *testing.T) {
	env := NewProviderEnvelope(providerport.Envelope{})
	if env.Name != ContractProviderPort || env.SchemaVersion != ProviderPortSchemaVersion {
		t.Fatalf("имя/версия: got (%q, %q)", env.Name, env.SchemaVersion)
	}
}

// ValidateDeclaration сверяет возможности объявления с доменным набором.
func TestValidateDeclaration(t *testing.T) {
	env := adaptercapability.NewDeclaration(adaptercapability.Envelope{
		EventID:       "evt-1",
		OccurredAt:    testTime,
		TenantID:      "tenant-1",
		CorrelationID: "corr-1",
	})
	valid := env
	valid.AdapterName = "firecrawl"
	valid.Capabilities = []adaptercapability.Capability{{Name: "search", SetVersion: "1.0.0"}}
	if err := ValidateDeclaration(valid); err != nil {
		t.Fatalf("валидное объявление: %v", err)
	}

	invalid := valid
	invalid.Capabilities = []adaptercapability.Capability{{Name: "nope", SetVersion: "1.0.0"}}
	if err := ValidateDeclaration(invalid); err == nil {
		t.Fatal("неизвестная возможность должна отвергаться")
	}
}
