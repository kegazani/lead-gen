package domain

import (
	"fmt"
	"time"
)

// Сохранённое состояние агрегатов billing.
//
// Адаптер репозитория не имеет доступа к закрытым полям агрегатов, поэтому он
// читает и пишет их снимки с экспортированными полями. Доменный объект из снимка
// собирается только функциями Rehydrate*, которые проверяют инварианты и не
// эмитят события: восстановление не является новым фактом домена.

// UsageEntrySnapshot — сохранённая запись потребления.
type UsageEntrySnapshot struct {
	ID        UsageID
	Feature   Feature
	Quantity  int64
	PeriodKey string
	UsageKey  string
	At        time.Time
}

// InvoiceSnapshot — сохранённый счёт.
type InvoiceSnapshot struct {
	ID         InvoiceID
	Period     BillingPeriod
	Amount     Money
	IssuedAt   time.Time
	PaidAt     time.Time
	PaymentRef string
}

// BillingResultSnapshot — сохранённый результат биллинга.
type BillingResultSnapshot struct {
	ResultRef ResultRef
	Kind      BillingResultKind
	Amount    Money
	At        time.Time
}

// SubscriptionSnapshot — полное сохранённое состояние aggregate root
// subscription-invoice.
type SubscriptionSnapshot struct {
	ID       SubscriptionID
	TenantID TenantID
	Status   SubscriptionStatus
	TariffID TariffID
	Price    Money
	Limits   []TariffLimit
	Period   BillingPeriod
	Usage    []UsageEntrySnapshot
	Invoices []InvoiceSnapshot
	Results  []BillingResultSnapshot
	Version  int
}

// Snapshot возвращает состояние подписки для сохранения.
func (s *SubscriptionInvoice) Snapshot() SubscriptionSnapshot {
	usage := make([]UsageEntrySnapshot, 0, len(s.usage))
	for _, entry := range s.usage {
		usage = append(usage, UsageEntrySnapshot{
			ID:        entry.id,
			Feature:   entry.feature,
			Quantity:  entry.quantity,
			PeriodKey: entry.periodKey,
			UsageKey:  entry.usageKey,
			At:        entry.at,
		})
	}
	invoices := make([]InvoiceSnapshot, 0, len(s.invoices))
	for _, invoice := range s.invoices {
		invoices = append(invoices, InvoiceSnapshot{
			ID:         invoice.id,
			Period:     invoice.period,
			Amount:     invoice.amount,
			IssuedAt:   invoice.issuedAt,
			PaidAt:     invoice.paidAt,
			PaymentRef: invoice.paymentRef,
		})
	}
	results := make([]BillingResultSnapshot, 0, len(s.results))
	for _, record := range s.results {
		results = append(results, BillingResultSnapshot{
			ResultRef: record.resultRef,
			Kind:      record.kind,
			Amount:    record.amount,
			At:        record.at,
		})
	}
	return SubscriptionSnapshot{
		ID:       s.id,
		TenantID: s.tenantID,
		Status:   s.status,
		TariffID: s.tariff.id,
		Price:    s.tariff.price,
		Limits:   s.tariff.Limits(),
		Period:   s.period,
		Usage:    usage,
		Invoices: invoices,
		Results:  results,
		Version:  s.version,
	}
}

// RehydrateSubscription восстанавливает aggregate root subscription-invoice из
// сохранённого состояния, проверяя инварианты: известность статуса, положительную
// версию, корректный период и тариф, согласованность потребления с лимитами,
// уникальность ключей идемпотентности и корректность счетов. События не эмитятся.
func RehydrateSubscription(snapshot SubscriptionSnapshot) (*SubscriptionInvoice, error) {
	if err := snapshot.ID.validate(); err != nil {
		return nil, err
	}
	if err := snapshot.TenantID.validate(); err != nil {
		return nil, err
	}
	if !snapshot.Status.isValid() {
		return nil, fmt.Errorf("%w: неизвестный статус %q у подписки %s", ErrInvalidSnapshot, snapshot.Status, snapshot.ID)
	}
	if snapshot.Version <= 0 {
		return nil, fmt.Errorf("%w: подписка %s версии %d", ErrInvalidSnapshot, snapshot.ID, snapshot.Version)
	}
	if snapshot.Period.IsZero() {
		return nil, fmt.Errorf("%w: подписка %s без периода", ErrInvalidSnapshot, snapshot.ID)
	}
	tariff, err := NewTariff(snapshot.TariffID, snapshot.Price, snapshot.Limits)
	if err != nil {
		return nil, fmt.Errorf("%w: тариф подписки %s: %v", ErrInvalidSnapshot, snapshot.ID, err)
	}

	usage := make([]UsageEntry, 0, len(snapshot.Usage))
	usageIdx := make(map[string]int, len(snapshot.Usage))
	usedByFeature := make(map[Feature]int64)
	periodKey := snapshot.Period.Key()
	for _, entry := range snapshot.Usage {
		if err := entry.ID.validate(); err != nil {
			return nil, fmt.Errorf("%w: подписка %s: %v", ErrInvalidSnapshot, snapshot.ID, err)
		}
		if entry.Feature.IsZero() {
			return nil, fmt.Errorf("%w: подписка %s: запись без возможности", ErrInvalidSnapshot, snapshot.ID)
		}
		if !tariff.Allows(entry.Feature) {
			return nil, fmt.Errorf("%w: подписка %s: запись по возможности %q вне тарифа", ErrInvalidSnapshot, snapshot.ID, entry.Feature)
		}
		if entry.Quantity <= 0 {
			return nil, fmt.Errorf("%w: подписка %s: запись с объёмом %d", ErrInvalidSnapshot, snapshot.ID, entry.Quantity)
		}
		if entry.PeriodKey != periodKey {
			return nil, fmt.Errorf("%w: подписка %s: запись периода %q не совпадает с текущим %q", ErrInvalidSnapshot, snapshot.ID, entry.PeriodKey, periodKey)
		}
		if entry.At.IsZero() {
			return nil, fmt.Errorf("%w: подписка %s: запись без времени", ErrInvalidSnapshot, snapshot.ID)
		}
		key := usageIdempotencyKey(entry.PeriodKey, entry.UsageKey)
		if _, ok := usageIdx[key]; ok {
			return nil, fmt.Errorf("%w: подписка %s: повтор ключа идемпотентности %q", ErrInvalidSnapshot, snapshot.ID, key)
		}
		usageIdx[key] = len(usage)
		usage = append(usage, UsageEntry{
			id:        entry.ID,
			feature:   entry.Feature,
			quantity:  entry.Quantity,
			periodKey: entry.PeriodKey,
			usageKey:  entry.UsageKey,
			at:        entry.At,
		})
		usedByFeature[entry.Feature] += entry.Quantity
	}
	for _, limit := range tariff.Limits() {
		if usedByFeature[limit.Feature()] != limit.Used() {
			return nil, fmt.Errorf(
				"%w: подписка %s: потребление %q (%d) не совпадает с лимитом (%d)",
				ErrInvalidSnapshot, snapshot.ID, limit.Feature(), usedByFeature[limit.Feature()], limit.Used(),
			)
		}
	}

	invoices := make([]Invoice, 0, len(snapshot.Invoices))
	seenPeriods := make(map[string]struct{}, len(snapshot.Invoices))
	for _, invoice := range snapshot.Invoices {
		if err := invoice.ID.validate(); err != nil {
			return nil, fmt.Errorf("%w: подписка %s: %v", ErrInvalidSnapshot, snapshot.ID, err)
		}
		if invoice.Period.IsZero() {
			return nil, fmt.Errorf("%w: подписка %s: счёт %s без периода", ErrInvalidSnapshot, snapshot.ID, invoice.ID)
		}
		if _, ok := seenPeriods[invoice.Period.Key()]; ok {
			return nil, fmt.Errorf("%w: подписка %s: два счёта за период %s", ErrInvalidSnapshot, snapshot.ID, invoice.Period.Key())
		}
		seenPeriods[invoice.Period.Key()] = struct{}{}
		if invoice.IssuedAt.IsZero() {
			return nil, fmt.Errorf("%w: подписка %s: счёт %s без времени", ErrInvalidSnapshot, snapshot.ID, invoice.ID)
		}
		if !invoice.PaidAt.IsZero() && invoice.PaidAt.Before(invoice.IssuedAt) {
			return nil, fmt.Errorf("%w: подписка %s: счёт %s оплачен раньше выставления", ErrInvalidSnapshot, snapshot.ID, invoice.ID)
		}
		invoices = append(invoices, Invoice{
			id:         invoice.ID,
			period:     invoice.Period,
			amount:     invoice.Amount,
			issuedAt:   invoice.IssuedAt,
			paidAt:     invoice.PaidAt,
			paymentRef: invoice.PaymentRef,
		})
	}

	results := make([]BillingResultRecord, 0, len(snapshot.Results))
	seenResults := make(map[ResultRef]struct{}, len(snapshot.Results))
	for _, record := range snapshot.Results {
		if err := record.ResultRef.validate(); err != nil {
			return nil, fmt.Errorf("%w: подписка %s: %v", ErrInvalidSnapshot, snapshot.ID, err)
		}
		if !record.Kind.isValid() {
			return nil, fmt.Errorf("%w: подписка %s: результат %s: вид %q", ErrInvalidSnapshot, snapshot.ID, record.ResultRef, record.Kind)
		}
		if _, ok := seenResults[record.ResultRef]; ok {
			return nil, fmt.Errorf("%w: подписка %s: повтор результата %s", ErrInvalidSnapshot, snapshot.ID, record.ResultRef)
		}
		seenResults[record.ResultRef] = struct{}{}
		results = append(results, BillingResultRecord{
			resultRef: record.ResultRef,
			kind:      record.Kind,
			amount:    record.Amount,
			at:        record.At,
		})
	}

	return &SubscriptionInvoice{
		id:       snapshot.ID,
		tenantID: snapshot.TenantID,
		status:   snapshot.Status,
		tariff:   tariff,
		period:   snapshot.Period,
		usage:    usage,
		usageIdx: usageIdx,
		invoices: invoices,
		results:  results,
		version:  snapshot.Version,
	}, nil
}

// SuccessFeeSnapshot — полное сохранённое состояние aggregate root success-fee.
type SuccessFeeSnapshot struct {
	ID             SuccessFeeID
	TenantID       TenantID
	SubscriptionID SubscriptionID
	ResultRef      ResultRef
	DealID         DealID
	Base           Money
	RateBps        int64
	Amount         Money
	Calculation    CalculationVersion
	Status         FeeStatus
	Version        int
}

// Snapshot возвращает состояние вознаграждения для сохранения.
func (f *SuccessFee) Snapshot() SuccessFeeSnapshot {
	return SuccessFeeSnapshot{
		ID:             f.id,
		TenantID:       f.tenantID,
		SubscriptionID: f.subscriptionID,
		ResultRef:      f.resultRef,
		DealID:         f.dealID,
		Base:           f.base,
		RateBps:        f.rateBps,
		Amount:         f.amount,
		Calculation:    f.calculation,
		Status:         f.status,
		Version:        f.version,
	}
}

// RehydrateSuccessFee восстанавливает aggregate root success-fee из сохранённого
// состояния, проверяя инварианты: известность статуса, положительную версию,
// непустые ссылки и версию правила, а также согласованность суммы со ставкой.
// События не эмитятся.
func RehydrateSuccessFee(snapshot SuccessFeeSnapshot) (*SuccessFee, error) {
	if err := snapshot.ID.validate(); err != nil {
		return nil, err
	}
	if err := snapshot.TenantID.validate(); err != nil {
		return nil, err
	}
	if err := snapshot.SubscriptionID.validate(); err != nil {
		return nil, err
	}
	if err := snapshot.ResultRef.validate(); err != nil {
		return nil, err
	}
	if err := validateID("deal_id", string(snapshot.DealID)); err != nil {
		return nil, fmt.Errorf("%w: вознаграждение %s: %v", ErrInvalidSnapshot, snapshot.ID, err)
	}
	if snapshot.Calculation.IsZero() {
		return nil, fmt.Errorf("%w: вознаграждение %s без версии правила", ErrInvalidSnapshot, snapshot.ID)
	}
	if snapshot.RateBps < 0 {
		return nil, fmt.Errorf("%w: вознаграждение %s: ставка %d", ErrInvalidSnapshot, snapshot.ID, snapshot.RateBps)
	}
	if !snapshot.Status.isValid() {
		return nil, fmt.Errorf("%w: вознаграждение %s: статус %q", ErrInvalidSnapshot, snapshot.ID, snapshot.Status)
	}
	if snapshot.Version <= 0 {
		return nil, fmt.Errorf("%w: вознаграждение %s версии %d", ErrInvalidSnapshot, snapshot.ID, snapshot.Version)
	}
	expected, err := snapshot.Base.MultiplyByBps(snapshot.RateBps)
	if err != nil {
		return nil, fmt.Errorf("%w: вознаграждение %s: %v", ErrInvalidSnapshot, snapshot.ID, err)
	}
	if !moneyEquals(expected, snapshot.Amount) {
		return nil, fmt.Errorf(
			"%w: вознаграждение %s: сумма %s не совпадает с расчётом %s",
			ErrInvalidSnapshot, snapshot.ID, snapshot.Amount, expected,
		)
	}
	return &SuccessFee{
		id:             snapshot.ID,
		tenantID:       snapshot.TenantID,
		subscriptionID: snapshot.SubscriptionID,
		resultRef:      snapshot.ResultRef,
		dealID:         snapshot.DealID,
		base:           snapshot.Base,
		rateBps:        snapshot.RateBps,
		amount:         snapshot.Amount,
		calculation:    snapshot.Calculation,
		status:         snapshot.Status,
		version:        snapshot.Version,
	}, nil
}

// moneyEquals сравнивает суммы по значению.
func moneyEquals(a, b Money) bool {
	return a.amountMinor == b.amountMinor && a.currency == b.currency
}
