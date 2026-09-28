package domain

import (
	"fmt"
	"time"
)

// Stage — стадия воронки лида. Согласованный набор (capability lead-workflow):
// not warmed, email sent, warming, cold. Значение вне набора не применяется:
// это инвариант каталога, а не подсказка интерфейсу.
type Stage string

// Согласованные стадии воронки лида.
const (
	StageNotWarmed Stage = "not warmed"
	StageEmailSent Stage = "email sent"
	StageWarming   Stage = "warming"
	StageCold      Stage = "cold"
)

// valid сообщает, известна ли стадия.
func (s Stage) valid() bool {
	switch s {
	case StageNotWarmed, StageEmailSent, StageWarming, StageCold:
		return true
	default:
		return false
	}
}

// String возвращает стадию строкой.
func (s Stage) String() string { return string(s) }

// LeadStatus — value object статуса лида: стадия воронки и срок snooze, после
// которого лид возвращается в активную воронку. SnoozeUntil == nil означает
// «лид не отложен». Value object неизменяем: изменение идёт методами агрегата
// Company, а не правкой полей снаружи.
type LeadStatus struct {
	// Stage — текущая стадия воронки.
	Stage Stage
	// SnoozeUntil — момент, до которого лид скрыт из активной воронки.
	SnoozeUntil *time.Time
}

// NewLeadStatus создаёт статус лида. Стадия обязана принадлежать согласованному
// набору; snoozeUntil может быть nil («не отложен»).
func NewLeadStatus(stage Stage, snoozeUntil *time.Time) (*LeadStatus, error) {
	status := LeadStatus{Stage: stage, SnoozeUntil: cloneTimePtr(snoozeUntil)}
	if err := status.validate(); err != nil {
		return nil, err
	}
	return &status, nil
}

// validate проверяет согласованность статуса.
func (s LeadStatus) validate() error {
	if !s.Stage.valid() {
		return fmt.Errorf("%w: %q", ErrInvalidStage, s.Stage)
	}
	return nil
}

// Snapshot возвращает состояние value object для сохранения.
func (s LeadStatus) Snapshot() LeadStatus {
	return LeadStatus{Stage: s.Stage, SnoozeUntil: cloneTimePtr(s.SnoozeUntil)}
}

// RehydrateLeadStatus восстанавливает value object из сохранённого состояния,
// отвергая противоречивый снимок (стадия вне набора).
func RehydrateLeadStatus(s LeadStatus) (*LeadStatus, error) {
	if err := s.validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSnapshot, err)
	}
	return &LeadStatus{Stage: s.Stage, SnoozeUntil: cloneTimePtr(s.SnoozeUntil)}, nil
}

// InPool сообщает, находится ли лид в активной воронке на момент now: лид не
// отложен либо срок snooze уже истёк.
func (s LeadStatus) InPool(now time.Time) bool {
	if s.SnoozeUntil == nil {
		return true
	}
	return !now.Before(*s.SnoozeUntil)
}

// cloneTimePtr копирует момент времени либо возвращает nil.
func cloneTimePtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	cloned := *t
	return &cloned
}
