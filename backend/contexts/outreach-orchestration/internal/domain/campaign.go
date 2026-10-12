package domain

import (
	"fmt"
	"strings"
	"time"
)

// CampaignState — состояние кампании.
type CampaignState string

// CampaignPurpose separates editorial review from delivery approval. Empty/old
// snapshots are normalized to delivery for legacy behavior.
type CampaignPurpose string

const (
	CampaignPurposeDelivery  CampaignPurpose = "delivery"
	CampaignPurposeEditorial CampaignPurpose = "editorial"
)

const (
	// CampaignStateDraft — кампания создана, черновиков ещё нет.
	CampaignStateDraft CampaignState = "draft"

	// CampaignStateActive — в кампании есть черновики в работе.
	CampaignStateActive CampaignState = "active"

	// CampaignStateCompleted — выполнение кампании завершено и не возобновляется
	// (инвариант I3).
	CampaignStateCompleted CampaignState = "completed"
)

// isValid сообщает, что состояние кампании объявлено доменом.
func (s CampaignState) isValid() bool {
	switch s {
	case CampaignStateDraft, CampaignStateActive, CampaignStateCompleted:
		return true
	default:
		return false
	}
}

// CreateCampaignParams — данные создания кампании.
type CreateCampaignParams struct {
	ID       CampaignID
	TenantID TenantID
	Name     string
	Window   CampaignWindow
	Purpose  CampaignPurpose
}

// DecisionParams — данные решения оператора по черновику.
type DecisionParams struct {
	DecisionID   DecisionID
	DraftID      DraftID
	DraftVersion int
	Actor        ActorID
	Reason       string
	DecidedAt    time.Time
}

// Campaign — aggregate root контекста outreach-orchestration.
//
// Агрегат владеет черновиками и решениями оператора и обеспечивает инварианты:
//
//	I1 — действие исполняется только при положительном решении по точной
//	     версии черновика (ExecuteDraft/FailDraft требуют такого решения);
//	I2 — решение привязано к версии черновика, правка повышает версию и
//	     требует нового решения;
//	I3 — завершённая кампания не возобновляется.
//
// Поля закрыты: состояние меняется только методами агрегата, прямой записи в
// хранилище в обход инвариантов домен не предусматривает.
type Campaign struct {
	id        CampaignID
	tenantID  TenantID
	name      string
	window    CampaignWindow
	state     CampaignState
	purpose   CampaignPurpose
	drafts    map[DraftID]*ActionDraft
	order     []DraftID
	decisions []OperatorDecision
	events    []DomainEvent

	// definitions — сценарии последовательностей кампании (сущность
	// sequence-definition), execution — участия лидов (сущность
	// sequence-execution). Порядок карт задают срезы definitionOrder и
	// executionOrder.
	definitions     map[SequenceID]*SequenceDefinition
	definitionOrder []SequenceID
	executions      map[ExecutionID]*SequenceExecution
	executionOrder  []ExecutionID
}

// CreateCampaign создаёт кампанию в состоянии «черновик».
func CreateCampaign(params CreateCampaignParams) (*Campaign, error) {
	if err := params.ID.validate(); err != nil {
		return nil, err
	}
	if err := params.TenantID.validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.Name) == "" {
		return nil, fmt.Errorf("%w: кампания %s", ErrEmptyCampaignName, params.ID)
	}
	if err := params.Window.validate(); err != nil {
		return nil, fmt.Errorf("%w: кампания %s", err, params.ID)
	}
	purpose := params.Purpose
	if purpose == "" {
		purpose = CampaignPurposeDelivery
	}
	if purpose != CampaignPurposeDelivery && purpose != CampaignPurposeEditorial {
		return nil, ErrInvalidCampaignPurpose
	}
	return &Campaign{
		id:          params.ID,
		tenantID:    params.TenantID,
		name:        strings.TrimSpace(params.Name),
		window:      params.Window,
		state:       CampaignStateDraft,
		purpose:     purpose,
		drafts:      make(map[DraftID]*ActionDraft),
		definitions: make(map[SequenceID]*SequenceDefinition),
		executions:  make(map[ExecutionID]*SequenceExecution),
	}, nil
}

// ID возвращает идентификатор кампании.
func (c *Campaign) ID() CampaignID { return c.id }

// TenantID возвращает тенанта-владельца кампании.
func (c *Campaign) TenantID() TenantID { return c.tenantID }

// Name возвращает название кампании.
func (c *Campaign) Name() string { return c.name }

// Window возвращает окно выполнения действий кампании.
func (c *Campaign) Window() CampaignWindow { return c.window }

// State возвращает состояние кампании.
func (c *Campaign) State() CampaignState { return c.state }

// Purpose returns campaign execution purpose.
func (c *Campaign) Purpose() CampaignPurpose {
	if c.purpose == "" {
		return CampaignPurposeDelivery
	}
	return c.purpose
}

// DraftAction создаёт черновик действия и сразу ставит его на утверждение.
// Созданный черновик не исполняется: до положительного решения он остаётся в
// состоянии «ожидает утверждения».
func (c *Campaign) DraftAction(id DraftID, spec DraftSpec, at time.Time) (*ActionDraft, []DomainEvent, error) {
	if c.Purpose() == CampaignPurposeEditorial {
		return nil, nil, ErrEditorialDeliveryForbidden
	}
	if c.state == CampaignStateCompleted {
		return nil, nil, fmt.Errorf("%w: кампания %s", ErrCampaignCompleted, c.id)
	}
	if _, exists := c.drafts[id]; exists {
		return nil, nil, fmt.Errorf("%w: черновик %s", ErrDuplicateDraft, id)
	}
	draft, err := newActionDraft(id, c.id, spec)
	if err != nil {
		return nil, nil, err
	}
	if err := draft.submit(); err != nil {
		return nil, nil, err
	}
	c.drafts[id] = draft
	c.order = append(c.order, id)
	events := c.changeState(CampaignStateActive, at)
	return draft, events, nil
}

// ApproveDraft фиксирует положительное решение по точной версии черновика и
// порождает событие action-approved.
//
// Повтор решения по той же паре (draft_id, draft_version) идемпотентен: второй
// записи и второго события не возникает.
func (c *Campaign) ApproveDraft(params DecisionParams) (OperatorDecision, []DomainEvent, error) {
	return c.commitDecision(params, DecisionApproved, "")
}

// RejectDraft фиксирует отклонение черновика и возвращает его на доработку.
// Отклонение требует причины. Событие action-approved не порождается.
func (c *Campaign) RejectDraft(params DecisionParams) (OperatorDecision, []DomainEvent, error) {
	return c.commitDecision(params, DecisionRejected, "")
}

// ReviseDraft применяет доработку черновика, повышает его версию и возвращает
// на утверждение. Ожидаемая версия защищает от правки по устаревшему чтению;
// прежнее решение к новой версии не относится (инвариант I2).
//
// Запись правки с автором здесь не ведётся: метод сохраняет прежнее поведение
// для служебных путей. Операторская правка идёт через ReviseDraftRecorded.
func (c *Campaign) ReviseDraft(draftID DraftID, expectedVersion int, spec DraftSpec) (*ActionDraft, error) {
	return c.reviseDraft(draftID, expectedVersion, spec, nil)
}

// ReviseDraftRecorded применяет правку оператора и сохраняет отдельную запись
// правки: автор, время, прежний и новый состав. Прежний (сгенерированный)
// состав фиксируется до замены, поэтому остаётся доступен и после правки.
func (c *Campaign) ReviseDraftRecorded(
	draftID DraftID,
	expectedVersion int,
	spec DraftSpec,
	actor ActorID,
	at time.Time,
) (*ActionDraft, error) {
	return c.reviseDraft(draftID, expectedVersion, spec, &revisionRecord{actor: actor, at: at})
}

// revisionRecord — данные правки оператора для записи в историю черновика.
type revisionRecord struct {
	actor ActorID
	at    time.Time
}

// reviseDraft — общий путь доработки. Необязательная запись правки требует
// актора и времени: незаполненная запись не создаётся.
func (c *Campaign) reviseDraft(
	draftID DraftID,
	expectedVersion int,
	spec DraftSpec,
	record *revisionRecord,
) (*ActionDraft, error) {
	if c.state == CampaignStateCompleted {
		return nil, fmt.Errorf("%w: кампания %s", ErrCampaignCompleted, c.id)
	}
	draft, err := c.draft(draftID)
	if err != nil {
		return nil, err
	}
	if err := draft.checkVersion(expectedVersion); err != nil {
		return nil, err
	}
	if record == nil {
		if err := draft.revise(spec); err != nil {
			return nil, err
		}
		return draft, nil
	}
	if err := record.actor.validate(); err != nil {
		return nil, err
	}
	if record.at.IsZero() {
		return nil, fmt.Errorf("%w: черновик %s", ErrDecisionTimeRequired, draftID)
	}
	edit := DraftEdit{
		Actor: record.actor,
		At:    record.at.UTC(),
		From:  draft.Spec(),
		To:    spec,
	}
	if err := draft.reviseRecorded(spec, edit); err != nil {
		return nil, err
	}
	return draft, nil
}

// PostponeDraft откладывает решение по черновику до указанного момента.
// Черновик остаётся «ожидает утверждения» и не исполняется: отложение не
// является решением или автоутверждением (инвариант I1 сохраняется).
func (c *Campaign) PostponeDraft(draftID DraftID, expectedVersion int, until time.Time) error {
	if c.state == CampaignStateCompleted {
		return fmt.Errorf("%w: кампания %s", ErrCampaignCompleted, c.id)
	}
	draft, err := c.draft(draftID)
	if err != nil {
		return err
	}
	if err := draft.checkVersion(expectedVersion); err != nil {
		return err
	}
	return draft.postpone(until)
}

// ExecuteDraft переводит утверждённый черновик в состояние «исполнено».
//
// Это единственный путь к исполнению действия. Без положительного решения по
// точной версии черновика вызов отвергается (инвариант I1), поэтому исполнить
// действие в обход решения оператора невозможно.
func (c *Campaign) ExecuteDraft(draftID DraftID, expectedVersion int) error {
	draft, err := c.executableDraft(draftID, expectedVersion)
	if err != nil {
		return err
	}
	return draft.markExecuted()
}

// FailDraft фиксирует ошибку исполнения утверждённого черновика. Требует
// непустой причины и положительного решения по точной версии.
func (c *Campaign) FailDraft(draftID DraftID, expectedVersion int, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("%w: черновик %s", ErrDecisionReasonRequired, draftID)
	}
	draft, err := c.executableDraft(draftID, expectedVersion)
	if err != nil {
		return err
	}
	return draft.markFailed()
}

// ExpireDraft помечает черновик просроченным по сроку утверждения.
// Автоутверждение запрещено: черновик остаётся неисполненным.
func (c *Campaign) ExpireDraft(draftID DraftID, expectedVersion int) error {
	if c.state == CampaignStateCompleted {
		return fmt.Errorf("%w: кампания %s", ErrCampaignCompleted, c.id)
	}
	draft, err := c.draft(draftID)
	if err != nil {
		return err
	}
	if err := draft.checkVersion(expectedVersion); err != nil {
		return err
	}
	return draft.expire()
}

// Complete завершает кампанию. После завершения кампания не возобновляется:
// постановка и решение по черновикам отвергаются (инвариант I3).
func (c *Campaign) Complete(at time.Time) ([]DomainEvent, error) {
	if c.state == CampaignStateCompleted {
		return nil, fmt.Errorf("%w: кампания %s", ErrCampaignCompleted, c.id)
	}
	return c.changeState(CampaignStateCompleted, at), nil
}

// Draft возвращает черновик по идентификатору.
func (c *Campaign) Draft(id DraftID) (*ActionDraft, bool) {
	d, ok := c.drafts[id]
	return d, ok
}

// Drafts возвращает черновики кампании в порядке постановки.
func (c *Campaign) Drafts() []*ActionDraft {
	out := make([]*ActionDraft, 0, len(c.order))
	for _, id := range c.order {
		out = append(out, c.drafts[id])
	}
	return out
}

// PendingApproval возвращает черновики, ожидающие решения оператора.
func (c *Campaign) PendingApproval() []*ActionDraft {
	var out []*ActionDraft
	for _, id := range c.order {
		if d := c.drafts[id]; d.State() == DraftStatePendingApproval {
			out = append(out, d)
		}
	}
	return out
}

// Decisions возвращает журнал решений кампании в порядке принятия.
func (c *Campaign) Decisions() []OperatorDecision {
	return append([]OperatorDecision(nil), c.decisions...)
}

// DecisionsForDraft возвращает журнал решений по конкретному черновику.
// Журнал — это выборка решений, а не отдельная модель.
func (c *Campaign) DecisionsForDraft(id DraftID) []OperatorDecision {
	var out []OperatorDecision
	for _, d := range c.decisions {
		if d.DraftID() == id {
			out = append(out, d)
		}
	}
	return out
}

// DomainEvents возвращает копию неподтверждённых событий агрегата.
func (c *Campaign) DomainEvents() []DomainEvent {
	return append([]DomainEvent(nil), c.events...)
}

// PullDomainEvents возвращает неподтверждённые события и очищает буфер.
func (c *Campaign) PullDomainEvents() []DomainEvent {
	if len(c.events) == 0 {
		return nil
	}
	out := c.events
	c.events = nil
	return out
}

// commitDecision — общий путь утверждения и отклонения. Проверка версии
// выполняется до любой записи, поэтому решение по устаревшей версии не меняет
// состояние. batchDigest непуст только на пакетном пути и служит признаком
// пакетного принятия; повтор с другим digest конфликтует с прежним решением.
func (c *Campaign) commitDecision(
	params DecisionParams,
	outcome DecisionOutcome,
	batchDigest string,
) (OperatorDecision, []DomainEvent, error) {
	if c.Purpose() == CampaignPurposeEditorial {
		return OperatorDecision{}, nil, ErrEditorialDeliveryForbidden
	}
	if c.state == CampaignStateCompleted {
		return OperatorDecision{}, nil, fmt.Errorf("%w: кампания %s", ErrCampaignCompleted, c.id)
	}
	draft, err := c.draft(params.DraftID)
	if err != nil {
		return OperatorDecision{}, nil, err
	}
	if err := draft.checkVersion(params.DraftVersion); err != nil {
		return OperatorDecision{}, nil, err
	}
	if existing, ok := c.decisionFor(params.DraftID, params.DraftVersion); ok {
		// Идемпотентен только полностью идентичный повтор: тот же исход, тот же
		// автор, та же причина и тот же провенанс пакета. Иначе — конфликт,
		// потому что второе решение (например, другого оператора) обязано
		// попасть в журнал аудита, а по паре (draft_id, draft_version) его место
		// уже занято (spec.md, «Аудит решений оператора»). Состояние при
		// конфликте не меняется.
		if existing.Outcome() != outcome ||
			existing.Actor() != params.Actor ||
			existing.Reason() != params.Reason ||
			existing.BatchDigest() != batchDigest {
			return OperatorDecision{}, nil, fmt.Errorf(
				"%w: черновик %s версии %d",
				ErrDecisionConflict, params.DraftID, params.DraftVersion,
			)
		}
		return existing, nil, nil
	}
	options := []decisionOption{}
	if batchDigest != "" {
		options = append(options, withBatchProvenance(batchDigest))
	}
	decision, err := newOperatorDecision(
		params.DecisionID,
		c.id,
		params.DraftID,
		params.DraftVersion,
		params.Actor,
		outcome,
		params.Reason,
		params.DecidedAt,
		options...,
	)
	if err != nil {
		return OperatorDecision{}, nil, err
	}
	switch outcome {
	case DecisionApproved:
		err = draft.approve()
	case DecisionRejected:
		err = draft.reject()
	}
	if err != nil {
		return OperatorDecision{}, nil, err
	}
	c.decisions = append(c.decisions, decision)

	var events []DomainEvent
	if outcome == DecisionApproved {
		events = append(events, ActionApproved{
			CampaignID:   c.id,
			TenantID:     c.tenantID,
			DraftID:      params.DraftID,
			DraftVersion: draft.Version(),
			DecisionID:   decision.ID(),
			Actor:        decision.Actor(),
			Channel:      draft.Channel(),
			Material:     draft.Spec().Material,
			Recipient:    draft.Spec().Recipient,
			At:           decision.DecidedAt(),
		})
		c.events = append(c.events, events...)
	}
	return decision, events, nil
}

// draft достаёт черновик или возвращает доменную ошибку.
func (c *Campaign) draft(id DraftID) (*ActionDraft, error) {
	d, ok := c.drafts[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrDraftNotFound, id)
	}
	return d, nil
}

// executableDraft проверяет все условия исполнения: кампания не завершена,
// черновик существует, версия совпадает и по ней есть положительное решение.
func (c *Campaign) executableDraft(draftID DraftID, expectedVersion int) (*ActionDraft, error) {
	if c.state == CampaignStateCompleted {
		return nil, fmt.Errorf("%w: кампания %s", ErrCampaignCompleted, c.id)
	}
	draft, err := c.draft(draftID)
	if err != nil {
		return nil, err
	}
	if err := draft.checkVersion(expectedVersion); err != nil {
		return nil, err
	}
	if _, ok := c.approvalFor(draftID, expectedVersion); !ok {
		return nil, fmt.Errorf(
			"%w: черновик %s версии %d",
			ErrApprovalRequired, draftID, expectedVersion,
		)
	}
	if draft.State() != DraftStateApproved {
		return nil, fmt.Errorf(
			"%w: черновик %s в состоянии %q",
			ErrApprovalRequired, draftID, draft.State(),
		)
	}
	return draft, nil
}

// approvalFor ищет положительное решение по точной версии черновика.
func (c *Campaign) approvalFor(id DraftID, version int) (OperatorDecision, bool) {
	d, ok := c.decisionFor(id, version)
	return d, ok && d.IsApproval()
}

// rejectionFor сообщает, есть ли решение об отклонении по точной версии черновика.
func (c *Campaign) rejectionFor(id DraftID, version int) bool {
	d, ok := c.decisionFor(id, version)
	return ok && d.Outcome() == DecisionRejected
}

// decisionFor ищет любое решение по точной версии черновика.
func (c *Campaign) decisionFor(id DraftID, version int) (OperatorDecision, bool) {
	for _, d := range c.decisions {
		if d.DraftID() == id && d.DraftVersion() == version {
			return d, true
		}
	}
	return OperatorDecision{}, false
}

// recordBatchStateChange фиксирует изменение агрегата при пакетном утверждении
// событием campaign-state-changed. Кампания остаётся в прежнем состоянии:
// пакет не вводит нового состояния, но факт изменения состава решений должен
// быть виден подписчикам события. В отличие от changeState, событие эмитится
// без смены состояния.
func (c *Campaign) recordBatchStateChange(at time.Time) DomainEvent {
	event := CampaignStateChanged{
		CampaignID: c.id,
		TenantID:   c.tenantID,
		From:       c.state,
		To:         c.state,
		At:         at,
	}
	c.events = append(c.events, event)
	return event
}

// changeState меняет состояние кампании и порождает campaign-state-changed,
// только если состояние действительно изменилось.
func (c *Campaign) changeState(to CampaignState, at time.Time) []DomainEvent {
	if c.state == to {
		return nil
	}
	from := c.state
	c.state = to
	event := CampaignStateChanged{
		CampaignID: c.id,
		TenantID:   c.tenantID,
		From:       from,
		To:         to,
		At:         at,
	}
	c.events = append(c.events, event)
	return []DomainEvent{event}
}

// Campaign — producer событий action-approved и campaign-state-changed.
var _ EventProducer = (*Campaign)(nil)
