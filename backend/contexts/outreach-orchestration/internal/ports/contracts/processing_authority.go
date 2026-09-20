package contracts

import (
	"fmt"
	"strings"
	"time"
)

// ContractProcessingAuthority — имя потребляемого контракта-политики: снимок
// полномочия на обработку данных, владельцем которого является контекст
// data-governance (каталог связей, связь governance-policy-for-outreach,
// интеграция policy, транспорт async/amqp).
const ContractProcessingAuthority = "processing-authority"

// ProcessingAuthoritySchemaVersion — текущая версия схемы. Изменять по правилам
// semver из envelope.go.
const ProcessingAuthoritySchemaVersion = "1.0.0"

// ProcessingAuthority — антикоррупционный DTO ПОТРЕБИТЕЛЯ для контракта
// processing-authority.
//
// Policy доставляется подпиской, а не вызовом: потребитель хранит контрактный
// снимок и применяет его локально, не обращаясь к data-governance на каждое
// решение (design.md, Решение 3). Владелец контракта — data-governance; его типы
// сюда не импортируются. Через границу проходит только контрактный снимок
// правила: идентификатор политики, область действия, разрешённые зоны обработки,
// основание и момент вступления в силу.
type ProcessingAuthority struct {
	Envelope

	// PolicyID — идентификатор версии политики.
	PolicyID string `json:"policy_id"`
	// Scope — область действия правила (например, tenant или контекст).
	Scope string `json:"scope"`
	// AllowedZones — зоны обработки, разрешённые политикой. Пустой список
	// отвергается: политика без разрешённых зон не является решением.
	AllowedZones []string `json:"allowed_zones"`
	// LegalBasis — основание обработки. Обязательно: разрешение без основания
	// недопустимо.
	LegalBasis string `json:"legal_basis"`
	// EffectiveAt — момент вступления правила в силу.
	EffectiveAt time.Time `json:"effective_at"`
}

// Validate проверяет envelope и обязательные поля контракта, собирая все
// отсутствующие или некорректные поля, а не первое из них.
func (p ProcessingAuthority) Validate() error {
	if err := p.Envelope.Validate(); err != nil {
		return err
	}
	var invalid []string
	if p.Name != ContractProcessingAuthority {
		invalid = append(invalid, "name")
	}
	if strings.TrimSpace(p.PolicyID) == "" {
		invalid = append(invalid, "policy_id")
	}
	if strings.TrimSpace(p.Scope) == "" {
		invalid = append(invalid, "scope")
	}
	zones := 0
	for _, zone := range p.AllowedZones {
		if strings.TrimSpace(zone) != "" {
			zones++
		}
	}
	if zones == 0 {
		invalid = append(invalid, "allowed_zones")
	}
	if strings.TrimSpace(p.LegalBasis) == "" {
		invalid = append(invalid, "legal_basis")
	}
	if p.EffectiveAt.IsZero() {
		invalid = append(invalid, "effective_at")
	}
	if len(invalid) > 0 {
		return fmt.Errorf("contracts: processing-authority: некорректные или отсутствующие обязательные поля: %s", strings.Join(invalid, ", "))
	}
	return nil
}
