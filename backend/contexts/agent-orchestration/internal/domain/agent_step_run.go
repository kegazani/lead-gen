package domain

import (
	"fmt"
	"time"
)

// StepRunStatus — состояние шага запуска агента.
type StepRunStatus string

const (
	// StepRunPending — шаг ещё не начинался.
	StepRunPending StepRunStatus = "pending"

	// StepRunRunning — шаг начат (попытка выполняется).
	StepRunRunning StepRunStatus = "running"

	// StepRunSucceeded — шаг завершён успехом (терминальное состояние шага).
	StepRunSucceeded StepRunStatus = "succeeded"

	// StepRunFailed — шаг завершился отказом. Может быть повторён, пока попытки не
	// исчерпаны.
	StepRunFailed StepRunStatus = "failed"

	// StepRunUnavailable — внешняя система шага не выбрана или недоступна
	// (терминальное состояние шага).
	StepRunUnavailable StepRunStatus = "unavailable"
)

// isValid сообщает, что состояние шага объявлено доменом.
func (s StepRunStatus) isValid() bool {
	switch s {
	case StepRunPending, StepRunRunning, StepRunSucceeded, StepRunFailed, StepRunUnavailable:
		return true
	default:
		return false
	}
}

// IsTerminal сообщает, что шаг больше не будет исполнен в этом запуске.
func (s StepRunStatus) IsTerminal() bool {
	return s == StepRunSucceeded
}

// AgentStepRun — entity agent-step-run: исполнение одного шага пайплайна внутри
// запуска агента.
//
// Идемпотентность (policy step-idempotency): шаг даёт эффект не более одного раза.
// После успеха повтор шага — no-op (исход возвращается из сохранённого
// состояния), а не второй эффект. Идемпотентный ключ шага детерминирован (см.
// StepRunIDFor), поэтому повтор адресует ту же запись.
type AgentStepRun struct {
	id           StepRunID
	step         PipelineStep
	status       StepRunStatus
	attempts     int
	maxAttempts  int
	outcome      BlockOutcome
	artifactRef  string
	draftRef     DraftRef
	draftVersion int
	startedAt    time.Time
	finishedAt   time.Time
}

// ID возвращает идентификатор шага запуска.
func (s *AgentStepRun) ID() StepRunID { return s.id }

// Step возвращает определение шага пайплайна.
func (s *AgentStepRun) Step() PipelineStep { return s.step }

// StepID возвращает идентификатор шага пайплайна.
func (s *AgentStepRun) StepID() PipelineStepID { return s.step.ID() }

// Ref возвращает ссылку на блок.
func (s *AgentStepRun) Ref() BlockRef { return s.step.Ref() }

// Order возвращает порядковый номер шага.
func (s *AgentStepRun) Order() int { return s.step.Order() }

// Status возвращает состояние шага.
func (s *AgentStepRun) Status() StepRunStatus { return s.status }

// Attempts возвращает число начатых попыток шага.
func (s *AgentStepRun) Attempts() int { return s.attempts }

// MaxAttempts возвращает предел попыток шага.
func (s *AgentStepRun) MaxAttempts() int { return s.maxAttempts }

// Outcome возвращает исход шага.
func (s *AgentStepRun) Outcome() BlockOutcome { return s.outcome }

// ArtifactRef возвращает непрозрачную ссылку на артефакт шага (например,
// идентификатор найденной компании). Содержания артефакта здесь нет.
func (s *AgentStepRun) ArtifactRef() string { return s.artifactRef }

// DraftRef возвращает ссылку на черновик, произведённый шагом.
func (s *AgentStepRun) DraftRef() DraftRef { return s.draftRef }

// DraftVersion возвращает версию черновика.
func (s *AgentStepRun) DraftVersion() int { return s.draftVersion }

// StartedAt возвращает время начала шага.
func (s *AgentStepRun) StartedAt() time.Time { return s.startedAt }

// FinishedAt возвращает время завершения шага.
func (s *AgentStepRun) FinishedAt() time.Time { return s.finishedAt }

// newStepRun создаёт шаг запуска из определения шага пайплайна.
func newStepRun(runID RunID, step PipelineStep, defaultMaxAttempts int) *AgentStepRun {
	return &AgentStepRun{
		id:          StepRunIDFor(runID, step.ID()),
		step:        step,
		status:      StepRunPending,
		maxAttempts: step.Config().attemptsWithDefault(defaultMaxAttempts),
	}
}

// begin начинает попытку шага. Если шаг уже успешен — возвращает признак
// alreadyDone и не начинает новую попытку (идемпотентность: эффект один раз).
func (s *AgentStepRun) begin(at time.Time) (alreadyDone bool) {
	if s.status == StepRunSucceeded {
		return true
	}
	s.status = StepRunRunning
	s.attempts++
	s.startedAt = at
	s.finishedAt = time.Time{}
	return false
}

// succeeded фиксирует успешный исход шага.
func (s *AgentStepRun) succeeded(outcome BlockOutcome, artifactRef string, draftRef DraftRef, draftVersion int, at time.Time) {
	s.status = StepRunSucceeded
	s.outcome = outcome
	s.artifactRef = artifactRef
	s.draftRef = draftRef
	s.draftVersion = draftVersion
	s.finishedAt = at
}

// failed фиксирует отказ шага. Признак retryable — попытки ещё не исчерпаны.
func (s *AgentStepRun) failed(outcome BlockOutcome, at time.Time) (retryable bool) {
	s.status = StepRunFailed
	s.outcome = outcome
	s.finishedAt = at
	return s.attempts < s.maxAttempts
}

// unavailable фиксирует явную недоступность внешней системы шага.
func (s *AgentStepRun) unavailable(outcome BlockOutcome, at time.Time) {
	s.status = StepRunUnavailable
	s.outcome = outcome
	s.finishedAt = at
}

// validateInState проверяет согласованность состояния шага.
func (s *AgentStepRun) validate() error {
	if s.id == "" {
		return fmt.Errorf("шаг запуска: пустой идентификатор")
	}
	if !s.status.isValid() {
		return fmt.Errorf("%w: %q", ErrUnknownStepStatus, s.status)
	}
	if s.status == StepRunSucceeded || s.status == StepRunFailed || s.status == StepRunUnavailable {
		if err := s.outcome.Validate(); err != nil {
			return err
		}
	}
	return nil
}
