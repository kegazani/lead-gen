package query

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/platform-governance/internal/app"
	"lead-gen/backend/contexts/platform-governance/internal/ports"
)

// fakeCatalog — управляемый порт чтения адаптеров для теста.
type fakeCatalog struct {
	states []ports.AdapterState
	err    error
}

// States реализует ports.AdapterCatalog.
func (f fakeCatalog) States(context.Context) ([]ports.AdapterState, error) {
	return f.states, f.err
}

// TestAdapterCatalogUnavailable проверяет, что отсутствие владельца адаптеров
// даёт честную недоступность, а не пустой успех.
func TestAdapterCatalogUnavailable(t *testing.T) {
	if _, err := NewAdapterCatalogHandler(nil).Handle(context.Background()); !errors.Is(err, app.ErrUnavailable) {
		t.Fatalf("nil-порт: ошибка = %v, ожидалась ErrUnavailable", err)
	}
	handler := NewAdapterCatalogHandler(fakeCatalog{err: errors.New("транспорт недоступен")})
	if _, err := handler.Handle(context.Background()); !errors.Is(err, app.ErrUnavailable) {
		t.Fatalf("сбой порта: ошибка = %v, ожидалась ErrUnavailable", err)
	}
}

// TestAdapterCatalogNormalizes проверяет, что неизвестные состояние и причина
// сводятся к объявленному набору: произвольный текст не проходит наружу.
func TestAdapterCatalogNormalizes(t *testing.T) {
	handler := NewAdapterCatalogHandler(fakeCatalog{states: []ports.AdapterState{
		{Name: "crawler", CapabilitySetVersion: "1.2.0", Health: "weird", Reason: "token=secret"},
		{Name: "mail", CapabilitySetVersion: "1.0.0", Health: ports.AdapterDown, Reason: ports.AdapterReasonAuthFailed},
	}})
	states, err := handler.Handle(context.Background())
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(states) != 2 {
		t.Fatalf("состояний = %d, ожидалось 2", len(states))
	}
	if states[0].Health != ports.AdapterUnknown || states[0].Reason != ports.AdapterReasonNone {
		t.Fatalf("неизвестные значения не нормализованы: %+v", states[0])
	}
	if states[1].Health != ports.AdapterDown || states[1].Reason != ports.AdapterReasonAuthFailed {
		t.Fatalf("объявленные значения изменены: %+v", states[1])
	}
}
