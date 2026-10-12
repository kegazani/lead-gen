package domain

import (
	"fmt"
	"strings"
	"time"
)

// AgentMission — value object agent-mission: контекст задания, к которому привязан
// агент. Задание поиска, обрабатываемая компания и тенант. Краткое описание
// задания (Criteria) — данные миссии, не персональные данные.
type AgentMission struct {
	id        MissionID
	companyID CompanyID
	tenantID  TenantID
	criteria  string
}

// NewAgentMission собирает миссию агента. Идентификаторы обязательны.
func NewAgentMission(id MissionID, companyID CompanyID, tenantID TenantID, criteria string) (AgentMission, error) {
	mission := AgentMission{
		id:        id,
		companyID: companyID,
		tenantID:  tenantID,
		criteria:  strings.TrimSpace(criteria),
	}
	if err := mission.validate(); err != nil {
		return AgentMission{}, err
	}
	return mission, nil
}

// ID возвращает идентификатор задания.
func (m AgentMission) ID() MissionID { return m.id }

// CompanyID возвращает идентификатор обрабатываемой компании.
func (m AgentMission) CompanyID() CompanyID { return m.companyID }

// TenantID возвращает тенант-владельца.
func (m AgentMission) TenantID() TenantID { return m.tenantID }

// Criteria возвращает краткое описание задания (цель поиска).
func (m AgentMission) Criteria() string { return m.criteria }

// validate проверяет обязательные поля миссии.
func (m AgentMission) validate() error {
	if err := m.id.validate(); err != nil {
		return err
	}
	if err := m.companyID.validate(); err != nil {
		return err
	}
	return m.tenantID.validate()
}

// ApprovalState — состояние ожидания решения оператора по черновику. Хранит
// точную ссылку и версию черновика: решение применяется только к ним.
type ApprovalState struct {
	draftRef     DraftRef
	draftVersion int
	requestedAt  time.Time
	granted      bool
	grantedAt    time.Time
	grantedBy    ActorID
}

// DraftRef возвращает ссылку на черновик, ожидающий решения.
func (a ApprovalState) DraftRef() DraftRef { return a.draftRef }

// DraftVersion возвращает версию черновика.
func (a ApprovalState) DraftVersion() int { return a.draftVersion }

// RequestedAt возвращает время запроса решения.
func (a ApprovalState) RequestedAt() time.Time { return a.requestedAt }

// Granted сообщает, что решение положительное.
func (a ApprovalState) Granted() bool { return a.granted }

// GrantedAt возвращает время положительного решения.
func (a ApprovalState) GrantedAt() time.Time { return a.grantedAt }

// GrantedBy возвращает актора решения.
func (a ApprovalState) GrantedBy() ActorID { return a.grantedBy }

// NewRunParams — вход создания запуска агента.
type NewRunParams struct {
	// ID — идентификатор запуска.
	ID RunID
	// Mission — задание и компания.
	Mission AgentMission
	// Pipeline — опубликованная версия пайплайна, к которой привязывается запуск.
	Pipeline *AgentPipeline
	// DefaultMaxAttempts — предел попыток шага по умолчанию для шагов без
	// собственного правила.
	DefaultMaxAttempts int
	// At — время создания.
	At time.Time
	// Header — конверт события создания (event_id и корреляции).
	Header EventHeader
}

// StepResult — наблюдаемый исход фиксации шага.
type StepResult struct {
	// StepRunID — идентификатор шага запуска.
	StepRunID StepRunID
	// Repeated — эффект уже был зафиксирован: повтор не дал второго эффекта.
	Repeated bool
	// RunCompleted — запуск достиг терминального состояния успеха.
	RunCompleted bool
}

// AgentRun — aggregate root agent-run: запуск агента по неизменяемой версии
// пайплайна для одной компании в контексте задания и тенанта.
//
// Инварианты (design.md, Реш. 4):
//   - запуск ссылается на неизменяемую версию пайплайна (снимок шагов внутри);
//   - каждый шаг даёт эффект не более одного раза (идемпотентность);
//   - терминальное состояние не возобновляется;
//   - необратимый шаг требует положительного решения оператора по точной версии
//     черновика;
//   - запуск одного тенанта не виден другому (тенант-скоуп).
type AgentRun struct {
	id              RunID
	mission         AgentMission
	pipelineID      PipelineID
	pipelineVersion int
	status          RunStatus
	steps           []*AgentStepRun
	cursor          int
	approval        *ApprovalState
	revision        int
	createdAt       time.Time
	updatedAt       time.Time
	events          []DomainEvent
}

// NewRun создаёт запуск агента по опубликованной версии пайплайна. Шаги версии
// копируются в состояние запуска: последующая публикация новой версии не меняет
// бегущий запуск.
func NewRun(params NewRunParams) (*AgentRun, error) {
	if err := params.ID.validate(); err != nil {
		return nil, err
	}
	if err := params.Mission.validate(); err != nil {
		return nil, err
	}
	if params.Pipeline == nil {
		return nil, fmt.Errorf("запуск: пайплайн не задан")
	}
	if !params.Pipeline.IsPublished() {
		return nil, fmt.Errorf("%w: %s v%d", ErrPipelineNotPublished, params.Pipeline.ID(), params.Pipeline.Version())
	}
	if params.Pipeline.TenantID() != params.Mission.TenantID() {
		return nil, fmt.Errorf("запуск: пайплайн и миссия принадлежат разным тенантам")
	}
	if params.At.IsZero() {
		return nil, ErrMissingTime
	}

	definition := params.Pipeline.Steps()
	steps := make([]*AgentStepRun, 0, len(definition))
	for _, step := range definition {
		steps = append(steps, newStepRun(params.ID, step, params.DefaultMaxAttempts))
	}

	header := params.Header
	header.OccurredAt = params.At
	header.TenantID = params.Mission.TenantID()
	if err := header.validate(); err != nil {
		return nil, err
	}

	run := &AgentRun{
		id:              params.ID,
		mission:         params.Mission,
		pipelineID:      params.Pipeline.ID(),
		pipelineVersion: params.Pipeline.Version(),
		status:          StatusQueued,
		steps:           steps,
		createdAt:       params.At,
		updatedAt:       params.At,
	}
	return run, nil
}

// ID возвращает идентификатор запуска.
func (r *AgentRun) ID() RunID { return r.id }

// TenantID возвращает тенант-владельца запуска.
func (r *AgentRun) TenantID() TenantID { return r.mission.TenantID() }

// Mission возвращает миссию запуска.
func (r *AgentRun) Mission() AgentMission { return r.mission }

// PipelineID возвращает идентификатор пайплайна.
func (r *AgentRun) PipelineID() PipelineID { return r.pipelineID }

// PipelineVersion возвращает версию пайплайна, к которой привязан запуск.
func (r *AgentRun) PipelineVersion() int { return r.pipelineVersion }

// Status возвращает состояние запуска.
func (r *AgentRun) Status() RunStatus { return r.status }

// Revision возвращает ревизию агрегата для оптимистичной блокировки.
func (r *AgentRun) Revision() int { return r.revision }

// CreatedAt возвращает время создания запуска.
func (r *AgentRun) CreatedAt() time.Time { return r.createdAt }

// UpdatedAt возвращает время последнего изменения.
func (r *AgentRun) UpdatedAt() time.Time { return r.updatedAt }

// Cursor возвращает индекс текущего шага.
func (r *AgentRun) Cursor() int { return r.cursor }

// Steps возвращает шаги запуска.
func (r *AgentRun) Steps() []*AgentStepRun { return append([]*AgentStepRun(nil), r.steps...) }

// Approval возвращает состояние ожидания решения (nil — ожидания нет).
func (r *AgentRun) Approval() *ApprovalState {
	if r.approval == nil {
		return nil
	}
	copied := *r.approval
	return &copied
}

// CurrentStep возвращает текущий шаг запуска. Второе значение — false, если
// запуск терминален или все шаги пройдены.
func (r *AgentRun) CurrentStep() (*AgentStepRun, bool) {
	if r.status.IsTerminal() || r.cursor >= len(r.steps) {
		return nil, false
	}
	return r.steps[r.cursor], true
}

// Start переводит запуск из очереди в исполнение.
func (r *AgentRun) Start(at time.Time, header EventHeader) error {
	if r.status == StatusRunning {
		return nil
	}
	if err := r.transition(StatusRunning, at); err != nil {
		return err
	}
	base := r.headerFor(header, at)
	event := RunStarted{
		EventHeader: base,
		RunID:       r.id,
		MissionID:   r.mission.ID(),
		CompanyID:   r.mission.CompanyID(),
		PipelineID:  r.pipelineID,
		Version:     r.pipelineVersion,
	}
	r.emit(event)
	return nil
}

// BeginStep начинает шаг по идентификатору. alreadyDone=true означает, что шаг уже
// дал результат: повтор не начинает новую попытку (идемпотентность). Шаги
// исполняются по порядку: начать шаг вне очереди нельзя.
func (r *AgentRun) BeginStep(stepID PipelineStepID, at time.Time) (step *AgentStepRun, alreadyDone bool, err error) {
	if at.IsZero() {
		return nil, false, ErrMissingTime
	}
	step, index, err := r.stepByID(stepID)
	if err != nil {
		return nil, false, err
	}
	if step.status == StepRunSucceeded {
		return step, true, nil
	}
	if err := r.ensureRunnable(at); err != nil {
		return nil, false, err
	}
	if index != r.cursor {
		return nil, false, fmt.Errorf("%w: шаг %s вне очереди (текущий %d)", ErrNoCurrentStep, stepID, r.cursor)
	}
	alreadyDone = step.begin(at)
	if !alreadyDone {
		r.touch(at)
	}
	return step, alreadyDone, nil
}

// CompleteStep фиксирует успешный исход текущего шага. Повтор шага, уже давшего
// эффект, ничего не меняет (Repeated=true) — эффект не удваивается.
//
// Для необратимого шага (отправка) обязательно положительное решение оператора по
// точной версии черновика: иначе отправка не выполняется (policy
// irreversible-step-approval).
func (r *AgentRun) CompleteStep(stepID PipelineStepID, outcome BlockOutcome, artifactRef string, draftRef DraftRef, draftVersion int, at time.Time, header EventHeader) (StepResult, error) {
	if at.IsZero() {
		return StepResult{}, ErrMissingTime
	}
	step, index, err := r.stepByID(stepID)
	if err != nil {
		return StepResult{}, err
	}
	if step.status == StepRunSucceeded {
		return StepResult{StepRunID: step.id, Repeated: true}, nil
	}
	if index != r.cursor {
		return StepResult{}, fmt.Errorf("%w: шаг %s вне очереди", ErrNoCurrentStep, stepID)
	}
	if step.status != StepRunRunning {
		return StepResult{}, fmt.Errorf("%w: %s", ErrStepNotStarted, step.id)
	}
	if outcome.kind != OutcomeSucceeded {
		return StepResult{}, fmt.Errorf("шаг %s: успешным исходом можно закрыть только исход succeeded", step.id)
	}
	if err := outcome.Validate(); err != nil {
		return StepResult{}, err
	}

	if step.step.Block().Irreversible() {
		if err := r.requireApproval(draftRef, draftVersion); err != nil {
			return StepResult{}, err
		}
	}

	step.succeeded(outcome, artifactRef, draftRef, draftVersion, at)
	r.touch(at)

	base := r.headerFor(header, at)
	r.emit(StepCompleted{
		EventHeader:  base,
		RunID:        r.id,
		StepRunID:    step.id,
		StepID:       step.step.ID(),
		BlockRef:     step.step.Ref(),
		Order:        step.step.Order(),
		Outcome:      outcome,
		Attempts:     step.attempts,
		ArtifactRef:  artifactRef,
		DraftRef:     draftRef,
		DraftVersion: draftVersion,
	})

	// Необратимый шаг исполнен по решению — решение больше не действительно.
	if step.step.Block().Irreversible() {
		r.approval = nil
	}

	r.cursor++
	completed := false
	if r.cursor >= len(r.steps) {
		if err := r.transition(StatusCompleted, at); err != nil {
			return StepResult{}, err
		}
		r.emit(RunCompleted{
			EventHeader: base,
			RunID:       r.id,
			MissionID:   r.mission.ID(),
			CompanyID:   r.mission.CompanyID(),
			PipelineID:  r.pipelineID,
			To:          StatusCompleted,
		})
		completed = true
	}
	return StepResult{StepRunID: step.id, RunCompleted: completed}, nil
}

// FailStep фиксирует отказ текущего шага. retryable=true — попытки не исчерпаны,
// шаг может быть повторён (policy step-idempotency). Исчерпание попыток переводит
// запуск в needs-attention и публикует событие блокировки.
func (r *AgentRun) FailStep(stepID PipelineStepID, reason string, at time.Time, header EventHeader) (retryable bool, err error) {
	if at.IsZero() {
		return false, ErrMissingTime
	}
	step, index, err := r.stepByID(stepID)
	if err != nil {
		return false, err
	}
	if step.status == StepRunSucceeded {
		// Идемпотентность: успешный шаг не переводится в отказ повтором.
		return false, nil
	}
	if index != r.cursor {
		return false, fmt.Errorf("%w: шаг %s вне очереди", ErrNoCurrentStep, stepID)
	}
	if step.status != StepRunRunning {
		return false, fmt.Errorf("%w: %s", ErrStepNotStarted, step.id)
	}
	outcome := Failed(reason)
	if err := outcome.Validate(); err != nil {
		return false, err
	}
	retryable = step.failed(outcome, at)
	r.touch(at)

	base := r.headerFor(header, at)
	r.emit(StepFailed{
		EventHeader: base,
		RunID:       r.id,
		StepRunID:   step.id,
		StepID:      step.step.ID(),
		BlockRef:    step.step.Ref(),
		Order:       step.step.Order(),
		Outcome:     outcome,
		Attempts:    step.attempts,
	})

	if !retryable {
		if err := r.transition(StatusNeedsAttention, at); err != nil {
			return false, err
		}
		r.emit(RunBlocked{
			EventHeader: base,
			RunID:       r.id,
			MissionID:   r.mission.ID(),
			CompanyID:   r.mission.CompanyID(),
			To:          StatusNeedsAttention,
			CurrentStep: step.step.ID(),
			BlockRef:    step.step.Ref(),
			Reason:      reason,
		})
	}
	return retryable, nil
}

// MarkStepUnavailable фиксирует явную недоступность внешней системы шага.
// Недоступность SHALL NOT выдаваться за успех: обязательный шаг держит обработку
// незавершённой (blocked), необязательный — пропускается.
func (r *AgentRun) MarkStepUnavailable(stepID PipelineStepID, reason string, at time.Time, header EventHeader) (blocked bool, err error) {
	if at.IsZero() {
		return false, ErrMissingTime
	}
	step, index, err := r.stepByID(stepID)
	if err != nil {
		return false, err
	}
	if step.status == StepRunSucceeded {
		return false, nil
	}
	if index != r.cursor {
		return false, fmt.Errorf("%w: шаг %s вне очереди", ErrNoCurrentStep, stepID)
	}
	if step.status != StepRunRunning {
		return false, fmt.Errorf("%w: %s", ErrStepNotStarted, step.id)
	}
	outcome := Unavailable(reason)
	if err := outcome.Validate(); err != nil {
		return false, err
	}
	step.unavailable(outcome, at)
	r.touch(at)

	base := r.headerFor(header, at)
	r.emit(StepFailed{
		EventHeader: base,
		RunID:       r.id,
		StepRunID:   step.id,
		StepID:      step.step.ID(),
		BlockRef:    step.step.Ref(),
		Order:       step.step.Order(),
		Outcome:     outcome,
		Attempts:    step.attempts,
	})

	if step.step.Block().Mandatory() {
		if err := r.transition(StatusBlocked, at); err != nil {
			return false, err
		}
		r.emit(RunBlocked{
			EventHeader: base,
			RunID:       r.id,
			MissionID:   r.mission.ID(),
			CompanyID:   r.mission.CompanyID(),
			To:          StatusBlocked,
			CurrentStep: step.step.ID(),
			BlockRef:    step.step.Ref(),
			Reason:      reason,
		})
		return true, nil
	}

	// Необязательный недоступный блок пропускается: обработка идёт дальше, но
	// недоступность не подменяется успехом (шаг остаётся unavailable).
	r.cursor++
	if r.cursor >= len(r.steps) {
		if err := r.transition(StatusCompleted, at); err != nil {
			return false, err
		}
		r.emit(RunCompleted{
			EventHeader: base,
			RunID:       r.id,
			MissionID:   r.mission.ID(),
			CompanyID:   r.mission.CompanyID(),
			PipelineID:  r.pipelineID,
			To:          StatusCompleted,
		})
	}
	return false, nil
}

// RequestApproval переводит запуск в ожидание решения оператора по черновику.
// Решение применяется только к указанной версии черновика.
func (r *AgentRun) RequestApproval(draftRef DraftRef, draftVersion int, actor ActorID, at time.Time, header EventHeader) error {
	if at.IsZero() {
		return ErrMissingTime
	}
	if err := draftRef.validate(); err != nil {
		return err
	}
	if err := r.transition(StatusAwaitingApproval, at); err != nil {
		return err
	}
	r.approval = &ApprovalState{
		draftRef:     draftRef,
		draftVersion: draftVersion,
		requestedAt:  at,
	}
	r.touch(at)
	_ = header
	return nil
}

// GrantApproval фиксирует положительное решение оператора. Если черновик изменился
// (не совпадает ссылка или версия), прежнее ожидание не подтверждается: требуется
// новое решение.
func (r *AgentRun) GrantApproval(draftRef DraftRef, draftVersion int, actor ActorID, at time.Time, header EventHeader) error {
	if at.IsZero() {
		return ErrMissingTime
	}
	if r.status != StatusAwaitingApproval || r.approval == nil {
		return ErrNoApprovalPending
	}
	if r.approval.draftRef != draftRef || r.approval.draftVersion != draftVersion {
		return fmt.Errorf("%w: %s v%d", ErrDraftChanged, r.approval.draftRef, r.approval.draftVersion)
	}
	r.approval.granted = true
	r.approval.grantedAt = at
	r.approval.grantedBy = actor
	if err := r.transition(StatusRunning, at); err != nil {
		return err
	}
	r.touch(at)
	_ = header
	return nil
}

// RejectApproval фиксирует отказ оператора: отправка не выполняется, запуск
// требует вмешательства (терминальное состояние).
func (r *AgentRun) RejectApproval(actor ActorID, reason string, at time.Time, header EventHeader) error {
	if at.IsZero() {
		return ErrMissingTime
	}
	if r.status != StatusAwaitingApproval || r.approval == nil {
		return ErrNoApprovalPending
	}
	if strings.TrimSpace(reason) == "" {
		return ErrReasonRequired
	}
	step := r.steps[r.cursor]
	from := r.status
	if err := r.transition(StatusNeedsAttention, at); err != nil {
		return err
	}
	r.touch(at)
	base := r.headerFor(header, at)
	r.emit(RunBlocked{
		EventHeader: base,
		RunID:       r.id,
		MissionID:   r.mission.ID(),
		CompanyID:   r.mission.CompanyID(),
		From:        from,
		To:          StatusNeedsAttention,
		CurrentStep: step.step.ID(),
		BlockRef:    step.step.Ref(),
		Reason:      reason,
	})
	return nil
}

// Stop останавливает запуск командой оператора: запуск переходит в терминальное
// состояние «остановлен» и не выполняет оставшиеся шаги. Остановка терминального
// запуска отвергается.
func (r *AgentRun) Stop(actor ActorID, reason string, at time.Time, header EventHeader) error {
	if at.IsZero() {
		return ErrMissingTime
	}
	if r.status.IsTerminal() {
		return fmt.Errorf("%w: %s", ErrTerminalRun, r.status)
	}
	if strings.TrimSpace(reason) == "" {
		return ErrReasonRequired
	}
	from := r.status
	if err := r.transition(StatusStopped, at); err != nil {
		return err
	}
	r.touch(at)
	base := r.headerFor(header, at)
	r.emit(RunCompleted{
		EventHeader: base,
		RunID:       r.id,
		MissionID:   r.mission.ID(),
		CompanyID:   r.mission.CompanyID(),
		PipelineID:  r.pipelineID,
		From:        from,
		To:          StatusStopped,
		Reason:      reason,
	})
	return nil
}

// requireApproval проверяет наличие положительного решения оператора по точной
// версии черновика для необратимого шага.
func (r *AgentRun) requireApproval(draftRef DraftRef, draftVersion int) error {
	if r.approval == nil {
		return ErrApprovalRequired
	}
	if !r.approval.granted {
		return ErrApprovalRequired
	}
	if r.approval.draftRef != draftRef || r.approval.draftVersion != draftVersion {
		return fmt.Errorf("%w: %s v%d", ErrDraftChanged, r.approval.draftRef, r.approval.draftVersion)
	}
	return nil
}

// stepByID находит шаг запуска по идентификатору шага пайплайна.
func (r *AgentRun) stepByID(id PipelineStepID) (*AgentStepRun, int, error) {
	for index, step := range r.steps {
		if step.step.ID() == id {
			return step, index, nil
		}
	}
	return nil, -1, fmt.Errorf("%w: %s", ErrStepNotFound, id)
}

// ensureRunnable проверяет, что запуск может начинать шаг: он не терминален, не в
// очереди и не ждёт решения. Заблокированный запуск возвращается в работу
// (blocked → running) — возобновление после устранения недоступности.
func (r *AgentRun) ensureRunnable(at time.Time) error {
	if r.status.IsTerminal() {
		return fmt.Errorf("%w: %s", ErrTerminalRun, r.status)
	}
	switch r.status {
	case StatusQueued:
		return fmt.Errorf("%w: запуск ещё не стартовал", ErrInvalidRunTransition)
	case StatusAwaitingApproval:
		return fmt.Errorf("%w: запуск ожидает решения оператора", ErrInvalidRunTransition)
	case StatusBlocked:
		return r.transition(StatusRunning, at)
	default:
		return nil
	}
}

// touch фиксирует изменение агрегата: время и инкремент ревизии для оптимистичной
// блокировки. Вызывается на каждом успешном изменении состояния.
func (r *AgentRun) touch(at time.Time) {
	r.updatedAt = at
	r.revision++
}

// transition выполняет переход статусной модели, отвергая недопустимый.
func (r *AgentRun) transition(to RunStatus, at time.Time) error {
	if r.status.IsTerminal() {
		return fmt.Errorf("%w: %s", ErrTerminalRun, r.status)
	}
	if r.status == to {
		return nil
	}
	if !r.status.canTransitionTo(to) {
		return fmt.Errorf("%w: %s → %s", ErrInvalidRunTransition, r.status, to)
	}
	r.status = to
	r.touch(at)
	return nil
}

// headerFor собирает конверт события с обязательными полями запуска. tenant_id и
// occurred_at берутся из запуска: событие не может «утечь» в чужой тенант.
func (r *AgentRun) headerFor(header EventHeader, at time.Time) EventHeader {
	header.OccurredAt = at
	header.TenantID = r.mission.TenantID()
	return header
}

// DomainEvents возвращает копию неподтверждённых событий.
func (r *AgentRun) DomainEvents() []DomainEvent {
	return append([]DomainEvent(nil), r.events...)
}

// PullDomainEvents возвращает неподтверждённые события и очищает буфер.
func (r *AgentRun) PullDomainEvents() []DomainEvent {
	events := append([]DomainEvent(nil), r.events...)
	r.events = nil
	return events
}

// emit добавляет событие в буфер.
func (r *AgentRun) emit(event DomainEvent) { r.events = append(r.events, event) }

// Проверка соответствия интерфейсу на этапе компиляции.
var _ EventProducer = (*AgentRun)(nil)
