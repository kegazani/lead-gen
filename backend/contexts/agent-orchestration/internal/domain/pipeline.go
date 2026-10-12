package domain

import (
	"fmt"
	"time"
)

// StepConfig — value object step-config: параметры блока и правило попыток шага.
// Параметры — данные реестра и публикации версии, а не персональные данные.
type StepConfig struct {
	params      map[string]string
	maxAttempts int
}

// NewStepConfig собирает конфигурацию шага. Отрицательное число попыток
// запрещено: ноль означает «применить значение по умолчанию» уровня запуска.
func NewStepConfig(params map[string]string, maxAttempts int) (StepConfig, error) {
	if maxAttempts < 0 {
		return StepConfig{}, fmt.Errorf("шаг: max_attempts не может быть отрицательным: %d", maxAttempts)
	}
	copied := make(map[string]string, len(params))
	for key, value := range params {
		copied[key] = value
	}
	return StepConfig{params: copied, maxAttempts: maxAttempts}, nil
}

// Params возвращает копию параметров блока.
func (c StepConfig) Params() map[string]string {
	out := make(map[string]string, len(c.params))
	for key, value := range c.params {
		out[key] = value
	}
	return out
}

// MaxAttempts возвращает предел попыток шага; 0 — значение по умолчанию.
func (c StepConfig) MaxAttempts() int { return c.maxAttempts }

// attemptsWithDefault возвращает предел попыток шага с учётом значения по
// умолчанию уровня запуска.
func (c StepConfig) attemptsWithDefault(defaultMax int) int {
	if c.maxAttempts > 0 {
		return c.maxAttempts
	}
	if defaultMax > 0 {
		return defaultMax
	}
	return 1
}

// PipelineStep — entity pipeline-step версии пайплайна. Несёт снимок описания
// блока, поэтому версия самодостаточна и не перечитывает реестр при исполнении.
type PipelineStep struct {
	id     PipelineStepID
	block  Block
	order  int
	config StepConfig
}

// ID возвращает идентификатор шага.
func (s PipelineStep) ID() PipelineStepID { return s.id }

// Block возвращает снимок описания блока.
func (s PipelineStep) Block() Block { return s.block }

// Ref возвращает ссылку на блок.
func (s PipelineStep) Ref() BlockRef { return s.block.Ref() }

// Order возвращает порядковый номер шага (с нуля).
func (s PipelineStep) Order() int { return s.order }

// Config возвращает конфигурацию шага.
func (s PipelineStep) Config() StepConfig { return s.config }

// StepSpec — вход описания шага при сборке версии пайплайна.
type StepSpec struct {
	// ID — идентификатор шага. Обязателен и уникален в версии.
	ID PipelineStepID
	// Ref — ссылка на зарегистрированный блок.
	Ref BlockRef
	// Config — параметры блока и правило попыток.
	Config StepConfig
}

// AgentPipeline — aggregate root agent-pipeline: версия пайплайна из блоков.
//
// Инварианты (design.md, Реш. 3):
//   - опубликованная версия неизменяема: изменение состава шагов создаёт новую
//     версию (Revise), а не правит текущую;
//   - шаг ссылается только на зарегистрированный блок: незарегистрированный блок
//     не исполняется и не подменяется другим.
type AgentPipeline struct {
	id          PipelineID
	tenantID    TenantID
	version     int
	steps       []PipelineStep
	published   bool
	publishedAt time.Time
	revision    int
	events      []DomainEvent
}

// NewPipeline собирает черновик версии пайплайна. Состав шагов проверяется против
// реестра блоков: неизвестный блок — ошибка конфигурации, а не исполняемый шаг.
// version — номер версии (>= 1).
func NewPipeline(id PipelineID, tenantID TenantID, version int, specs []StepSpec, registry *BlockRegistry) (*AgentPipeline, error) {
	if err := id.validate(); err != nil {
		return nil, err
	}
	if err := tenantID.validate(); err != nil {
		return nil, err
	}
	if version < 1 {
		return nil, fmt.Errorf("пайплайн: версия должна быть положительной: %d", version)
	}
	steps, err := buildSteps(specs, registry)
	if err != nil {
		return nil, err
	}
	return &AgentPipeline{id: id, tenantID: tenantID, version: version, steps: steps}, nil
}

// buildSteps проверяет и упорядочивает шаги версии против реестра блоков.
func buildSteps(specs []StepSpec, registry *BlockRegistry) ([]PipelineStep, error) {
	if registry == nil {
		return nil, fmt.Errorf("пайплайн: реестр блоков не задан")
	}
	seenID := make(map[PipelineStepID]struct{}, len(specs))
	steps := make([]PipelineStep, 0, len(specs))
	for _, spec := range specs {
		if err := spec.ID.validate(); err != nil {
			return nil, err
		}
		if _, exists := seenID[spec.ID]; exists {
			return nil, fmt.Errorf("%w: %s", ErrDuplicateStep, spec.ID)
		}
		seenID[spec.ID] = struct{}{}
		block, ok := registry.Lookup(spec.Ref)
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrUnregisteredBlock, spec.Ref)
		}
		steps = append(steps, PipelineStep{id: spec.ID, block: block, config: spec.Config})
	}
	for index := range steps {
		steps[index].order = index
	}
	return steps, nil
}

// ID возвращает идентификатор пайплайна.
func (p *AgentPipeline) ID() PipelineID { return p.id }

// TenantID возвращает тенант-владельца пайплайна.
func (p *AgentPipeline) TenantID() TenantID { return p.tenantID }

// Version возвращает номер версии пайплайна.
func (p *AgentPipeline) Version() int { return p.version }

// IsPublished сообщает, опубликована ли версия.
func (p *AgentPipeline) IsPublished() bool { return p.published }

// PublishedAt возвращает время публикации (нулевое для черновика).
func (p *AgentPipeline) PublishedAt() time.Time { return p.publishedAt }

// Revision возвращает ревизию агрегата для оптимистичной блокировки.
func (p *AgentPipeline) Revision() int { return p.revision }

// Steps возвращает копию шагов версии в порядке исполнения.
func (p *AgentPipeline) Steps() []PipelineStep { return append([]PipelineStep(nil), p.steps...) }

// StepByID возвращает шаг версии по идентификатору.
func (p *AgentPipeline) StepByID(id PipelineStepID) (PipelineStep, bool) {
	for _, step := range p.steps {
		if step.id == id {
			return step, true
		}
	}
	return PipelineStep{}, false
}

// Publish публикует черновик версии. Повторная публикация отвергается: версия
// неизменяема после публикации. Событие публикации буферизуется для outbox.
func (p *AgentPipeline) Publish(at time.Time, header EventHeader) error {
	if p.published {
		return fmt.Errorf("%w: %s v%d", ErrPipelineAlreadyPublished, p.id, p.version)
	}
	if len(p.steps) == 0 {
		return fmt.Errorf("%w: %s v%d", ErrEmptyPipeline, p.id, p.version)
	}
	if at.IsZero() {
		return ErrMissingTime
	}
	header.OccurredAt = at
	header.TenantID = p.tenantID
	if err := header.validate(); err != nil {
		return err
	}
	p.published = true
	p.publishedAt = at
	p.revision++

	stepIDs := make([]PipelineStepID, 0, len(p.steps))
	blocks := make([]BlockRef, 0, len(p.steps))
	for _, step := range p.steps {
		stepIDs = append(stepIDs, step.id)
		blocks = append(blocks, step.Ref())
	}
	p.emit(PipelinePublished{
		EventHeader: header,
		PipelineID:  p.id,
		Version:     p.version,
		Steps:       stepIDs,
		Blocks:      blocks,
	})
	return nil
}

// Revise возвращает новую версию пайплайна с изменённым составом шагов. Исходная
// (опубликованная) версия не изменяется: правка состава создаёт новую версию
// (инвариант неизменяемости). Новая версия — черновик, её нужно опубликовать.
func (p *AgentPipeline) Revise(specs []StepSpec, registry *BlockRegistry) (*AgentPipeline, error) {
	if !p.published {
		return nil, fmt.Errorf("%w: %s v%d", ErrPipelineNotPublished, p.id, p.version)
	}
	steps, err := buildSteps(specs, registry)
	if err != nil {
		return nil, err
	}
	return &AgentPipeline{
		id:       p.id,
		tenantID: p.tenantID,
		version:  p.version + 1,
		steps:    steps,
	}, nil
}

// DomainEvents возвращает копию неподтверждённых событий.
func (p *AgentPipeline) DomainEvents() []DomainEvent {
	return append([]DomainEvent(nil), p.events...)
}

// PullDomainEvents возвращает неподтверждённые события и очищает буфер.
func (p *AgentPipeline) PullDomainEvents() []DomainEvent {
	events := append([]DomainEvent(nil), p.events...)
	p.events = nil
	return events
}

// emit добавляет событие в буфер.
func (p *AgentPipeline) emit(event DomainEvent) { p.events = append(p.events, event) }

// Проверка соответствия интерфейсу на этапе компиляции.
var _ EventProducer = (*AgentPipeline)(nil)
