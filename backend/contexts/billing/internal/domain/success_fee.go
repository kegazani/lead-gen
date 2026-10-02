package domain

import (
	"fmt"
	"time"
)

// FeeStatus — статус вознаграждения за результат.
//
// Статусная модель: calculated ⇄ disputed → excluded. Начисление создаётся из
// подтверждённого результата (calculated); спор приостанавливает списание
// (disputed); исключение снимает сделку из расчёта (excluded). Спор разрешим:
// disputed → calculated.
type FeeStatus string

const (
	// FeeCalculated — вознаграждение начислено.
	FeeCalculated FeeStatus = "calculated"

	// FeeDisputed — начисление оспорено клиентом; списание приостановлено.
	FeeDisputed FeeStatus = "disputed"

	// FeeExcluded — сделка исключена из расчёта.
	FeeExcluded FeeStatus = "excluded"
)

// FeeStatuses возвращает согласованный набор статусов вознаграждения.
func FeeStatuses() []FeeStatus {
	return []FeeStatus{FeeCalculated, FeeDisputed, FeeExcluded}
}

// isValid сообщает, что статус объявлен доменом.
func (s FeeStatus) isValid() bool {
	return s == FeeCalculated || s == FeeDisputed || s == FeeExcluded
}

// validate отвергает статус вне согласованного набора.
func (s FeeStatus) validate() error {
	if !s.isValid() {
		return fmt.Errorf("%w: %q", ErrUnknownFeeStatus, s)
	}
	return nil
}

// String возвращает статус.
func (s FeeStatus) String() string { return string(s) }

// SuccessFee — aggregate root success-fee контекста billing.
//
// Вознаграждение создаётся только из подтверждённого результата, а расчёт хранит
// версию применённого правила (инварианты aggregates.success-fee). Ссылки на
// сделку и результат хранятся по идентификаторам: агрегат не владеет чужыми
// данными. Поля закрыты; version — токен оптимистичной блокировки.
type SuccessFee struct {
	id             SuccessFeeID
	tenantID       TenantID
	subscriptionID SubscriptionID
	resultRef      ResultRef
	dealID         DealID
	base           Money
	rateBps        int64
	amount         Money
	calculation    CalculationVersion
	status         FeeStatus

	version int
	events  []DomainEvent
}

// NewSuccessFeeParams — параметры создания вознаграждения.
type NewSuccessFeeParams struct {
	ID             SuccessFeeID
	TenantID       TenantID
	SubscriptionID SubscriptionID
	ResultRef      ResultRef
	DealID         DealID
	Base           Money
	RateBps        int64
	Calculation    CalculationVersion
	// Confirmed — результат подтверждён. Неподтверждённый результат вознаграждения
	// не создаёт.
	Confirmed     bool
	At            time.Time
	EventID       EventID
	CorrelationID CorrelationID
	CausationID   CausationID
}

// NewSuccessFee создаёт начисленное вознаграждение из подтверждённого результата и
// эмитит success-fee-calculated. Расчёт без версии правила и без ссылки на
// результат не создаётся.
func NewSuccessFee(params NewSuccessFeeParams) (*SuccessFee, error) {
	if err := params.ID.validate(); err != nil {
		return nil, err
	}
	if err := params.TenantID.validate(); err != nil {
		return nil, err
	}
	if err := params.SubscriptionID.validate(); err != nil {
		return nil, err
	}
	if err := params.ResultRef.validate(); err != nil {
		return nil, err
	}
	if err := validateID("deal_id", string(params.DealID)); err != nil {
		return nil, err
	}
	if params.Calculation.IsZero() {
		return nil, ErrEmptyCalculationVersion
	}
	if params.RateBps < 0 {
		return nil, fmt.Errorf("%w: %d", ErrInvalidRate, params.RateBps)
	}
	if params.Base.Currency() == "" {
		return nil, ErrInvalidCurrency
	}
	if !params.Confirmed {
		return nil, fmt.Errorf("%w: результат %s", ErrUnconfirmedResult, params.ResultRef)
	}
	if params.At.IsZero() {
		return nil, ErrMissingTime
	}
	header := EventHeader{
		EventID:       params.EventID,
		OccurredAt:    params.At,
		TenantID:      params.TenantID,
		CorrelationID: params.CorrelationID,
		CausationID:   params.CausationID,
	}
	if err := header.validate(); err != nil {
		return nil, err
	}
	amount, err := params.Base.MultiplyByBps(params.RateBps)
	if err != nil {
		return nil, err
	}

	fee := &SuccessFee{
		id:             params.ID,
		tenantID:       params.TenantID,
		subscriptionID: params.SubscriptionID,
		resultRef:      params.ResultRef,
		dealID:         params.DealID,
		base:           params.Base,
		rateBps:        params.RateBps,
		amount:         amount,
		calculation:    params.Calculation,
		status:         FeeCalculated,
		version:        1,
	}
	event := SuccessFeeCalculated{
		EventHeader:    header,
		SuccessFeeID:   fee.id,
		SubscriptionID: fee.subscriptionID,
		ResultRef:      fee.resultRef,
		DealID:         fee.dealID,
		Base:           fee.base,
		Amount:         fee.amount,
		RateBps:        fee.rateBps,
		Calculation:    fee.calculation,
	}
	fee.events = append(fee.events, event)
	return fee, nil
}

// ID возвращает идентификатор вознаграждения.
func (f *SuccessFee) ID() SuccessFeeID { return f.id }

// TenantID возвращает тенанта-владельца.
func (f *SuccessFee) TenantID() TenantID { return f.tenantID }

// SubscriptionID возвращает ссылку на подписку.
func (f *SuccessFee) SubscriptionID() SubscriptionID { return f.subscriptionID }

// ResultRef возвращает ссылку на подтверждённый результат.
func (f *SuccessFee) ResultRef() ResultRef { return f.resultRef }

// DealID возвращает ссылку на сделку.
func (f *SuccessFee) DealID() DealID { return f.dealID }

// Base возвращает базу расчёта (сумму сделки).
func (f *SuccessFee) Base() Money { return f.base }

// RateBps возвращает ставку расчёта в базисных пунктах.
func (f *SuccessFee) RateBps() int64 { return f.rateBps }

// Amount возвращает сумму вознаграждения.
func (f *SuccessFee) Amount() Money { return f.amount }

// Calculation возвращает версию применённого правила расчёта.
func (f *SuccessFee) Calculation() CalculationVersion { return f.calculation }

// Status возвращает статус вознаграждения.
func (f *SuccessFee) Status() FeeStatus { return f.status }

// Version возвращает версию агрегата.
func (f *SuccessFee) Version() int { return f.version }

// DomainEvents возвращает копию неподтверждённых событий агрегата.
func (f *SuccessFee) DomainEvents() []DomainEvent {
	return append([]DomainEvent(nil), f.events...)
}

// PullDomainEvents возвращает неподтверждённые события и очищает буфер.
func (f *SuccessFee) PullDomainEvents() []DomainEvent {
	if len(f.events) == 0 {
		return nil
	}
	out := f.events
	f.events = nil
	return out
}

// Dispute фиксирует спор клиента и приостанавливает списание (BRD, порядок
// начисления и оспаривания). Оспорить можно начисленное вознаграждение.
func (f *SuccessFee) Dispute(at time.Time) error {
	if at.IsZero() {
		return ErrMissingTime
	}
	if f.status != FeeCalculated {
		return fmt.Errorf("%w: %s → disputed", ErrInvalidFeeTransition, f.status)
	}
	f.status = FeeDisputed
	f.version++
	return nil
}

// Resolve снимает спор: списание возобновляется.
func (f *SuccessFee) Resolve(at time.Time) error {
	if at.IsZero() {
		return ErrMissingTime
	}
	if f.status != FeeDisputed {
		return fmt.Errorf("%w: %s → calculated", ErrInvalidFeeTransition, f.status)
	}
	f.status = FeeCalculated
	f.version++
	return nil
}

// Exclude исключает сделку из расчёта (например, связь с коммуникацией не
// подтверждена). Начисленное или оспоренное вознаграждение исключается.
func (f *SuccessFee) Exclude(at time.Time) error {
	if at.IsZero() {
		return ErrMissingTime
	}
	if f.status == FeeExcluded {
		return fmt.Errorf("%w: %s → excluded", ErrInvalidFeeTransition, f.status)
	}
	f.status = FeeExcluded
	f.version++
	return nil
}

// SuccessFee — producer событий вознаграждения.
var _ EventProducer = (*SuccessFee)(nil)
