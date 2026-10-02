package domain

import (
	"fmt"
	"time"
)

// SubscriptionInvoice — aggregate root subscription-billing контекста billing.
//
// Агрегат владеет подпиской, её тарифными лимитами, потреблением за период,
// счетами и зафиксированными результатами биллинга. Обеспечивает инварианты
// (architecture/domain-catalog.yaml, aggregates.subscription-billing):
//
//	I1 — запись об использовании идемпотентна по ключу периода: пара
//	     (period_key, usage_key) уникальна, повтор не расходует лимит дважды;
//	I2 — счёт выставляется в пределах активной подписки и её лимитов;
//	I3 — результат биллинга идемпотентен по ссылке на источник;
//	I4 — подписка привязана ровно к одному тенанту и не переносится между ними.
//
// Поля закрыты: состояние меняется только методами агрегата. version — токен
// оптимистичной блокировки: каждое изменение повышает его на единицу.
type SubscriptionInvoice struct {
	id       SubscriptionID
	tenantID TenantID
	status   SubscriptionStatus
	tariff   Tariff
	period   BillingPeriod

	usage    []UsageEntry
	usageIdx map[string]int

	invoices []Invoice
	results  []BillingResultRecord

	version int
	events  []DomainEvent
}

// NewSubscriptionParams — параметры создания подписки.
type NewSubscriptionParams struct {
	ID       SubscriptionID
	TenantID TenantID
	Tariff   Tariff
	Period   BillingPeriod
}

// NewSubscription создаёт активную подписку на тариф на первый период. Подписка
// без тарифа и периода не создаётся; событий создание не эмитит — счёт за период
// выставляется отдельно.
func NewSubscription(params NewSubscriptionParams) (*SubscriptionInvoice, error) {
	if err := params.ID.validate(); err != nil {
		return nil, err
	}
	if err := params.TenantID.validate(); err != nil {
		return nil, err
	}
	if _, err := NewTariffID(string(params.Tariff.ID())); err != nil {
		return nil, fmt.Errorf("%w: тариф подписки %s", ErrEmptyID, params.ID)
	}
	if params.Period.IsZero() {
		return nil, fmt.Errorf("%w: подписка %s", ErrInvalidPeriod, params.ID)
	}
	return &SubscriptionInvoice{
		id:       params.ID,
		tenantID: params.TenantID,
		status:   StatusActive,
		tariff:   params.Tariff,
		period:   params.Period,
		usageIdx: make(map[string]int),
		version:  1,
	}, nil
}

// ID возвращает идентификатор подписки.
func (s *SubscriptionInvoice) ID() SubscriptionID { return s.id }

// TenantID возвращает тенанта-владельца подписки.
func (s *SubscriptionInvoice) TenantID() TenantID { return s.tenantID }

// Status возвращает статус подписки.
func (s *SubscriptionInvoice) Status() SubscriptionStatus { return s.status }

// IsActive сообщает, что подписка активна.
func (s *SubscriptionInvoice) IsActive() bool { return s.status == StatusActive }

// Tariff возвращает тариф подписки.
func (s *SubscriptionInvoice) Tariff() Tariff { return s.tariff }

// Period возвращает текущий тарифный период.
func (s *SubscriptionInvoice) Period() BillingPeriod { return s.period }

// Version возвращает версию агрегата — токен оптимистичной блокировки.
func (s *SubscriptionInvoice) Version() int { return s.version }

// Usage возвращает записи потребления текущего периода в порядке появления.
func (s *SubscriptionInvoice) Usage() []UsageEntry {
	return append([]UsageEntry(nil), s.usage...)
}

// Invoices возвращает счета подписки в порядке выставления.
func (s *SubscriptionInvoice) Invoices() []Invoice {
	return append([]Invoice(nil), s.invoices...)
}

// BillingResults возвращает зафиксированные результаты биллинга.
func (s *SubscriptionInvoice) BillingResults() []BillingResultRecord {
	return append([]BillingResultRecord(nil), s.results...)
}

// Invoice возвращает счёт по идентификатору и признак его наличия.
func (s *SubscriptionInvoice) Invoice(id InvoiceID) (Invoice, bool) {
	for _, invoice := range s.invoices {
		if invoice.id == id {
			return invoice, true
		}
	}
	return Invoice{}, false
}

// InvoiceForPeriod возвращает счёт за период и признак его наличия.
func (s *SubscriptionInvoice) InvoiceForPeriod(period BillingPeriod) (Invoice, bool) {
	return s.invoiceFor(period)
}

// HasUsage сообщает, что запись с заданным ключом уже учтена в текущем периоде.
func (s *SubscriptionInvoice) HasUsage(usageKey string) bool {
	_, ok := s.usageIdx[usageIdempotencyKey(s.period.Key(), usageKey)]
	return ok
}

// DomainEvents возвращает копию неподтверждённых событий агрегата.
func (s *SubscriptionInvoice) DomainEvents() []DomainEvent {
	return append([]DomainEvent(nil), s.events...)
}

// PullDomainEvents возвращает неподтверждённые события и очищает буфер.
func (s *SubscriptionInvoice) PullDomainEvents() []DomainEvent {
	if len(s.events) == 0 {
		return nil
	}
	out := s.events
	s.events = nil
	return out
}

// RecordUsage фиксирует потребление возможности за текущий период.
//
// Инвариант I1: повтор с тем же ключом распознаётся как дубликат и не меняет
// состояние. Запись вне тарифа отвергается (ErrFeatureNotInTariff), превышение
// лимита — ErrTariffLimitExceeded, приостановленная подписка потребление не
// принимает. Событий фиксация не эмитит: счёт выставляет IssueInvoice.
func (s *SubscriptionInvoice) RecordUsage(in UsageInput) (UsageResult, error) {
	if err := in.validate(); err != nil {
		return UsageResult{}, err
	}
	key := usageIdempotencyKey(s.period.Key(), in.UsageKey)
	if idx, ok := s.usageIdx[key]; ok {
		entry := s.usage[idx]
		limit, _ := s.tariff.limit(entry.feature)
		return UsageResult{
			Duplicate: true,
			Feature:   entry.feature,
			Quantity:  entry.quantity,
			Limit:     limit.Limit(),
			Remaining: limit.Remaining(),
		}, nil
	}
	if s.status != StatusActive {
		return UsageResult{}, fmt.Errorf("%w: подписка %s", ErrSubscriptionNotActive, s.id)
	}
	limit, ok := s.tariff.limit(in.Feature)
	if !ok {
		return UsageResult{}, fmt.Errorf("%w: возможность %q", ErrFeatureNotInTariff, in.Feature)
	}
	if limit.Exceeds(in.Quantity) {
		return UsageResult{}, fmt.Errorf(
			"%w: возможность %q: остаток %d, запрошено %d",
			ErrTariffLimitExceeded, in.Feature, limit.Remaining(), in.Quantity,
		)
	}
	s.replaceLimit(limit.withUsage(in.Quantity))
	s.usage = append(s.usage, UsageEntry{
		id:        in.UsageID,
		feature:   in.Feature,
		quantity:  in.Quantity,
		periodKey: s.period.Key(),
		usageKey:  in.UsageKey,
		at:        in.At,
	})
	s.usageIdx[key] = len(s.usage) - 1
	s.version++

	updated, _ := s.tariff.limit(in.Feature)
	return UsageResult{
		Duplicate: false,
		Feature:   in.Feature,
		Quantity:  in.Quantity,
		Limit:     updated.Limit(),
		Remaining: updated.Remaining(),
	}, nil
}

// InvoicingInput — вход выставления счёта.
type InvoicingInput struct {
	Period        BillingPeriod
	At            time.Time
	EventID       EventID
	CorrelationID CorrelationID
	CausationID   CausationID
}

// IssueInvoice выставляет счёт за период в пределах активной подписки и её
// лимитов (инвариант I2). Повторное выставление за тот же период идемпотентно:
// возвращается существующий счёт без нового события. Успешное выставление эмитит
// invoice-issued.
func (s *SubscriptionInvoice) IssueInvoice(in InvoicingInput) (Invoice, []DomainEvent, error) {
	if in.At.IsZero() {
		return Invoice{}, nil, ErrMissingTime
	}
	if in.Period.IsZero() {
		return Invoice{}, nil, fmt.Errorf("%w: подписка %s", ErrInvalidPeriod, s.id)
	}
	if existing, ok := s.invoiceFor(in.Period); ok {
		return existing, nil, nil
	}
	if s.status != StatusActive {
		return Invoice{}, nil, fmt.Errorf("%w: подписка %s", ErrSubscriptionNotActive, s.id)
	}
	for _, limit := range s.tariff.Limits() {
		if limit.Used() > limit.Limit() {
			return Invoice{}, nil, fmt.Errorf(
				"%w: возможность %q: потреблено %d, предел %d",
				ErrTariffLimitExceeded, limit.Feature(), limit.Used(), limit.Limit(),
			)
		}
	}
	header := s.header(in.EventID, in.At, in.CorrelationID, in.CausationID)
	if err := header.validate(); err != nil {
		return Invoice{}, nil, err
	}

	invoice := Invoice{
		id:       s.invoiceID(in.Period),
		period:   in.Period,
		amount:   s.tariff.Price(),
		issuedAt: in.At,
	}
	s.invoices = append(s.invoices, invoice)
	s.version++

	event := InvoiceIssued{
		EventHeader:    header,
		SubscriptionID: s.id,
		InvoiceID:      invoice.id,
		Period:         invoice.period,
		Amount:         invoice.amount,
	}
	return invoice, s.emit(event), nil
}

// BillingResultInput — вход фиксации результата биллинга.
type BillingResultInput struct {
	ResultRef     ResultRef
	Kind          BillingResultKind
	Amount        Money
	At            time.Time
	EventID       EventID
	CorrelationID CorrelationID
	CausationID   CausationID
}

// RecordBillingResult фиксирует результат биллинга в подписке (инвариант I3:
// идемпотентно по ссылке на источник). Успешная фиксация эмитит
// billing-result-recorded.
func (s *SubscriptionInvoice) RecordBillingResult(in BillingResultInput) ([]DomainEvent, error) {
	if err := in.ResultRef.validate(); err != nil {
		return nil, err
	}
	if !in.Kind.isValid() {
		return nil, fmt.Errorf("%w: %q", ErrUnknownBillingResultKind, in.Kind)
	}
	if in.At.IsZero() {
		return nil, ErrMissingTime
	}
	if in.Amount.Currency() == "" {
		return nil, ErrInvalidCurrency
	}
	if s.hasResult(in.ResultRef) {
		return nil, nil
	}
	header := s.header(in.EventID, in.At, in.CorrelationID, in.CausationID)
	if err := header.validate(); err != nil {
		return nil, err
	}
	s.results = append(s.results, BillingResultRecord{
		resultRef: in.ResultRef,
		kind:      in.Kind,
		amount:    in.Amount,
		at:        in.At,
	})
	s.version++

	event := BillingResultRecorded{
		EventHeader:    header,
		SubscriptionID: s.id,
		ResultRef:      in.ResultRef,
		Kind:           in.Kind,
		Amount:         in.Amount,
	}
	return s.emit(event), nil
}

// Suspend приостанавливает доступ к функциям, сохраняя данные клиента (BRD,
// приостановка и возобновление). Событий не эмитит.
func (s *SubscriptionInvoice) Suspend(at time.Time) error {
	if at.IsZero() {
		return ErrMissingTime
	}
	if s.status == StatusSuspended {
		return fmt.Errorf("%w: подписка %s уже приостановлена", ErrStatusUnchanged, s.id)
	}
	s.status = StatusSuspended
	s.version++
	return nil
}

// Resume возобновляет доступ в полном объёме тарифа после оплаты.
func (s *SubscriptionInvoice) Resume(at time.Time) error {
	if at.IsZero() {
		return ErrMissingTime
	}
	if s.status == StatusActive {
		return fmt.Errorf("%w: подписка %s уже активна", ErrStatusUnchanged, s.id)
	}
	s.status = StatusActive
	s.version++
	return nil
}

// RenewPeriod открывает следующий тарифный период: потребление нового периода
// начинается с нуля. Период должен начинаться позже текущего; подписка активна.
func (s *SubscriptionInvoice) RenewPeriod(period BillingPeriod) error {
	if period.IsZero() {
		return fmt.Errorf("%w: подписка %s", ErrInvalidPeriod, s.id)
	}
	if s.status != StatusActive {
		return fmt.Errorf("%w: подписка %s", ErrSubscriptionNotActive, s.id)
	}
	if !period.Start().After(s.period.Start()) {
		return fmt.Errorf("%w: новый период не позже текущего", ErrInvalidPeriod)
	}
	s.period = period
	s.usage = nil
	s.usageIdx = make(map[string]int)
	limits := s.tariff.Limits()
	for i := range limits {
		limits[i] = limits[i].resetUsage()
	}
	s.tariff = s.tariff.withLimits(limits)
	s.version++
	return nil
}

// MarkInvoicePaid отмечает счёт оплаченным. Повторная отметка идемпотентна.
func (s *SubscriptionInvoice) MarkInvoicePaid(invoiceID InvoiceID, at time.Time, reference string) (Invoice, error) {
	if at.IsZero() {
		return Invoice{}, ErrMissingTime
	}
	for i := range s.invoices {
		if s.invoices[i].id != invoiceID {
			continue
		}
		if s.invoices[i].Paid() {
			return s.invoices[i], nil
		}
		s.invoices[i] = s.invoices[i].withPayment(at, reference)
		s.version++
		return s.invoices[i], nil
	}
	return Invoice{}, fmt.Errorf("%w: %s", ErrInvoiceNotFound, invoiceID)
}

// replaceLimit заменяет лимит возможности на обновлённый.
func (s *SubscriptionInvoice) replaceLimit(updated TariffLimit) {
	limits := s.tariff.Limits()
	for i := range limits {
		if limits[i].feature == updated.feature {
			limits[i] = updated
			break
		}
	}
	s.tariff = s.tariff.withLimits(limits)
}

// invoiceFor возвращает счёт за период.
func (s *SubscriptionInvoice) invoiceFor(period BillingPeriod) (Invoice, bool) {
	for _, invoice := range s.invoices {
		if invoice.period.Equal(period) {
			return invoice, true
		}
	}
	return Invoice{}, false
}

// hasResult сообщает, что результат с такой ссылкой уже зафиксирован.
func (s *SubscriptionInvoice) hasResult(ref ResultRef) bool {
	for _, record := range s.results {
		if record.resultRef == ref {
			return true
		}
	}
	return false
}

// invoiceID возвращает детерминированный идентификатор счёта: подписка и период.
// Детерминированность делает повторное выставление идемпотентным.
func (s *SubscriptionInvoice) invoiceID(period BillingPeriod) InvoiceID {
	return InvoiceID(s.id.String() + "@" + period.Key())
}

// header собирает конверт события. Тенант берётся у агрегата: событие не может
// называть чужого тенанта.
func (s *SubscriptionInvoice) header(eventID EventID, at time.Time, correlation CorrelationID, causation CausationID) EventHeader {
	return EventHeader{
		EventID:       eventID,
		OccurredAt:    at,
		TenantID:      s.tenantID,
		CorrelationID: correlation,
		CausationID:   causation,
	}
}

// emit добавляет событие в буфер агрегата и возвращает его вызывающему.
func (s *SubscriptionInvoice) emit(event DomainEvent) []DomainEvent {
	s.events = append(s.events, event)
	return []DomainEvent{event}
}

// SubscriptionInvoice — producer событий подписки.
var _ EventProducer = (*SubscriptionInvoice)(nil)
