package prometheus

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeRanger — управляемый источник range-запросов.
type fakeRanger struct {
	matrix   Matrix
	warnings []string
	err      error
	gotQuery string
}

func (f *fakeRanger) QueryRange(_ context.Context, query string, _, _ time.Time, _ time.Duration) (Matrix, []string, error) {
	f.gotQuery = query
	return f.matrix, f.warnings, f.err
}

func TestResolveQueryEscapesValue(t *testing.T) {
	got := ResolveQuery(`rate(x{node="{{node}}"}[5m])`, "{{node}}", `a"b\c`)
	if got != `rate(x{node="a\"b\\c"}[5m])` {
		t.Fatalf("ResolveQuery = %q", got)
	}
	if ResolveQuery("q", "{{node}}", "") != "q" {
		t.Fatal("пустое значение не должно менять запрос")
	}
}

func TestResolveWindowCaps(t *testing.T) {
	limits := Limits{MaxPoints: 10, MaxRange: time.Hour, MinStep: 10 * time.Second}
	window, step, warnings, err := ResolveWindow(Panel{Range: 2 * time.Hour}, "48h", "1s", limits)
	if err != nil {
		t.Fatalf("ResolveWindow: %v", err)
	}
	if window != time.Hour {
		t.Fatalf("диапазон не ограничен пределом: %v", window)
	}
	if step < 10*time.Second {
		t.Fatalf("шаг ниже минимума: %v", step)
	}
	if len(warnings) == 0 || warnings[0] != WarningStepCapped {
		t.Fatalf("нет пометки об увеличении шага: %v", warnings)
	}

	if _, _, _, err := ResolveWindow(Panel{}, "nonsense", "", limits); err == nil {
		t.Fatal("некорректный диапазон не отвергнут")
	}
	if _, _, _, err := ResolveWindow(Panel{}, "", "nonsense", limits); err == nil {
		t.Fatal("некорректный шаг не отвергнут")
	}
}

// TestReadPanelUnavailable проверяет честный отказ: недоступный источник даёт
// Available=false с причиной и без точек; ошибка транспорта — тот же исход.
func TestReadPanelUnavailable(t *testing.T) {
	panel := Panel{Key: "k"}
	if result := ReadPanel(context.Background(), nil, panel, "q", time.Now(), time.Now(), time.Hour, time.Minute, DefaultLimits()); result.Available || result.Reason != ReasonSourceUnavailable || len(result.Series) != 0 {
		t.Fatalf("nil-источник не дал честного отказа: %+v", result)
	}
	broken := &fakeRanger{err: errors.New("сбой")}
	result := ReadPanel(context.Background(), broken, panel, "q", time.Now(), time.Now(), time.Hour, time.Minute, DefaultLimits())
	if result.Available || result.Reason != ReasonSourceUnavailable || len(result.Series) != 0 {
		t.Fatalf("ошибка транспорта не дала честного отказа: %+v", result)
	}
}

// TestReadPanelReturnsPoints проверяет, что рабочий источник отдаёт точки и
// запрос панели (а не свободный PromQL) уходит источнику.
func TestReadPanelReturnsPoints(t *testing.T) {
	source := &fakeRanger{matrix: Matrix{
		{Metric: map[string]string{"service": "svc"}, Samples: []Sample{{Time: time.Now().UTC(), Value: 3.5}}},
	}}
	result := ReadPanel(context.Background(), source, Panel{Key: "k"}, "up{}", time.Now(), time.Now(), time.Hour, time.Minute, DefaultLimits())
	if !result.Available || len(result.Series) != 1 || result.Series[0].Points[0].Value != 3.5 {
		t.Fatalf("рабочий источник не отдал точки: %+v", result)
	}
	if source.gotQuery != "up{}" {
		t.Fatalf("источник получил запрос %q", source.gotQuery)
	}
}
