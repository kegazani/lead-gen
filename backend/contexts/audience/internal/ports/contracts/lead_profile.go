package contracts

import (
	"fmt"
	"strings"
	"time"

	"lead-gen/backend/contexts/audience/internal/domain"
)

// ContractLeadProfile — имя потребляемого контракта-запроса: профиль лида,
// владельцем которого является контекст lead-management (каталог связей, связь
// lead-to-audience, интеграция query, транспорт sync/http-json).
const ContractLeadProfile = "lead-profile"

// SignalValue — наблюдаемый признак лида в форме контракта: имя и нормализованная
// сила в [0, 1]. Словарь имён задаёт версия модели скоринга владельца audience.
type SignalValue struct {
	Name     string  `json:"name"`
	Strength float64 `json:"strength"`
}

// LeadProfile — антикоррупционный DTO потребителя для контракта lead-profile.
//
// Аудитория хранит ссылку на лид и наблюдаемые признаки, а не сам лид: сюда
// попадают только идентификаторы и сигналы, необходимые для скоринга и
// сегментации. Персональные данные лида через границу не проходят.
type LeadProfile struct {
	// LeadID — идентификатор лида (владелец — lead-management).
	LeadID string `json:"lead_id"`
	// TenantID — тенант-владелец лида; обязан совпасть с тенантом сегмента.
	TenantID string `json:"tenant_id"`
	// ObservedAt — момент наблюдения признаков.
	ObservedAt time.Time `json:"observed_at"`
	// Signals — наблюдаемые признаки лида. Пустой набор оценивать нельзя.
	Signals []SignalValue `json:"signals"`
}

// Validate проверяет обязательные поля контракта, собирая все отсутствующие или
// некорректные, а не первое из них.
func (p LeadProfile) Validate() error {
	var invalid []string
	if strings.TrimSpace(p.LeadID) == "" {
		invalid = append(invalid, "lead_id")
	}
	if strings.TrimSpace(p.TenantID) == "" {
		invalid = append(invalid, "tenant_id")
	}
	if p.ObservedAt.IsZero() {
		invalid = append(invalid, "observed_at")
	}
	if len(p.Signals) == 0 {
		invalid = append(invalid, "signals")
	}
	for _, signal := range p.Signals {
		if strings.TrimSpace(signal.Name) == "" {
			invalid = append(invalid, "signals.name")
			break
		}
	}
	if len(invalid) > 0 {
		return fmt.Errorf("contracts: lead-profile: некорректные или отсутствующие обязательные поля: %s", strings.Join(invalid, ", "))
	}
	return nil
}

// Domain переводит DTO в доменный профиль лида, проверяя форму и сигналы.
func (p LeadProfile) Domain() (domain.LeadProfile, error) {
	if err := p.Validate(); err != nil {
		return domain.LeadProfile{}, err
	}
	signals := make([]domain.Signal, 0, len(p.Signals))
	for _, signal := range p.Signals {
		built, err := domain.NewSignal(signal.Name, signal.Strength)
		if err != nil {
			return domain.LeadProfile{}, err
		}
		signals = append(signals, built)
	}
	return domain.LeadProfile{
		LeadID:     domain.LeadID(p.LeadID),
		TenantID:   domain.TenantID(p.TenantID),
		Signals:    signals,
		ObservedAt: p.ObservedAt,
	}, nil
}
