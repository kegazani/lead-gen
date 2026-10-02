package domain

import (
	"fmt"
	"strings"
	"time"
)

// Signal — наблюдаемый признак профиля лида (вход скоринга). Значение силы
// нормализовано в [0, 1]: это вклад признака, а не его сырое значение. Набор
// допустимых имён задаёт версия модели скоринга.
type Signal struct {
	name     string
	strength float64
}

// NewSignal собирает сигнал из имени и силы.
func NewSignal(name string, strength float64) (Signal, error) {
	s := Signal{name: strings.TrimSpace(name), strength: strength}
	if err := s.validate(); err != nil {
		return Signal{}, err
	}
	return s, nil
}

func (s Signal) validate() error {
	if s.name == "" {
		return fmt.Errorf("%w: пустое имя", ErrInvalidSignal)
	}
	if s.strength != s.strength || s.strength < 0 || s.strength > 1 {
		return fmt.Errorf("%w: сигнал %q силы %v", ErrInvalidSignal, s.name, s.strength)
	}
	return nil
}

// Name возвращает имя сигнала.
func (s Signal) Name() string { return s.name }

// Strength возвращает силу сигнала.
func (s Signal) Strength() float64 { return s.strength }

// LeadProfile — вход скоринга (потребительский DTO контракта lead-profile,
// владелец — lead-management). Аудитория хранит ссылку на лид и наблюдаемые
// признаки, но не редактирует лид: профиль приходит извне и здесь не порождается.
type LeadProfile struct {
	LeadID     LeadID
	TenantID   TenantID
	Signals    []Signal
	ObservedAt time.Time
}

// validate проверяет обязательные части профиля и уникальность имён сигналов.
func (p LeadProfile) validate() error {
	if err := p.LeadID.validate(); err != nil {
		return err
	}
	if err := p.TenantID.validate(); err != nil {
		return err
	}
	if p.ObservedAt.IsZero() {
		return ErrMissingTime
	}
	if len(p.Signals) == 0 {
		return ErrNoSignals
	}
	seen := make(map[string]struct{}, len(p.Signals))
	for _, signal := range p.Signals {
		if err := signal.validate(); err != nil {
			return err
		}
		if _, ok := seen[signal.name]; ok {
			return fmt.Errorf("%w: %q", ErrDuplicateSignal, signal.name)
		}
		seen[signal.name] = struct{}{}
	}
	return nil
}
