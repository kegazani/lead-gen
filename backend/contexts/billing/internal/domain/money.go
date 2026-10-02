package domain

import (
	"fmt"
	"strings"
)

// Money — точная сумма в минимальных единицах указанной валюты (VO money,
// architecture/domain-catalog.yaml). Сумма неотрицательна и неизменяема; арифметика
// следит за совпадением валют.
type Money struct {
	amountMinor int64
	currency    string
}

// NewMoney собирает VO из суммы в минимальных единицах и кода валюты. Отрицательная
// сумма отвергается: счета и вознаграждения не бывают отрицательными. Код валюты —
// три буквы, приводится к верхнему регистру.
func NewMoney(amountMinor int64, currency string) (Money, error) {
	if amountMinor < 0 {
		return Money{}, fmt.Errorf("%w: %d", ErrNegativeAmount, amountMinor)
	}
	code := strings.ToUpper(strings.TrimSpace(currency))
	if len(code) != 3 {
		return Money{}, fmt.Errorf("%w: %q", ErrInvalidCurrency, currency)
	}
	for _, r := range code {
		if r < 'A' || r > 'Z' {
			return Money{}, fmt.Errorf("%w: %q", ErrInvalidCurrency, currency)
		}
	}
	return Money{amountMinor: amountMinor, currency: code}, nil
}

// ZeroMoney возвращает нулевую сумму в заданной валюте.
func ZeroMoney(currency string) (Money, error) { return NewMoney(0, currency) }

// AmountMinor возвращает сумму в минимальных единицах.
func (m Money) AmountMinor() int64 { return m.amountMinor }

// Currency возвращает код валюты.
func (m Money) Currency() string { return m.currency }

// IsZero сообщает, что сумма не задана (нулевая или без валюты).
func (m Money) IsZero() bool { return m.amountMinor == 0 && m.currency == "" }

// IsZeroAmount сообщает, что сумма равна нулю (валюта при этом задана).
func (m Money) IsZeroAmount() bool { return m.amountMinor == 0 }

// Add складывает суммы одной валюты.
func (m Money) Add(other Money) (Money, error) {
	if err := m.checkCurrency(other); err != nil {
		return Money{}, err
	}
	sum := m.amountMinor + other.amountMinor
	if sum < m.amountMinor {
		return Money{}, fmt.Errorf("домен: переполнение суммы")
	}
	return Money{amountMinor: sum, currency: m.currency}, nil
}

// Subtract вычитает сумму той же валюты. Результат не может быть отрицательным.
func (m Money) Subtract(other Money) (Money, error) {
	if err := m.checkCurrency(other); err != nil {
		return Money{}, err
	}
	if other.amountMinor > m.amountMinor {
		return Money{}, fmt.Errorf("%w: %d - %d", ErrNegativeAmount, m.amountMinor, other.amountMinor)
	}
	return Money{amountMinor: m.amountMinor - other.amountMinor, currency: m.currency}, nil
}

// MultiplyByBps возвращает долю суммы по ставке в базисных пунктах (1 bps = 0.01%).
// Округление — до ближайшего минимального пункта (половина вверх). Ставка не может
// быть отрицательной.
func (m Money) MultiplyByBps(bps int64) (Money, error) {
	if m.currency == "" {
		return Money{}, ErrInvalidCurrency
	}
	if bps < 0 {
		return Money{}, fmt.Errorf("%w: %d", ErrInvalidRate, bps)
	}
	if bps == 0 || m.amountMinor == 0 {
		return Money{amountMinor: 0, currency: m.currency}, nil
	}
	product := m.amountMinor * bps
	if product/bps != m.amountMinor {
		return Money{}, fmt.Errorf("домен: переполнение при расчёте доли")
	}
	amount := (product + 5000) / 10000
	return Money{amountMinor: amount, currency: m.currency}, nil
}

// checkCurrency сверяет валюты двух сумм.
func (m Money) checkCurrency(other Money) error {
	if m.currency != other.currency {
		return fmt.Errorf("%w: %q и %q", ErrCurrencyMismatch, m.currency, other.currency)
	}
	return nil
}

// String возвращает сумму в виде «<минорные единицы> <валюта>».
func (m Money) String() string {
	return fmt.Sprintf("%d %s", m.amountMinor, m.currency)
}
