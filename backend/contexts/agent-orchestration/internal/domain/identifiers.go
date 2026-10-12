package domain

import (
	"fmt"
	"strings"
)

// Идентификаторы и ссылки домена agent-orchestration.
//
// Каждый идентификатор — отдельный именованный тип, чтобы запуск нельзя было
// передать вместо пайплайна, а компанию — вместо тенанта. Значения неизменяемы.
// Ссылки на чужие агрегаты (компания, черновик) — только идентификаторы: прямая
// модель чужого контекста не импортируется (design.md, Реш. 5).
type (
	// PipelineID — идентификатор aggregate root agent-pipeline.
	PipelineID string

	// PipelineStepID — идентификатор entity pipeline-step внутри версии пайплайна.
	PipelineStepID string

	// RunID — идентификатор aggregate root agent-run.
	RunID string

	// StepRunID — идентификатор entity agent-step-run внутри запуска.
	StepRunID string

	// MissionID — идентификатор задания (поискового контура), в рамках которого
	// обрабатывается компания.
	MissionID string

	// CompanyID — ссылка на обрабатываемую компанию (владение lead-management).
	CompanyID string

	// TenantID — идентификатор тенанта-владельца данных.
	TenantID string

	// ActorID — идентификатор пользователя-оператора или машинного актора.
	ActorID string

	// BlockRef — ссылка шага пайплайна на зарегистрированный блок обработки.
	BlockRef string

	// CorrelationID — идентификатор сквозного сценария.
	CorrelationID string

	// CausationID — идентификатор команды или события, вызвавшего это событие.
	CausationID string

	// EventID — стабильный идентификатор события домена. Служит ключом
	// идемпотентности публикации и дедупликации у потребителя.
	EventID string

	// DraftRef — непрозрачная ссылка на черновик письма (владение
	// outreach-orchestration): идентификатор без содержания.
	DraftRef string
)

func (id PipelineID) String() string     { return string(id) }
func (id PipelineStepID) String() string { return string(id) }
func (id RunID) String() string          { return string(id) }
func (id StepRunID) String() string      { return string(id) }
func (id MissionID) String() string      { return string(id) }
func (id CompanyID) String() string      { return string(id) }
func (id TenantID) String() string       { return string(id) }
func (id ActorID) String() string        { return string(id) }
func (id BlockRef) String() string       { return string(id) }
func (id CorrelationID) String() string  { return string(id) }
func (id CausationID) String() string    { return string(id) }
func (id EventID) String() string        { return string(id) }
func (id DraftRef) String() string       { return string(id) }

func (id PipelineID) validate() error     { return validateID("pipeline_id", string(id)) }
func (id PipelineStepID) validate() error { return validateID("pipeline_step_id", string(id)) }
func (id RunID) validate() error          { return validateID("run_id", string(id)) }
func (id TenantID) validate() error       { return validateID("tenant_id", string(id)) }
func (id MissionID) validate() error      { return validateID("mission_id", string(id)) }
func (id CompanyID) validate() error      { return validateID("company_id", string(id)) }
func (id BlockRef) validate() error       { return validateID("block_ref", string(id)) }
func (id EventID) validate() error        { return validateID("event_id", string(id)) }
func (id DraftRef) validate() error       { return validateID("draft_ref", string(id)) }

// StepRunIDFor выводит идентификатор шага запуска детерминированно из запуска и
// шага пайплайна. Детерминированность — часть идемпотентности: повторный прогон
// того же шага адресует ту же запись шага, а не создаёт вторую.
func StepRunIDFor(runID RunID, stepID PipelineStepID) StepRunID {
	return StepRunID(string(runID) + ":" + string(stepID))
}

// validateID отвергает пустой или состоящий из пробелов идентификатор. Ссылки на
// чужие агрегаты (компания, черновик) при необходимости проверяются отдельно.
func validateID(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: %s", ErrEmptyID, field)
	}
	return nil
}
