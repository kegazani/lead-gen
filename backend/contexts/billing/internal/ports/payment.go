package ports

import (
	"context"
	"errors"

	"lead-gen/backend/contexts/billing/internal/domain"
)

// ErrPaymentUnavailable — внешняя платёжная система недоступна или контракт с ней
// не подключён. Списание не выполнено, счёт не считается оплаченным.
var ErrPaymentUnavailable = errors.New("порты: платёжная система недоступна")

// ErrPaymentRejected — платёжная система отклонила списание по доменной причине
// (недостаточно средств, запрет операции). Отличается от недоступности.
var ErrPaymentRejected = errors.New("порты: платёжная система отклонила платёж")

// ChargeRequest — запрос списания по счёту. Несёт только идентификаторы и сумму;
// платёжных реквизитов клиента здесь нет — их владеет платёжная система.
type ChargeRequest struct {
	TenantID       domain.TenantID
	SubscriptionID domain.SubscriptionID
	InvoiceID      domain.InvoiceID
	Amount         domain.Money
	// IdempotencyKey — ключ идемпотентности списания: повторный запрос по тому же
	// ключу не создаёт второго платежа.
	IdempotencyKey string
}

// ChargeResult — результат списания: непрозрачная ссылка на платёж.
type ChargeResult struct {
	Reference string
}

// PaymentGateway — порт внешней платёжной системы. Реализация обязана принимать
// context.Context и уважать дедлайн и отмену, а также соблюдать ключ
// идемпотентности. Недоступность даёт ErrPaymentUnavailable, доменный отказ —
// ErrPaymentRejected; ошибка не выдаётся за успешную оплату.
type PaymentGateway interface {
	Charge(ctx context.Context, request ChargeRequest) (ChargeResult, error)
}
