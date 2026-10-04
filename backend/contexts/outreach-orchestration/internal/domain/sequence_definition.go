package domain

import (
	"fmt"
	"strings"
)

// SequenceDefinition — сущность агрегата campaign: сценарий последовательности
// касаний. Состоит из упорядоченных неизменяемых шагов; порядок шагов задаёт
// ход цепочки (architecture/domain-catalog.yaml, aggregate campaign,
// entity sequence-definition).
type SequenceDefinition struct {
	id       SequenceID
	campaign CampaignID
	tenant   TenantID
	name     string
	steps    []SequenceStep
}

// SequenceDefinitionParams — данные сценария.
type SequenceDefinitionParams struct {
	ID       SequenceID
	Campaign CampaignID
	Tenant   TenantID
	Name     string
	Steps    []SequenceStep
}

// NewSequenceDefinition создаёт проверенный сценарий. Сценарий без шагов
// отвергается: пустой сценарий не ведёт цепочку.
func NewSequenceDefinition(params SequenceDefinitionParams) (*SequenceDefinition, error) {
	if err := params.ID.validate(); err != nil {
		return nil, err
	}
	if err := params.Campaign.validate(); err != nil {
		return nil, err
	}
	if err := params.Tenant.validate(); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return nil, fmt.Errorf("%w: сценарий %s", ErrEmptySequenceName, params.ID)
	}
	if len(params.Steps) == 0 {
		return nil, fmt.Errorf("%w: сценарий %s", ErrSequenceStepsEmpty, params.ID)
	}
	steps := make([]SequenceStep, len(params.Steps))
	copy(steps, params.Steps)
	return &SequenceDefinition{
		id:       params.ID,
		campaign: params.Campaign,
		tenant:   params.Tenant,
		name:     name,
		steps:    steps,
	}, nil
}

// ID возвращает идентификатор сценария.
func (d *SequenceDefinition) ID() SequenceID { return d.id }

// CampaignID возвращает кампанию-владельца сценария.
func (d *SequenceDefinition) CampaignID() CampaignID { return d.campaign }

// TenantID возвращает тенанта-владельца сценария.
func (d *SequenceDefinition) TenantID() TenantID { return d.tenant }

// Name возвращает название сценария.
func (d *SequenceDefinition) Name() string { return d.name }

// Steps возвращает копию шагов сценария в порядке исполнения.
func (d *SequenceDefinition) Steps() []SequenceStep {
	out := make([]SequenceStep, len(d.steps))
	copy(out, d.steps)
	return out
}

// Step возвращает шаг по позиции и признак его наличия.
func (d *SequenceDefinition) Step(index int) (SequenceStep, bool) {
	if index < 0 || index >= len(d.steps) {
		return SequenceStep{}, false
	}
	return d.steps[index], true
}

// Len возвращает число шагов сценария.
func (d *SequenceDefinition) Len() int { return len(d.steps) }
