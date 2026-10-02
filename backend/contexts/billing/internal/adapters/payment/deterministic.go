package payment

import (
	"context"
	"strings"
	"sync"

	"lead-gen/backend/contexts/billing/internal/ports"
)

// Deterministic — детерминированный адаптер платёжной системы без сети: списание
// успешно, если счёт не объявлен отклоняемым, и возвращает детерминированную
// непрозрачную ссылку на платёж. Повторный запрос по тому же ключу идемпотентности
// возвращает ту же ссылку и не создаёт второго платежа. Адаптер пригоден для
// локального запуска и тестов и безопасен для конкурентного использования.
type Deterministic struct {
	mu      sync.Mutex
	reject  map[string]struct{}
	charged map[string]string
}

// Deterministic реализует порт платёжной системы.
var _ ports.PaymentGateway = (*Deterministic)(nil)

// NewDeterministic создаёт адаптер, успешно списывающий любой счёт.
func NewDeterministic() *Deterministic {
	return &Deterministic{
		reject:  make(map[string]struct{}),
		charged: make(map[string]string),
	}
}

// Reject объявляет счёт отклоняемым: списание по нему даёт ErrPaymentRejected.
// Значение пустое — no-op. Метод не безопасен для одновременного вызова с Charge.
func (d *Deterministic) Reject(invoiceID string) {
	if strings.TrimSpace(invoiceID) == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reject[invoiceID] = struct{}{}
}

// Charge реализует ports.PaymentGateway.
func (d *Deterministic) Charge(_ context.Context, request ports.ChargeRequest) (ports.ChargeResult, error) {
	key := request.IdempotencyKey
	if key == "" {
		key = string(request.InvoiceID)
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if _, ok := d.reject[string(request.InvoiceID)]; ok {
		return ports.ChargeResult{}, ports.ErrPaymentRejected
	}
	if reference, ok := d.charged[key]; ok {
		return ports.ChargeResult{Reference: reference}, nil
	}
	reference := "deterministic:" + string(request.InvoiceID)
	d.charged[key] = reference
	return ports.ChargeResult{Reference: reference}, nil
}

// Charged сообщает число подтверждённых списаний. Метод нужен тестам.
func (d *Deterministic) Charged() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.charged)
}
