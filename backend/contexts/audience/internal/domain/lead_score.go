package domain

import (
	"fmt"
	"sort"
	"time"
)

// Модели скоринга. Версия модели фиксирует словарь сигналов и их веса, поэтому
// одна и та же версия с одним и тем же профилем даёт одну и ту же оценку. Новый
// словарь или веса — новая версия, а не правка существующей: инвариант
// audience-segment требует воспроизводимости по версии модели.
var scoringModels = map[ModelVersion]map[string]float64{
	"v1": {
		"fit":          1.0,
		"intent":       0.8,
		"reachability": 0.5,
	},
}

// KnownModelVersion сообщает, объявлена ли версия модели скоринга доменом.
func KnownModelVersion(v ModelVersion) bool {
	_, ok := scoringModels[v]
	return ok
}

// ModelVersions возвращает объявленные версии моделей в порядке возрастания.
func ModelVersions() []ModelVersion {
	versions := make([]ModelVersion, 0, len(scoringModels))
	for v := range scoringModels {
		versions = append(versions, v)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })
	return versions
}

// scoreProfile вычисляет оценку профиля по версии модели. Оценка — взвешенное
// среднее сил сигналов; словарь версии должен покрывать все сигналы профиля,
// иначе воспроизвести оценку нельзя и возвращается ошибка.
func scoreProfile(profile LeadProfile, version ModelVersion) (ScoreValue, error) {
	weights, ok := scoringModels[version]
	if !ok {
		return ScoreValue{}, fmt.Errorf("%w: %q", ErrUnknownModelVersion, version)
	}
	if len(profile.Signals) == 0 {
		return ScoreValue{}, ErrNoSignals
	}

	var weighted, totalWeight float64
	for _, signal := range profile.Signals {
		weight, ok := weights[signal.name]
		if !ok {
			return ScoreValue{}, fmt.Errorf("%w: сигнал %q версии %q", ErrUnknownSignal, signal.name, version)
		}
		weighted += weight * signal.strength
		totalWeight += weight
	}
	if totalWeight == 0 {
		return ScoreValue{}, fmt.Errorf("%w: все веса версии %q нулевые", ErrUnknownModelVersion, version)
	}
	return NewScoreValue(weighted / totalWeight)
}

// LeadScore — сущность lead-score внутри агрегата audience-segment: оценка
// одного лида, зафиксированная вместе с версией модели, сигналами и временем.
type LeadScore struct {
	leadID       LeadID
	value        ScoreValue
	modelVersion ModelVersion
	signals      []Signal
	computedAt   time.Time
}

// newLeadScore собирает новую оценку, отвергая профиль без обязательных частей.
func newLeadScore(profile LeadProfile, version ModelVersion, value ScoreValue, at time.Time) (*LeadScore, error) {
	if err := profile.validate(); err != nil {
		return nil, err
	}
	if err := version.validate(); err != nil {
		return nil, err
	}
	if at.IsZero() {
		return nil, ErrMissingTime
	}
	return &LeadScore{
		leadID:       profile.LeadID,
		value:        value,
		modelVersion: version,
		signals:      append([]Signal(nil), profile.Signals...),
		computedAt:   at,
	}, nil
}

// LeadID возвращает идентификатор оценённого лида.
func (s *LeadScore) LeadID() LeadID { return s.leadID }

// Value возвращает оценку.
func (s *LeadScore) Value() ScoreValue { return s.value }

// ModelVersion возвращает версию модели, по которой получена оценка.
func (s *LeadScore) ModelVersion() ModelVersion { return s.modelVersion }

// Signals возвращает копию сигналов профиля.
func (s *LeadScore) Signals() []Signal { return append([]Signal(nil), s.signals...) }

// ComputedAt возвращает время вычисления оценки.
func (s *LeadScore) ComputedAt() time.Time { return s.computedAt }

// Profile восстанавливает профиль лида, по которому получена оценка. Нужен
// регулярному пересчёту: он повторяет сегментацию по уже сохранённым профилям.
func (s *LeadScore) Profile(tenant TenantID) LeadProfile {
	return LeadProfile{
		LeadID:     s.leadID,
		TenantID:   tenant,
		Signals:    append([]Signal(nil), s.signals...),
		ObservedAt: s.computedAt,
	}
}
