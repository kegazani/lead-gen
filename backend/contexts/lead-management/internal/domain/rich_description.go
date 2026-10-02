package domain

import (
	"fmt"
	"strings"
	"time"
)

// ComponentKind — вид составляющей rich-описания компании.
type ComponentKind string

const (
	// ComponentProducts — продукты и услуги компании.
	ComponentProducts ComponentKind = "products"
	// ComponentSignals — сигналы из открытых источников.
	ComponentSignals ComponentKind = "signals"
	// ComponentSize — размер компании (источник company-size-assessment).
	ComponentSize ComponentKind = "size"
)

// valid сообщает, что вид составляющей объявлен доменом.
func (k ComponentKind) valid() bool {
	switch k {
	case ComponentProducts, ComponentSignals, ComponentSize:
		return true
	default:
		return false
	}
}

// RichComponent — одна составляющая rich-описания с указанием источника.
// Источник обязателен: составляющая без источника не принимается — иначе
// происхождение данных теряется (по образцу company-size-assessment).
type RichComponent struct {
	// Kind — вид составляющей.
	Kind ComponentKind
	// Value — значение составляющей.
	Value string
	// Source — источник составляющей (например, osint-enrichment,
	// company-size-assessment).
	Source string
	// SourceURL — адрес источника, если он есть.
	SourceURL string
	// ObservedAt — момент наблюдения значения.
	ObservedAt time.Time
}

// validate проверяет полноту составляющей.
func (c RichComponent) validate() error {
	if !c.Kind.valid() {
		return fmt.Errorf("%w: неизвестный вид составляющей %q", ErrInvalidRichDescription, c.Kind)
	}
	if strings.TrimSpace(c.Value) == "" {
		return fmt.Errorf("%w: пустое значение составляющей %q", ErrInvalidRichDescription, c.Kind)
	}
	if strings.TrimSpace(c.Source) == "" {
		return fmt.Errorf("%w: составляющая %q без источника", ErrInvalidRichDescription, c.Kind)
	}
	return nil
}

// RichDescription — rich-описание компании: набор составляющих с источниками.
// Собирается шагом обогащения и служит основой эмбеддинга.
type RichDescription struct {
	components []RichComponent
	builtAt    time.Time
}

// RichDescriptionSnapshot — сохранённое состояние rich-описания.
type RichDescriptionSnapshot struct {
	Components []RichComponent
	BuiltAt    time.Time
}

// NewRichDescription собирает rich-описание, проверяя каждую составляющую.
func NewRichDescription(components []RichComponent, at time.Time) (RichDescription, error) {
	out := make([]RichComponent, 0, len(components))
	for _, component := range components {
		if err := component.validate(); err != nil {
			return RichDescription{}, err
		}
		component.Value = strings.TrimSpace(component.Value)
		component.Source = strings.TrimSpace(component.Source)
		out = append(out, component)
	}
	if at.IsZero() {
		return RichDescription{}, fmt.Errorf("%w: не задано время сборки", ErrInvalidRichDescription)
	}
	return RichDescription{components: out, builtAt: at}, nil
}

// IsZero сообщает, что описание не собрано.
func (r RichDescription) IsZero() bool { return len(r.components) == 0 }

// Components возвращает составляющие описания.
func (r RichDescription) Components() []RichComponent {
	return append([]RichComponent(nil), r.components...)
}

// BuiltAt возвращает время сборки.
func (r RichDescription) BuiltAt() time.Time { return r.builtAt }

// Text собирает текстовую сводку описания для эмбеддинга: значения составляющих
// через пробел. Источники в текст не попадают — они метаданные, а не смысл.
func (r RichDescription) Text() string {
	values := make([]string, 0, len(r.components))
	for _, component := range r.components {
		values = append(values, component.Value)
	}
	return strings.Join(values, " ")
}

// snapshot возвращает состояние описания для сохранения.
func (r RichDescription) snapshot() RichDescriptionSnapshot {
	return RichDescriptionSnapshot{
		Components: append([]RichComponent(nil), r.components...),
		BuiltAt:    r.builtAt,
	}
}

// rehydrateRichDescription восстанавливает описание из снимка. Пустой снимок даёт
// пустое описание: отсутствие rich-описания — допустимое состояние.
func rehydrateRichDescription(s RichDescriptionSnapshot) (RichDescription, error) {
	if len(s.Components) == 0 && s.BuiltAt.IsZero() {
		return RichDescription{}, nil
	}
	return NewRichDescription(s.Components, s.BuiltAt)
}
