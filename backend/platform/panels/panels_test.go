package panels

import (
	"context"
	"errors"
	"html/template"
	"strings"
	"testing"
	"time"
)

// TestRegisterRejectsEmptyAndDuplicate проверяет, что реестр не подменяет панель
// молча: пустой идентификатор и повторная регистрация — ошибка.
func TestRegisterRejectsEmptyAndDuplicate(t *testing.T) {
	registry := NewRegistry(0)
	if err := registry.Register(Definition{}, nil); !errors.Is(err, ErrEmptyID) {
		t.Fatalf("пустой id: %v", err)
	}
	if err := registry.Register(Definition{ID: "a"}, nil); err != nil {
		t.Fatalf("первая регистрация: %v", err)
	}
	if err := registry.Register(Definition{ID: "a"}, nil); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("повтор: %v", err)
	}
}

// TestNewPanelAppearsWithoutTouchingOthers проверяет критерий 5.4: новая панель
// появляется регистрацией, без правки кода остальных панелей.
func TestNewPanelAppearsWithoutTouchingOthers(t *testing.T) {
	registry := NewRegistry(0)
	mustRegister(t, registry, Definition{ID: "a", Title: "A", Order: 1}, okRenderer("a"))
	states := registry.Render(context.Background())
	if len(states) != 1 || states[0].ID != "a" {
		t.Fatalf("исходный состав: %+v", states)
	}

	mustRegister(t, registry, Definition{ID: "b", Title: "B", Order: 2}, okRenderer("b"))
	states = registry.Render(context.Background())
	if len(states) != 2 || states[0].ID != "a" || states[1].ID != "b" {
		t.Fatalf("новая панель не появилась в порядке: %+v", states)
	}
}

// TestFailedPanelIsolated проверяет критерий 5.4: отказ одной панели локализуется
// и не ломает остальные.
func TestFailedPanelIsolated(t *testing.T) {
	registry := NewRegistry(0)
	mustRegister(t, registry, Definition{ID: "bad", Order: 1}, func(context.Context) (template.HTML, error) {
		return "", errors.New("источник отказал")
	})
	mustRegister(t, registry, Definition{ID: "good", Order: 2}, okRenderer("good"))

	states := registry.Render(context.Background())
	if len(states) != 2 {
		t.Fatalf("состав: %+v", states)
	}
	if states[0].Available || states[0].Reason != ReasonUnavailable || states[0].Body != "" {
		t.Fatalf("отказ не локализован: %+v", states[0])
	}
	if !states[1].Available || !strings.Contains(string(states[1].Body), "good") {
		t.Fatalf("рабочая панель пострадала: %+v", states[1])
	}
}

// TestTimeoutIsolated проверяет, что превышение таймаута панели помечает её
// недоступной, а соседние рисуются.
func TestTimeoutIsolated(t *testing.T) {
	registry := NewRegistry(10 * time.Millisecond)
	mustRegister(t, registry, Definition{ID: "slow", Order: 1}, func(ctx context.Context) (template.HTML, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
	mustRegister(t, registry, Definition{ID: "fast", Order: 2}, okRenderer("fast"))

	states := registry.Render(context.Background())
	if states[0].Available {
		t.Fatalf("панель с таймаутом выдана за доступную: %+v", states[0])
	}
	if !states[1].Available {
		t.Fatalf("соседняя панель не нарисована: %+v", states[1])
	}
}

func mustRegister(t *testing.T, registry *Registry, def Definition, render Renderer) {
	t.Helper()
	if err := registry.Register(def, render); err != nil {
		t.Fatalf("Register(%q): %v", def.ID, err)
	}
}

func okRenderer(text string) Renderer {
	return func(context.Context) (template.HTML, error) {
		return template.HTML("<p>" + text + "</p>"), nil
	}
}
