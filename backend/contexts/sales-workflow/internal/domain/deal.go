package domain

import (
	"fmt"
	"strings"
	"time"
)

// ActionInput — общая часть действий над сделкой: кто, когда и в рамках какого
// сквозного сценария совершил действие.
type ActionInput struct {
	// Actor — пользователь-оператор или внешняя система, совершившая действие.
	Actor ActorID

	// At — время действия (UTC).
	At time.Time

	// Reason — причина действия (может быть пустой).
	Reason string

	// EventID — стабильный идентификатор порождаемого события.
	EventID EventID

	// CorrelationID — идентификатор сквозного сценария.
	CorrelationID CorrelationID

	// CausationID — идентификатор команды или события, вызвавшего действие.
	CausationID CausationID
}

// validate проверяет обязательные части действия.
func (in ActionInput) validate() error {
	if strings.TrimSpace(string(in.Actor)) == "" {
		return ErrMissingActor
	}
	if in.At.IsZero() {
		return ErrMissingTime
	}
	if strings.TrimSpace(string(in.EventID)) == "" {
		return fmt.Errorf("%w: событие сделки", ErrMissingEventID)
	}
	return nil
}

// StageTransition — зафиксированный переход этапа с временем и актором. Журнал
// переходов ведёт агрегат: каждый переход записывается, а не выводится задним
// числом.
type StageTransition struct {
	from   DealStage
	to     DealStage
	actor  ActorID
	at     time.Time
	reason string
}

// From возвращает исходный этап (пустой для перехода создания).
func (t StageTransition) From() DealStage { return t.from }

// To возвращает целевой этап.
func (t StageTransition) To() DealStage { return t.to }

// Actor возвращает актора перехода.
func (t StageTransition) Actor() ActorID { return t.actor }

// At возвращает время перехода.
func (t StageTransition) At() time.Time { return t.at }

// Reason возвращает причину перехода (может быть пустой).
func (t StageTransition) Reason() string { return t.reason }

// NewDealParams — данные создания сделки.
type NewDealParams struct {
	// ID — идентификатор сделки в контексте.
	ID DealID

	// TenantID — тенант-владелец.
	TenantID TenantID

	// CompanyRef — ссылка на карточку компании (владение lead-management).
	CompanyRef CompanyRef

	// External — внешняя версия записи CRM. Нулевое значение означает, что
	// запись CRM ещё не известна: сделка будет синхронизирована событием CRM.
	External ExternalVersion

	Actor         ActorID
	At            time.Time
	EventID       EventID
	CorrelationID CorrelationID
	CausationID   CausationID
}

// Deal — aggregate root deal: сделка внешней CRM.
//
// Агрегат владеет жизненным циклом сделки и подтверждённым результатом и
// обеспечивает инварианты:
//
//	I1 — переходы этапов следуют согласованному workflow (DealStage.CanTransitionTo);
//	I2 — подтверждённый результат имеет достаточное доказательство и
//	     идемпотентную внешнюю ссылку (AttributionProof + политика
//	     confirmed-attribution);
//	I3 — won и lost терминальны: из них переходов нет;
//	I4 — ссылки на чужие агрегаты хранятся по идентификаторам.
//
// Источник истины по сделке — внешняя CRM (storage external). Агрегат не хранит
// себя сам: сохранение отдано порту внешней CRM, а версия агрегата служит токеном
// оптимистичной блокировки.
type Deal struct {
	id          DealID
	tenantID    TenantID
	companyRef  CompanyRef
	stage       DealStage
	external    ExternalVersion
	attribution *AttributionProof

	transitions []StageTransition
	version     int
	events      []DomainEvent
}

// NewDeal создаёт сделку в начальном этапе lead. Создание фиксируется первым
// переходом и событием deal-state-changed.
func NewDeal(params NewDealParams) (*Deal, error) {
	if err := params.ID.validate(); err != nil {
		return nil, err
	}
	if err := params.TenantID.validate(); err != nil {
		return nil, err
	}
	if err := params.CompanyRef.validate(); err != nil {
		return nil, err
	}
	input := ActionInput{
		Actor:         params.Actor,
		At:            params.At,
		EventID:       params.EventID,
		CorrelationID: params.CorrelationID,
		CausationID:   params.CausationID,
	}
	if err := input.validate(); err != nil {
		return nil, err
	}

	deal := &Deal{
		id:         params.ID,
		tenantID:   params.TenantID,
		companyRef: params.CompanyRef,
		stage:      StageLead,
		external:   params.External,
		version:    1,
	}
	deal.transitions = append(deal.transitions, StageTransition{
		from:  "",
		to:    StageLead,
		actor: input.Actor,
		at:    input.At,
	})
	deal.events = append(deal.events, DealStateChanged{
		EventHeader: deal.header(input),
		DealID:      deal.id,
		To:          StageLead,
		Actor:       input.Actor,
	})
	return deal, nil
}

// ID возвращает идентификатор сделки.
func (d *Deal) ID() DealID { return d.id }

// TenantID возвращает тенанта-владельца сделки.
func (d *Deal) TenantID() TenantID { return d.tenantID }

// CompanyRef возвращает ссылку на карточку компании.
func (d *Deal) CompanyRef() CompanyRef { return d.companyRef }

// Stage возвращает текущий этап сделки.
func (d *Deal) Stage() DealStage { return d.stage }

// External возвращает внешнюю версию записи CRM, с которой синхронизирована
// сделка (нулевую, если синхронизации ещё не было).
func (d *Deal) External() ExternalVersion { return d.external }

// Attribution возвращает подтверждённое доказательство и признак его наличия.
func (d *Deal) Attribution() (AttributionProof, bool) {
	if d.attribution == nil {
		return AttributionProof{}, false
	}
	return *d.attribution, true
}

// ResultConfirmed сообщает, подтверждён ли результат сделки.
func (d *Deal) ResultConfirmed() bool { return d.attribution != nil }

// Version возвращает версию агрегата — токен оптимистичной блокировки.
func (d *Deal) Version() int { return d.version }

// Transitions возвращает журнал переходов в порядке совершения.
func (d *Deal) Transitions() []StageTransition {
	return append([]StageTransition(nil), d.transitions...)
}

// DomainEvents возвращает копию неподтверждённых событий агрегата.
func (d *Deal) DomainEvents() []DomainEvent {
	return append([]DomainEvent(nil), d.events...)
}

// PullDomainEvents возвращает неподтверждённые события и очищает буфер.
func (d *Deal) PullDomainEvents() []DomainEvent {
	if len(d.events) == 0 {
		return nil
	}
	out := d.events
	d.events = nil
	return out
}

// ChangeStage переводит сделку на новый этап жизненного цикла. Переход обязан
// следовать согласованному workflow (I1): произвольный этап не принимается, а из
// терминального этапа переходов нет (I3).
func (d *Deal) ChangeStage(in ActionInput, to DealStage) ([]DomainEvent, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	if err := to.validate(); err != nil {
		return nil, err
	}
	return d.applyStage(in, to)
}

// ApplyExternal применяет наблюдаемое внешней CRM состояние сделки. Вызов
// допустим только когда политика conflict-resolution уже признала внешнюю версию
// применимой: агрегат лишь проверяет, что версия действительно новее (или первая)
// и что переход этапа допустим. Локальное состояние не перезаписывается
// устаревшей или конфликтующей версией — это обеспечивает вызывающий слой.
func (d *Deal) ApplyExternal(in ActionInput, external ExternalVersion, to DealStage) ([]DomainEvent, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	if external.IsZero() {
		return nil, fmt.Errorf("%w: пустая внешняя версия", ErrInvalidExternalVersion)
	}
	if err := to.validate(); err != nil {
		return nil, err
	}
	if !d.external.IsZero() && !external.IsNewerThan(d.external) {
		return nil, fmt.Errorf("%w: внешняя версия не новее применённой", ErrInvalidExternalVersion)
	}
	if to == d.stage {
		// Внешняя версия изменилась, но этап тот же: фиксируем версию без события.
		d.external = external
		d.version++
		return nil, nil
	}
	events, err := d.applyStage(in, to)
	if err != nil {
		return nil, err
	}
	d.external = external
	return events, nil
}

// ConfirmAttribution подтверждает результат сделки и связывает его с исходящим
// действием (I2). Результат можно подтвердить только на этапе, допускающем
// атрибуцию; повтор с той же внешней ссылкой идемпотентен и второго результата не
// создаёт.
func (d *Deal) ConfirmAttribution(in ActionInput, proof AttributionProof) ([]DomainEvent, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	if !d.stage.AllowsAttribution() {
		return nil, fmt.Errorf("%w: этап %s", ErrAttributionNotAllowed, d.stage)
	}
	decision, err := ConfirmedAttribution(proof, d.attribution)
	if err != nil {
		return nil, err
	}
	if decision.Outcome == AttributionRepeat {
		return nil, nil
	}
	confirmed := proof
	d.attribution = &confirmed
	d.version++
	return d.emit(ResultAttributed{
		EventHeader: d.header(in),
		DealID:      d.id,
		ActionRef:   proof.ActionRef(),
		EvidenceRef: proof.EvidenceRef(),
		ExternalRef: proof.ExternalRef(),
		ConfirmedAt: proof.ConfirmedAt(),
	}), nil
}

// applyStage выполняет переход этапа, фиксирует его в журнале и эмитит событие
// deal-state-changed.
func (d *Deal) applyStage(in ActionInput, to DealStage) ([]DomainEvent, error) {
	if d.stage.IsTerminal() {
		return nil, fmt.Errorf("%w: этап %s", ErrTerminalDeal, d.stage)
	}
	if !d.stage.CanTransitionTo(to) {
		return nil, fmt.Errorf("%w: %s → %s", ErrInvalidStageTransition, d.stage, to)
	}
	from := d.stage
	d.stage = to
	d.transitions = append(d.transitions, StageTransition{
		from:   from,
		to:     to,
		actor:  in.Actor,
		at:     in.At,
		reason: strings.TrimSpace(in.Reason),
	})
	d.version++
	return d.emit(DealStateChanged{
		EventHeader: d.header(in),
		DealID:      d.id,
		From:        from,
		To:          to,
		Actor:       in.Actor,
		Reason:      strings.TrimSpace(in.Reason),
	}), nil
}

// header собирает конверт события из входа действия и тенанта сделки.
func (d *Deal) header(in ActionInput) EventHeader {
	return EventHeader{
		EventID:       in.EventID,
		OccurredAt:    in.At,
		TenantID:      d.tenantID,
		CorrelationID: in.CorrelationID,
		CausationID:   in.CausationID,
	}
}

// emit записывает события в буфер агрегата и возвращает их вызывающему.
func (d *Deal) emit(events ...DomainEvent) []DomainEvent {
	d.events = append(d.events, events...)
	return events
}
