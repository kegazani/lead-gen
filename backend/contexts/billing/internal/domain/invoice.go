package domain

import "time"

// Invoice — счёт за тарифный период (entity внутри aggregate root
// subscription-invoice). Неизменяем, кроме отметки об оплате, которая проводится
// методом агрегата.
type Invoice struct {
	id         InvoiceID
	period     BillingPeriod
	amount     Money
	issuedAt   time.Time
	paidAt     time.Time
	paymentRef string
}

// ID возвращает идентификатор счёта.
func (i Invoice) ID() InvoiceID { return i.id }

// Period возвращает период начисления.
func (i Invoice) Period() BillingPeriod { return i.period }

// Amount возвращает сумму счёта.
func (i Invoice) Amount() Money { return i.amount }

// IssuedAt возвращает момент выставления.
func (i Invoice) IssuedAt() time.Time { return i.issuedAt }

// Paid сообщает, что счёт оплачен.
func (i Invoice) Paid() bool { return !i.paidAt.IsZero() }

// PaidAt возвращает момент оплаты (нулевое, если не оплачен).
func (i Invoice) PaidAt() time.Time { return i.paidAt }

// PaymentRef возвращает непрозрачную ссылку на платёж во внешней системе.
func (i Invoice) PaymentRef() string { return i.paymentRef }

// withPayment возвращает копию счёта с отметкой об оплате.
func (i Invoice) withPayment(at time.Time, reference string) Invoice {
	i.paidAt = at
	i.paymentRef = reference
	return i
}

// BillingResultRecord — зафиксированный в подписке результат биллинга
// (вознаграждение за подтверждённый результат). Инвариант: результат
// идемпотентен по ссылке на источник.
type BillingResultRecord struct {
	resultRef ResultRef
	kind      BillingResultKind
	amount    Money
	at        time.Time
}

// ResultRef возвращает ссылку на источник результата.
func (r BillingResultRecord) ResultRef() ResultRef { return r.resultRef }

// Kind возвращает вид результата.
func (r BillingResultRecord) Kind() BillingResultKind { return r.kind }

// Amount возвращает сумму результата.
func (r BillingResultRecord) Amount() Money { return r.amount }

// At возвращает момент фиксации.
func (r BillingResultRecord) At() time.Time { return r.at }
