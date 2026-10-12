package domain

import (
	"fmt"
	"strings"
	"time"
)

// TransitionInput — данные перехода статуса: кто, когда и в рамках какого
// сквозного сценария совершил переход.
type TransitionInput struct {
	// Actor — пользователь-оператор, совершивший переход.
	Actor ActorID

	// At — время перехода (UTC).
	At time.Time

	// Reason — причина перехода. Обязательна для отклонения, отмены и сбоя.
	Reason string

	// ReasonCode — машинный код причины перехода. Обязателен для сбоя исполнения
	// (набор: search_failed, draft_failed, handoff_failed, transition_rejected,
	// workflow_timed_out); для прочих переходов пуст.
	ReasonCode string

	// EventID — стабильный идентификатор порождаемого события.
	EventID EventID

	// CorrelationID — идентификатор сквозного сценария.
	CorrelationID CorrelationID

	// CausationID — идентификатор команды или события, вызвавшего переход.
	CausationID CausationID
}

// validate проверяет обязательные части перехода.
func (in TransitionInput) validate() error {
	if strings.TrimSpace(string(in.Actor)) == "" {
		return ErrMissingActor
	}
	if in.At.IsZero() {
		return ErrMissingTime
	}
	if strings.TrimSpace(string(in.EventID)) == "" {
		return fmt.Errorf("%w: %s", ErrMissingEventID, "переход статуса заявки")
	}
	return nil
}

// Transition — зафиксированный переход статуса с временем и актором. Журнал
// переходов ведёт агрегат: каждый переход записывается, а не выводится задним
// числом.
type Transition struct {
	from       RequestStatus
	to         RequestStatus
	actor      ActorID
	at         time.Time
	reason     string
	reasonCode string
}

// From возвращает исходный статус (пустой для первого перехода создания).
func (t Transition) From() RequestStatus { return t.from }

// To возвращает целевой статус.
func (t Transition) To() RequestStatus { return t.to }

// Actor возвращает актора перехода.
func (t Transition) Actor() ActorID { return t.actor }

// At возвращает время перехода.
func (t Transition) At() time.Time { return t.at }

// Reason возвращает причину перехода (может быть пустой).
func (t Transition) Reason() string { return t.reason }

// ReasonCode возвращает машинный код причины перехода (может быть пустым).
func (t Transition) ReasonCode() string { return t.reasonCode }

// NewRequestParams — данные создания заявки.
type NewRequestParams struct {
	ID                    RequestID
	OfferID               OfferID
	TenantID              TenantID
	Content               string
	Target                string
	ExecutionPurpose      string
	SenderCompany         *SenderCompanySnapshot
	ExecutionHistoryKnown bool
	Actor                 ActorID
	At                    time.Time
	EventID               EventID

	CorrelationID CorrelationID
	CausationID   CausationID
}

// Request — aggregate root контекста request-fulfillment.
//
// Агрегат владеет оффером и обеспечивает инварианты:
//
//	I1 — заявка без непустого оффера не создаётся;
//	I2 — заявка привязана ровно к одному тенанту и не переносится между ними;
//	I3 — переход в processing возможен только из validated;
//	I4 — после validated оффер не редактируется на месте: правка создаёт новую
//	     версию, прежняя остаётся неизменной;
//	I5 — fulfilled, rejected, cancelled и failed терминальны;
//	I6 — ссылки на чужие агрегаты хранятся по идентификаторам.
//
// Поля закрыты: состояние меняется только методами агрегата. version — токен
// оптимистичной блокировки: каждое изменение повышает его на единицу, а
// репозиторий сверяет сохранённую версию с ожидаемой.
type Request struct {
	id               RequestID
	tenantID         TenantID
	status           RequestStatus
	executionPurpose string
	senderCompany    *SenderCompanySnapshot

	offer        *Offer
	offerHistory []Offer

	audienceSnapshotID    AudienceSnapshotID
	campaignID            CampaignID
	dealID                DealID
	callID                CallID
	executionHistoryKnown bool

	// validationDecision — решение валидации, зафиксированное при переходе в
	// validated или rejected. Живёт внутри агрегата (change request-validation).
	validationDecision *ValidationDecision

	transitions []Transition
	version     int
	events      []DomainEvent
}

// NewRequest создаёт заявку в статусе submitted. Заявка без непустого оффера не
// создаётся (I1). Создание фиксируется первым переходом и событием
// request-submitted.
func NewRequest(params NewRequestParams) (*Request, error) {
	if err := params.ID.validate(); err != nil {
		return nil, err
	}
	if err := params.TenantID.validate(); err != nil {
		return nil, err
	}
	purpose := normalizeExecutionPurpose(params.ExecutionPurpose)
	if purpose != ExecutionPurposeDelivery && purpose != ExecutionPurposeEditorial {
		return nil, fmt.Errorf("%w: недопустимый purpose %q", ErrInvalidSnapshot, purpose)
	}
	content, err := NewOfferContent(params.Content)
	if err != nil {
		return nil, fmt.Errorf("%w: заявка %s: %v", ErrOfferRequired, params.ID, err)
	}
	offer, err := newOffer(params.OfferID, content, NewRequestTarget(params.Target))
	if err != nil {
		return nil, fmt.Errorf("%w: заявка %s: %v", ErrOfferRequired, params.ID, err)
	}
	input := TransitionInput{
		Actor:         params.Actor,
		At:            params.At,
		EventID:       params.EventID,
		CorrelationID: params.CorrelationID,
		CausationID:   params.CausationID,
	}
	if err := input.validate(); err != nil {
		return nil, err
	}

	request := &Request{
		id:                    params.ID,
		tenantID:              params.TenantID,
		status:                StatusSubmitted,
		executionPurpose:      purpose,
		senderCompany:         cloneSenderCompany(params.SenderCompany),
		offer:                 offer,
		version:               1,
		executionHistoryKnown: params.ExecutionHistoryKnown,
	}
	request.transitions = append(request.transitions, Transition{
		from:  "",
		to:    StatusSubmitted,
		actor: input.Actor,
		at:    input.At,
	})
	event := RequestSubmitted{
		EventHeader:   request.header(input),
		RequestID:     request.id,
		To:            StatusSubmitted,
		Actor:         input.Actor,
		OfferRevision: offer.Revision(),
	}
	request.events = append(request.events, event)
	return request, nil
}

const (
	ExecutionPurposeDelivery  = "delivery"
	ExecutionPurposeEditorial = "editorial"
)

func normalizeExecutionPurpose(purpose string) string {
	if strings.TrimSpace(purpose) == "" {
		return ExecutionPurposeDelivery
	}
	return strings.TrimSpace(purpose)
}

// SenderServiceSnapshot и SenderCompanySnapshot — типы собственного домена;
// контекст request-fulfillment не хранит DTO соседнего published contract.
type SenderServiceSnapshot struct {
	Name, Short, Full string
}

type SenderCompanySnapshot struct {
	CompanyName, Website, Description string
	Services                          []SenderServiceSnapshot
}

func cloneSenderCompany(snapshot *SenderCompanySnapshot) *SenderCompanySnapshot {
	if snapshot == nil {
		return nil
	}
	copy := *snapshot
	copy.Services = append([]SenderServiceSnapshot(nil), snapshot.Services...)
	return &copy
}

// ID возвращает идентификатор заявки.
func (r *Request) ID() RequestID { return r.id }

// TenantID возвращает тенанта-владельца заявки.
func (r *Request) TenantID() TenantID { return r.tenantID }

// Status возвращает текущий статус заявки.
func (r *Request) Status() RequestStatus { return r.status }

// ExecutionHistoryKnown сообщает, что заявка создана текущим контрактом и её
// прежняя execution history известна. Отсутствующий legacy snapshot marker — false.
func (r *Request) ExecutionHistoryKnown() bool { return r.executionHistoryKnown }

// ExecutionPurpose возвращает purpose; старые записи без поля имеют delivery semantics.
func (r *Request) ExecutionPurpose() string { return normalizeExecutionPurpose(r.executionPurpose) }

// SenderCompanySnapshot возвращает независимую копию безопасного снимка отправителя.
func (r *Request) SenderCompanySnapshot() *SenderCompanySnapshot {
	return cloneSenderCompany(r.senderCompany)
}

// SameSubmission сверяет повтор с исходной бизнес-командой. Purpose и snapshot
// входят в ключ сравнения; изменение любого из них не переиспользует заявку.
func (r *Request) SameSubmission(params NewRequestParams) bool {
	if r == nil || r.id != params.ID || r.tenantID != params.TenantID ||
		r.offer.ID() != params.OfferID || r.offer.Content().Text() != params.Content ||
		r.offer.Target().Description() != params.Target ||
		normalizeExecutionPurpose(r.executionPurpose) != normalizeExecutionPurpose(params.ExecutionPurpose) ||
		len(r.transitions) == 0 || r.transitions[0].actor != params.Actor {
		return false
	}
	return senderSnapshotsEqual(r.senderCompany, params.SenderCompany)
}

func senderSnapshotsEqual(left, right *SenderCompanySnapshot) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	if left.CompanyName != right.CompanyName || left.Website != right.Website || left.Description != right.Description || len(left.Services) != len(right.Services) {
		return false
	}
	for i := range left.Services {
		if left.Services[i] != right.Services[i] {
			return false
		}
	}
	return true
}

// Offer возвращает текущую версию оффера.
func (r *Request) Offer() *Offer { return r.offer }

// OfferHistory возвращает прежние версии оффера в порядке появления.
func (r *Request) OfferHistory() []Offer {
	return append([]Offer(nil), r.offerHistory...)
}

// AudienceSnapshotID возвращает ссылку на снимок аудитории (может быть пустой).
func (r *Request) AudienceSnapshotID() AudienceSnapshotID { return r.audienceSnapshotID }

// CampaignID возвращает ссылку на кампанию (может быть пустой).
func (r *Request) CampaignID() CampaignID { return r.campaignID }

// DealID возвращает ссылку на сделку (может быть пустой).
func (r *Request) DealID() DealID { return r.dealID }

// CallID возвращает ссылку на звонок-уточнение (может быть пустой).
func (r *Request) CallID() CallID { return r.callID }

// ValidationDecision возвращает решение валидации и признак его наличия.
func (r *Request) ValidationDecision() (ValidationDecision, bool) {
	if r.validationDecision == nil {
		return ValidationDecision{}, false
	}
	return *r.validationDecision, true
}

// Version возвращает версию агрегата — токен оптимистичной блокировки.
func (r *Request) Version() int { return r.version }

// Transitions возвращает журнал переходов в порядке совершения.
func (r *Request) Transitions() []Transition {
	return append([]Transition(nil), r.transitions...)
}

// DomainEvents возвращает копию неподтверждённых событий агрегата.
func (r *Request) DomainEvents() []DomainEvent {
	return append([]DomainEvent(nil), r.events...)
}

// PullDomainEvents возвращает неподтверждённые события и очищает буфер.
func (r *Request) PullDomainEvents() []DomainEvent {
	if len(r.events) == 0 {
		return nil
	}
	out := r.events
	r.events = nil
	return out
}

// StartValidation переводит заявку из submitted в validating. Отдельного
// события у этого перехода нет: согласованный набор событий заявки начинается
// с request-validated. Повторное применение к заявке в validating — no-op.
func (r *Request) StartValidation(in TransitionInput) ([]DomainEvent, error) {
	if _, _, err := r.applyTransition(StatusValidating, in, false); err != nil {
		return nil, err
	}
	return nil, nil
}

// Validate переводит заявку из validating в validated и фиксирует одобрение.
// Повторное применение к заявке в validated — no-op: решение не создаётся
// повторно, событие не эмитится.
func (r *Request) Validate(in TransitionInput) ([]DomainEvent, error) {
	from, changed, err := r.applyTransition(StatusValidated, in, false)
	if err != nil {
		return nil, err
	}
	if !changed {
		return nil, nil
	}
	decision, err := newValidationDecision(ValidationApproved, in.Actor, in.At, "")
	if err != nil {
		return nil, err
	}
	r.validationDecision = &decision
	return r.emit(RequestValidated{
		EventHeader:   r.header(in),
		RequestID:     r.id,
		From:          from,
		To:            StatusValidated,
		Actor:         in.Actor,
		OfferRevision: r.offer.Revision(),
	}), nil
}

// StartProcessing переводит заявку из validated в processing. Это единственный
// путь запуска исполнения: из других статусов переход отвергается (I3).
// Повторное применение к заявке в processing — no-op.
func (r *Request) StartProcessing(in TransitionInput) ([]DomainEvent, error) {
	from, changed, err := r.applyTransition(StatusProcessing, in, false)
	if err != nil {
		return nil, err
	}
	if !changed {
		return nil, nil
	}
	return r.emit(RequestProcessingStarted{
		EventHeader:   r.header(in),
		RequestID:     r.id,
		From:          from,
		To:            StatusProcessing,
		Actor:         in.Actor,
		OfferRevision: r.offer.Revision(),
	}), nil
}

// Fulfill переводит заявку из processing в fulfilled (терминальный статус).
// Повторное применение к уже fulfilled заявке — no-op.
func (r *Request) Fulfill(in TransitionInput) ([]DomainEvent, error) {
	from, changed, err := r.applyTransition(StatusFulfilled, in, false)
	if err != nil {
		return nil, err
	}
	if !changed {
		return nil, nil
	}
	return r.emit(RequestFulfilled{
		EventHeader:        r.header(in),
		RequestID:          r.id,
		From:               from,
		To:                 StatusFulfilled,
		Actor:              in.Actor,
		AudienceSnapshotID: r.audienceSnapshotID,
		CampaignID:         r.campaignID,
		DealID:             r.dealID,
	}), nil
}

// Reject отклоняет заявку по итогам валидации (validating → rejected) и
// фиксирует решение с причиной. Причина обязательна. Повторное применение к уже
// rejected заявке — no-op: решение не создаётся повторно.
func (r *Request) Reject(in TransitionInput) ([]DomainEvent, error) {
	from, changed, err := r.applyTransition(StatusRejected, in, true)
	if err != nil {
		return nil, err
	}
	if !changed {
		return nil, nil
	}
	decision, err := newValidationDecision(ValidationRejected, in.Actor, in.At, in.Reason)
	if err != nil {
		return nil, err
	}
	r.validationDecision = &decision
	return r.emit(RequestRejected{
		EventHeader: r.header(in),
		RequestID:   r.id,
		From:        from,
		To:          StatusRejected,
		Actor:       in.Actor,
		Reason:      strings.TrimSpace(in.Reason),
	}), nil
}

// Cancel отменяет заявку из любого активного статуса. Причина обязательна.
// Повторное применение к уже cancelled заявке — no-op: повторное решение не
// создаётся.
func (r *Request) Cancel(in TransitionInput) ([]DomainEvent, error) {
	from, changed, err := r.applyTransition(StatusCancelled, in, true)
	if err != nil {
		return nil, err
	}
	if !changed {
		return nil, nil
	}
	return r.emit(RequestCancelled{
		EventHeader: r.header(in),
		RequestID:   r.id,
		From:        from,
		To:          StatusCancelled,
		Actor:       in.Actor,
		Reason:      strings.TrimSpace(in.Reason),
	}), nil
}

// Fail переводит заявку из processing в failed — терминальный исход «исполнение
// необратимо сломалось». Причина (текст) обязательна; машинный код причины
// (ReasonCode) сохраняется вместе с текстом. Повторное применение к уже failed
// заявке — no-op: статус, версия и число событий не меняются.
func (r *Request) Fail(in TransitionInput) ([]DomainEvent, error) {
	from, changed, err := r.applyTransition(StatusFailed, in, true)
	if err != nil {
		return nil, err
	}
	if !changed {
		return nil, nil
	}
	return r.emit(RequestExecutionFailed{
		EventHeader: r.header(in),
		RequestID:   r.id,
		From:        from,
		To:          StatusFailed,
		Actor:       in.Actor,
		ReasonCode:  strings.TrimSpace(in.ReasonCode),
		Reason:      strings.TrimSpace(in.Reason),
	}), nil
}

// Rerun повторно запускает упавшую заявку явным переходом failed → validated.
// Это отдельная операция вне canTransitionTo: failed остаётся терминальным для
// автоматического исполнения, а оператор сознательно возвращает заявку на
// стартовую позицию, после чего стандартный путь validated → processing запускает
// новое исполнение. Повторное применение к уже validated заявке — no-op. Из
// другого статуса переход отвергается.
func (r *Request) Rerun(in TransitionInput) ([]DomainEvent, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	if r.status == StatusValidated {
		return nil, nil
	}
	if r.status != StatusFailed {
		if r.status.IsTerminal() {
			return nil, fmt.Errorf("%w: заявка %s в статусе %s", ErrTerminalState, r.id, r.status)
		}
		return nil, fmt.Errorf("%w: заявка %s: %s → %s", ErrInvalidTransition, r.id, r.status, StatusValidated)
	}
	from := r.status
	r.status = StatusValidated
	r.version++
	r.transitions = append(r.transitions, Transition{
		from:   from,
		to:     StatusValidated,
		actor:  in.Actor,
		at:     in.At,
		reason: strings.TrimSpace(in.Reason),
	})
	return nil, nil
}

// FailureReason возвращает причину сбоя исполнения: машинный код, текст и
// признак того, что заявка действительно упала. Признак истинен только в статусе
// failed; в других статусах код и текст пусты.
func (r *Request) FailureReason() (code string, text string, ok bool) {
	if r.status != StatusFailed {
		return "", "", false
	}
	last := r.transitions[len(r.transitions)-1]
	return last.reasonCode, last.reason, true
}

// failureReasonCodes — согласованный набор машинных кодов причины сбоя исполнения.
// Должен совпадать с кодами, которые задаёт адаптер workflow (fulfillment.go):
// новый код сбоя обязан попасть и туда, и сюда — иначе домен отвергнет переход.
var failureReasonCodes = []string{
	"search_failed",
	"draft_failed",
	"handoff_failed",
	"transition_rejected",
	"workflow_timed_out",
}

// validateFailureReasonCode проверяет, что машинный код причины сбоя непуст и
// входит в согласованный набор. Код — машинный идентификатор причины, поэтому он
// обязателен так же, как текст причины; чужой код отвергается доменной ошибкой.
func validateFailureReasonCode(code string) error {
	trimmed := strings.TrimSpace(code)
	if trimmed == "" {
		return fmt.Errorf("%w: код причины сбоя обязателен", ErrInvalidReasonCode)
	}
	for _, allowed := range failureReasonCodes {
		if trimmed == allowed {
			return nil
		}
	}
	return fmt.Errorf("%w: %q", ErrInvalidReasonCode, trimmed)
}

// ReturnForRevision возвращает заявку клиенту на доработку (validating |
// processing → needs-revision). Причина обязательна: клиент должен знать, что
// исправить. Повторное применение к заявке в needs-revision — no-op.
func (r *Request) ReturnForRevision(in TransitionInput) ([]DomainEvent, error) {
	from, changed, err := r.applyTransition(StatusNeedsRevision, in, true)
	if err != nil {
		return nil, err
	}
	if !changed {
		return nil, nil
	}
	return r.emit(RequestReturnedForRevision{
		EventHeader: r.header(in),
		RequestID:   r.id,
		From:        from,
		To:          StatusNeedsRevision,
		Actor:       in.Actor,
		Reason:      strings.TrimSpace(in.Reason),
	}), nil
}

// Resubmit повторно подаёт доработанную заявку на проверку
// (needs-revision → submitted). Причина не требуется: клиент возвращает заявку в
// очередь валидации. Повторное применение к уже submitted заявке — no-op.
func (r *Request) Resubmit(in TransitionInput) ([]DomainEvent, error) {
	from, changed, err := r.applyTransition(StatusSubmitted, in, false)
	if err != nil {
		return nil, err
	}
	if !changed {
		return nil, nil
	}
	return r.emit(RequestResubmitted{
		EventHeader: r.header(in),
		RequestID:   r.id,
		From:        from,
		To:          StatusSubmitted,
		Actor:       in.Actor,
		Reason:      strings.TrimSpace(in.Reason),
	}), nil
}

// ReviseOffer правит оффер. До validated правка идёт на месте и не создаёт
// новую версию (валидатор уточняет формулировки); после validated создаётся новая
// версия, а прежняя остаётся неизменной (I4). Правка запрещена в терминальных
// статусах. События не эмитятся: правка оффера не меняет статус заявки.
func (r *Request) ReviseOffer(in TransitionInput, newOfferID OfferID, content, target string) (*Offer, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	if r.status.IsTerminal() {
		return nil, fmt.Errorf("%w: заявка %s в статусе %s", ErrTerminalState, r.id, r.status)
	}
	nextContent, err := NewOfferContent(content)
	if err != nil {
		return nil, err
	}
	nextTarget := NewRequestTarget(target)

	if r.status.OfferEditableInPlace() {
		if err := r.offer.edit(nextContent, nextTarget); err != nil {
			return nil, err
		}
		r.version++
		return r.offer, nil
	}

	revised, err := r.offer.newRevision(newOfferID, nextContent, nextTarget)
	if err != nil {
		return nil, err
	}
	r.offerHistory = append(r.offerHistory, *r.offer)
	r.offer = revised
	r.version++
	return revised, nil
}

// AttachCall фиксирует ссылку на звонок-уточнение по идентификатору (I6).
// Разрешена в активных статусах: звонок инициируется на валидации.
func (r *Request) AttachCall(id CallID) error {
	if r.status.IsTerminal() {
		return fmt.Errorf("%w: заявка %s в статусе %s", ErrTerminalState, r.id, r.status)
	}
	if strings.TrimSpace(string(id)) == "" {
		return fmt.Errorf("%w: call_id", ErrCallIDRequired)
	}
	r.callID = id
	r.version++
	return nil
}

// SetOfferEmbedding фиксирует вектор текущего оффера (вход семантического recall,
// capability semantic-lead-search). Правка оффера сбрасывает вектор: он посчитан
// из прежнего текста.
func (r *Request) SetOfferEmbedding(vector []float32) error {
	if err := r.offer.SetEmbedding(vector); err != nil {
		return err
	}
	r.version++
	return nil
}

// RecordAudienceSnapshot фиксирует ссылку на снимок аудитории по идентификатору
// (I6). Запись разрешена в активных статусах.
func (r *Request) RecordAudienceSnapshot(id AudienceSnapshotID) error {
	if r.status.IsTerminal() {
		return fmt.Errorf("%w: заявка %s в статусе %s", ErrTerminalState, r.id, r.status)
	}
	if strings.TrimSpace(string(id)) == "" {
		return fmt.Errorf("%w: audience_snapshot_id", ErrEmptyID)
	}
	r.audienceSnapshotID = id
	r.version++
	return nil
}

// RecordCampaign фиксирует ссылку на кампанию по идентификатору (I6). Повтор с
// тем же идентификатором — идемпотентный no-op: версия не растёт, чтобы повторная
// доставка activity передачи не накапливала версии без изменения состояния.
func (r *Request) RecordCampaign(id CampaignID) error {
	if r.status.IsTerminal() {
		return fmt.Errorf("%w: заявка %s в статусе %s", ErrTerminalState, r.id, r.status)
	}
	if strings.TrimSpace(string(id)) == "" {
		return fmt.Errorf("%w: campaign_id", ErrEmptyID)
	}
	if r.campaignID == id {
		return nil
	}
	r.campaignID = id
	r.version++
	return nil
}

// RecordDeal фиксирует ссылку на сделку по идентификатору (I6).
func (r *Request) RecordDeal(id DealID) error {
	if r.status.IsTerminal() {
		return fmt.Errorf("%w: заявка %s в статусе %s", ErrTerminalState, r.id, r.status)
	}
	if strings.TrimSpace(string(id)) == "" {
		return fmt.Errorf("%w: deal_id", ErrEmptyID)
	}
	r.dealID = id
	r.version++
	return nil
}

// applyTransition проверяет допустимость перехода и фиксирует его. При отказе
// состояние и версия не меняются.
//
// Повторное применение перехода к заявке, уже находящейся в целевом статусе, —
// идемпотентный no-op: успех без приращения версии, без записи перехода и без
// эмиссии события. Это делает повторную доставку activity (потеря ack) безопасной
// и не ослабляет домен: переход в другой статус, как и переход из терминального
// состояния, по-прежнему отвергается. changed сообщает вызывающему, что перехода
// не было, — он не эмитит событие и не создаёт доменное решение повторно.
//
// reasonRequired требует непустую причину для перехода, но только когда переход
// действительно совершается: повтор в уже достигнутый статус остаётся no-op
// независимо от причины.
func (r *Request) applyTransition(to RequestStatus, in TransitionInput, reasonRequired bool) (RequestStatus, bool, error) {
	if err := in.validate(); err != nil {
		return "", false, err
	}
	if err := to.validate(); err != nil {
		return "", false, err
	}
	if r.status == to {
		return r.status, false, nil
	}
	if reasonRequired && strings.TrimSpace(in.Reason) == "" {
		return "", false, ErrReasonRequired
	}
	if to == StatusFailed {
		if err := validateFailureReasonCode(in.ReasonCode); err != nil {
			return "", false, err
		}
	}
	if r.status.IsTerminal() {
		return "", false, fmt.Errorf("%w: заявка %s в статусе %s", ErrTerminalState, r.id, r.status)
	}
	if !r.status.canTransitionTo(to) {
		return "", false, fmt.Errorf("%w: заявка %s: %s → %s", ErrInvalidTransition, r.id, r.status, to)
	}
	from := r.status
	r.status = to
	r.version++
	r.transitions = append(r.transitions, Transition{
		from:       from,
		to:         to,
		actor:      in.Actor,
		at:         in.At,
		reason:     strings.TrimSpace(in.Reason),
		reasonCode: strings.TrimSpace(in.ReasonCode),
	})
	return from, true, nil
}

// header собирает конверт события перехода. Тенант берётся у агрегата: событие
// не может называть чужого тенанта.
func (r *Request) header(in TransitionInput) EventHeader {
	return EventHeader{
		EventID:       in.EventID,
		OccurredAt:    in.At,
		TenantID:      r.tenantID,
		CorrelationID: in.CorrelationID,
		CausationID:   in.CausationID,
	}
}

// emit добавляет событие в буфер агрегата и возвращает его вызывающему.
func (r *Request) emit(event DomainEvent) []DomainEvent {
	r.events = append(r.events, event)
	return []DomainEvent{event}
}

// Request — producer событий жизненного цикла заявки.
var _ EventProducer = (*Request)(nil)
