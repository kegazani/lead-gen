package observ

import "testing"

func TestNewCorrelationIDUniqueNonEmpty(t *testing.T) {
	a := NewCorrelationID()
	b := NewCorrelationID()

	if a == "" || b == "" {
		t.Fatalf("пустой correlation_id: %q %q", a, b)
	}
	if a == b {
		t.Fatalf("два вызова вернули одинаковое значение: %q", a)
	}
	if len(a) > maxCorrelationIDLen {
		t.Fatalf("длина превышает предел: %d", len(a))
	}
}
