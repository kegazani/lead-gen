package payment

import (
	"context"

	"lead-gen/backend/contexts/billing/internal/ports"
)

// Unavailable — платёжная система не подключена: порт честно отвечает отказом, а
// не выдумывает ссылку на платёж. Так выглядит сборка, пока контракт с внешней
// системой не подключён.
type Unavailable struct{}

// Unavailable реализует порт платёжной системы.
var _ ports.PaymentGateway = Unavailable{}

// Charge реализует ports.PaymentGateway.
func (Unavailable) Charge(context.Context, ports.ChargeRequest) (ports.ChargeResult, error) {
	return ports.ChargeResult{}, ports.ErrPaymentUnavailable
}
