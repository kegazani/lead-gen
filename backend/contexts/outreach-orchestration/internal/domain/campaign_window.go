package domain

import (
	"fmt"
	"time"
)

// CampaignWindow — value object: допустимый временной интервал выполнения
// действий кампании.
//
// Значение неизменяемо: поля закрыты, новый интервал создаётся только
// конструктором NewCampaignWindow, который проверяет границы. Нулевое значение
// считается недействительным и отвергается валидацией агрегата.
type CampaignWindow struct {
	start time.Time
	end   time.Time
}

// NewCampaignWindow создаёт проверенное окно кампании. Границы обязательны,
// начало должно быть строго раньше окончания. Значения приводятся к UTC,
// чтобы сравнение окон не зависело от локальной зоны вызывающего.
func NewCampaignWindow(start, end time.Time) (CampaignWindow, error) {
	if start.IsZero() || end.IsZero() {
		return CampaignWindow{}, fmt.Errorf("%w: границы окна обязательны", ErrInvalidCampaignWindow)
	}
	s, e := start.UTC(), end.UTC()
	if !s.Before(e) {
		return CampaignWindow{}, fmt.Errorf(
			"%w: начало %s не раньше окончания %s",
			ErrInvalidCampaignWindow,
			s.Format(time.RFC3339),
			e.Format(time.RFC3339),
		)
	}
	return CampaignWindow{start: s, end: e}, nil
}

// Start возвращает начало окна (UTC).
func (w CampaignWindow) Start() time.Time { return w.start }

// End возвращает окончание окна (UTC).
func (w CampaignWindow) End() time.Time { return w.end }

// Duration возвращает длительность окна.
func (w CampaignWindow) Duration() time.Duration { return w.end.Sub(w.start) }

// IsZero сообщает, что окно не задано.
func (w CampaignWindow) IsZero() bool { return w.start.IsZero() && w.end.IsZero() }

// validate проверяет окно на полноту и порядок границ. Используется при
// создании кампании и при восстановлении агрегата из сохранённого состояния,
// куда окно могло попасть в обход конструктора.
func (w CampaignWindow) validate() error {
	if w.IsZero() {
		return fmt.Errorf("%w: границы окна обязательны", ErrInvalidCampaignWindow)
	}
	if !w.start.Before(w.end) {
		return fmt.Errorf(
			"%w: начало %s не раньше окончания %s",
			ErrInvalidCampaignWindow,
			w.start.Format(time.RFC3339),
			w.end.Format(time.RFC3339),
		)
	}
	return nil
}

// Contains сообщает, попадает ли момент в окно, включая обе границы.
// Для нулевого окна всегда false.
func (w CampaignWindow) Contains(moment time.Time) bool {
	if w.IsZero() {
		return false
	}
	m := moment.UTC()
	return !m.Before(w.start) && !m.After(w.end)
}

// Equals сравнивает окна по моменту времени, а не по внутреннему представлению.
func (w CampaignWindow) Equals(other CampaignWindow) bool {
	return w.start.Equal(other.start) && w.end.Equal(other.end)
}

func (w CampaignWindow) String() string {
	if w.IsZero() {
		return "campaign-window(empty)"
	}
	return fmt.Sprintf(
		"campaign-window(%s..%s)",
		w.start.Format(time.RFC3339),
		w.end.Format(time.RFC3339),
	)
}
