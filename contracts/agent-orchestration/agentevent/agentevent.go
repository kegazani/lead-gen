// Package agentevent — опубликованный (published language) контракт agent-event
// контекста agent-orchestration: события жизненного цикла запуска агента и его
// шагов для аналитики.
//
// Владелец контракта — agent-orchestration: он производит события шагов и
// запусков. Потребитель — analytics (lead-quality-insights): он читает исходы
// шагов, чтобы анализировать причины «не подходит». Событие несёт идентификаторы,
// имя блока, время и исход с нормализованной причиной; секретов, токенов и
// персональных данных здесь нет (Реш. 12 design.md).
//
// Зависимости — только стандартная библиотека.
//
// Транспорт — async/amqp (event): публикация через transactional outbox владельца.
package agentevent

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ContractName — имя публикуемого контракта.
const ContractName = "agent-event"

// SchemaVersion — текущая версия схемы контракта (semver).
const SchemaVersion = "1.0.0"

// Имена событий. Совпадают с именами domain events каталога доменной архитектуры.
const (
	// NamePipelinePublished — опубликована версия пайплайна.
	NamePipelinePublished = "agent-pipeline-published"
	// NameRunStarted — запуск агента начат.
	NameRunStarted = "agent-run-started"
	// NameStepCompleted — шаг агента завершён успехом.
	NameStepCompleted = "agent-step-completed"
	// NameStepFailed — шаг агента завершился отказом или недоступностью.
	NameStepFailed = "agent-step-failed"
	// NameRunCompleted — запуск агента достиг терминального состояния.
	NameRunCompleted = "agent-run-completed"
	// NameRunBlocked — запуск агента заблокирован (недоступность или вмешательство).
	NameRunBlocked = "agent-run-blocked"
)

// Исходы шага в событии.
const (
	// OutcomeSucceeded — шаг завершён успехом.
	OutcomeSucceeded = "succeeded"
	// OutcomeFailed — шаг завершился отказом с нормализованной причиной.
	OutcomeFailed = "failed"
	// OutcomeUnavailable — внешняя система шага не выбрана или недоступна.
	OutcomeUnavailable = "unavailable"
)

// ErrInvalid — событие не проходит проверку обязательных полей.
var ErrInvalid = errors.New("agentevent: некорректное событие")

// Envelope — конверт события: обязательные поля жизненного цикла и корреляции.
type Envelope struct {
	EventID       string    `json:"event_id"`
	Name          string    `json:"name"`
	OccurredAt    time.Time `json:"occurred_at"`
	TenantID      string    `json:"tenant_id"`
	CorrelationID string    `json:"correlation_id"`
	CausationID   string    `json:"causation_id"`
}

// StepEvent — событие шага агента. Публикуется на каждый шаг с временем, блоком и
// исходом (Реш. 12 design.md). Для отсутствия содержимого чужих артефактов несёт
// только непрозрачные ссылки.
type StepEvent struct {
	Envelope
	RunID        string `json:"run_id"`
	StepRunID    string `json:"step_run_id"`
	StepID       string `json:"step_id"`
	BlockRef     string `json:"block_ref"`
	Order        int    `json:"order"`
	Outcome      string `json:"outcome"`
	Reason       string `json:"reason,omitempty"`
	Attempts     int    `json:"attempts"`
	ArtifactRef  string `json:"artifact_ref,omitempty"`
	DraftRef     string `json:"draft_ref,omitempty"`
	DraftVersion int    `json:"draft_version,omitempty"`
}

// RunEvent — событие запуска агента (старт, терминальный исход, блокировка).
type RunEvent struct {
	Envelope
	RunID       string `json:"run_id"`
	MissionID   string `json:"mission_id"`
	CompanyID   string `json:"company_id"`
	PipelineID  string `json:"pipeline_id"`
	Version     int    `json:"pipeline_version"`
	From        string `json:"from,omitempty"`
	To          string `json:"to"`
	CurrentStep string `json:"current_step,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// PipelineEvent — событие публикации версии пайплайна.
type PipelineEvent struct {
	Envelope
	PipelineID string   `json:"pipeline_id"`
	Version    int      `json:"pipeline_version"`
	Steps      []string `json:"steps"`
}

// Validate проверяет обязательные поля конверта события.
func (e Envelope) Validate() error {
	var invalid []string
	if strings.TrimSpace(e.EventID) == "" {
		invalid = append(invalid, "event_id")
	}
	if strings.TrimSpace(e.Name) == "" {
		invalid = append(invalid, "name")
	}
	if strings.TrimSpace(e.TenantID) == "" {
		invalid = append(invalid, "tenant_id")
	}
	if e.OccurredAt.IsZero() {
		invalid = append(invalid, "occurred_at")
	}
	if len(invalid) > 0 {
		return fmt.Errorf("%w: %s", ErrInvalid, strings.Join(invalid, ", "))
	}
	return nil
}

// Validate проверяет конверт и обязательные поля события шага.
func (e StepEvent) Validate() error {
	if err := e.Envelope.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(e.RunID) == "" || strings.TrimSpace(e.BlockRef) == "" {
		return fmt.Errorf("%w: run_id, block_ref", ErrInvalid)
	}
	switch e.Outcome {
	case OutcomeSucceeded, OutcomeFailed, OutcomeUnavailable:
	default:
		return fmt.Errorf("%w: outcome %q", ErrInvalid, e.Outcome)
	}
	return nil
}

// Validate проверяет конверт и обязательные поля события запуска.
func (e RunEvent) Validate() error {
	if err := e.Envelope.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(e.RunID) == "" || strings.TrimSpace(e.To) == "" {
		return fmt.Errorf("%w: run_id, to", ErrInvalid)
	}
	return nil
}
