package query

import (
	"context"
	"errors"
	"strings"
	"testing"

	"lead-gen/backend/contexts/platform-governance/internal/app"
	"lead-gen/backend/contexts/platform-governance/internal/ports"
)

// fakeCatalog — управляемый порт чтения адаптеров для теста.
type fakeCatalog struct {
	states []ports.AdapterStatus
	err    error
}

// States реализует ports.AdapterCatalog.
func (f fakeCatalog) States(context.Context) ([]ports.AdapterStatus, error) {
	return f.states, f.err
}

// TestAdapterCatalogUnavailable проверяет, что отсутствие владельца адаптеров
// даёт честную недоступность, а не пустой успех, и что текст ошибки владельца не
// переносится наружу.
func TestAdapterCatalogUnavailable(t *testing.T) {
	if _, err := NewAdapterCatalogHandler(nil).Handle(context.Background()); !errors.Is(err, app.ErrUnavailable) {
		t.Fatalf("nil-порт: ошибка = %v, ожидалась ErrUnavailable", err)
	}

	const secret = "token=supersecret-value"
	handler := NewAdapterCatalogHandler(fakeCatalog{err: errors.New("сбой источника: " + secret)})
	_, err := handler.Handle(context.Background())
	if !errors.Is(err, app.ErrUnavailable) {
		t.Fatalf("сбой порта: ошибка = %v, ожидалась ErrUnavailable", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("текст ошибки владельца утёк наружу: %v", err)
	}
}

// TestAdapterCatalogNormalizes проверяет, что неизвестные состояние и причина
// сводятся к объявленному набору: произвольный текст не проходит наружу, а
// несконфигурированный адаптер не выдаётся за «в норме».
func TestAdapterCatalogNormalizes(t *testing.T) {
	handler := NewAdapterCatalogHandler(fakeCatalog{states: []ports.AdapterStatus{
		{Adapter: "crawler", State: "weird", Reason: "token=secret"},
		{Adapter: "mail", State: ports.AdapterStateDown, Reason: ports.AdapterReasonAuthFailed},
		{Adapter: "smtp", State: "", Reason: ""},
	}})
	states, err := handler.Handle(context.Background())
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(states) != 3 {
		t.Fatalf("состояний = %d, ожидалось 3", len(states))
	}
	if states[0].State != ports.AdapterStateUnknown || states[0].Reason != ports.AdapterReasonNone {
		t.Fatalf("неизвестные значения не нормализованы: %+v", states[0])
	}
	if states[1].State != ports.AdapterStateDown || states[1].Reason != ports.AdapterReasonAuthFailed {
		t.Fatalf("объявленные значения изменены: %+v", states[1])
	}
	if states[2].State != ports.AdapterStateUnknown {
		t.Fatalf("несконфигурированный адаптер выдан за «в норме»: %+v", states[2])
	}
}
