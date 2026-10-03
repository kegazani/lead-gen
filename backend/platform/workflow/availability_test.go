package workflow

import "testing"

// TestAvailabilityTracksReadiness проверяет, что признак доступности рантайма
// переключается и читается пробой готовности.
func TestAvailabilityTracksReadiness(t *testing.T) {
	availability := NewAvailability()
	if availability.Ready() {
		t.Fatal("до подключения рантайм должен быть недоступен")
	}
	availability.MarkReady()
	if !availability.Ready() {
		t.Fatal("после подключения рантайм должен быть доступен")
	}
	availability.MarkNotReady()
	if availability.Ready() {
		t.Fatal("после деградации рантайм должен быть недоступен")
	}
}

// TestAvailabilityNilIsReady проверяет, что отсутствие рантайма в сборке не
// считается неготовностью.
func TestAvailabilityNilIsReady(t *testing.T) {
	var availability *Availability
	if !availability.Ready() {
		t.Fatal("nil-признак должен читаться как готовность")
	}
	// Обновление nil-признака не паникует.
	availability.MarkReady()
	availability.MarkNotReady()
}
