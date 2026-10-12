// Package panels — технический реестр панелей экрана: состав панелей задаётся
// регистрацией (данными), а не жёстким шаблоном экрана. Пакет не содержит
// доменных терминов: он хранит объявления и изолирует отказ источника одной
// панели, не ломая остальные.
//
// Каждая панель объявляет идентификатор, заголовок, источник и способ
// обновления. Новая панель появляется регистрацией без правки остальных.
// Рендеринг панели — функция владельца экрана: она возвращает готовый фрагмент
// разметки либо ошибку. Ошибка одного рендерера локализуется панелью: остальные
// панели продолжают работать, а виновница получает признак недоступности.
package panels

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"sort"
	"time"
)

// Source — источник данных панели (машинный код): метрики, процессы, агрегаты и
// т. д. Пакет не толкует значения, а показывает их как объявленные данные.
type Source string

// Refresh — способ обновления панели.
type Refresh string

const (
	// RefreshPoll — панель обновляется периодическим опросом.
	RefreshPoll Refresh = "poll"
	// RefreshStatic — панель обновляется вместе с загрузкой экрана.
	RefreshStatic Refresh = "static"
)

// DefaultTimeout — предел рендеринга одной панели. Ноль в Registry — это значение.
const DefaultTimeout = 5 * time.Second

// Definition — объявление панели.
type Definition struct {
	// ID — машинный идентификатор панели. Обязателен и уникален.
	ID string
	// Title — заголовок панели.
	Title string
	// Source — источник данных панели.
	Source Source
	// Refresh — способ обновления панели.
	Refresh Refresh
	// Order — порядок вывода; при равенстве порядок задаёт ID.
	Order int
}

// Renderer рисует панель. Ошибка (в том числе превышение таймаута контекста)
// помечает панель недоступной, не прерывая остальные.
type Renderer func(ctx context.Context) (template.HTML, error)

// ReasonUnavailable — машинный код недоступности панели.
const ReasonUnavailable = "unavailable"

// State — результат рендеринга панели: объявление, готовый фрагмент и признак
// доступности. Body пуст, если панель недоступна: разметка отказа — забота экрана.
type State struct {
	Definition
	Available bool
	Reason    string
	Body      template.HTML
}

// Registry — реестр панелей.
type Registry struct {
	entries []entry
	timeout time.Duration
}

type entry struct {
	def    Definition
	render Renderer
}

// NewRegistry собирает реестр. timeout <= 0 — DefaultTimeout.
func NewRegistry(timeout time.Duration) *Registry {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Registry{timeout: timeout}
}

// ErrEmptyID — панель без идентификатора.
var ErrEmptyID = errors.New("panels: идентификатор панели пуст")

// ErrDuplicateID — повторная регистрация идентификатора.
var ErrDuplicateID = errors.New("panels: панель с таким идентификатором уже объявлена")

// Register добавляет панель. Пустой идентификатор и повтор отклоняются: реестр не
// подменяет панель молча.
func (r *Registry) Register(def Definition, render Renderer) error {
	if def.ID == "" {
		return ErrEmptyID
	}
	for _, existing := range r.entries {
		if existing.def.ID == def.ID {
			return fmt.Errorf("%w: %q", ErrDuplicateID, def.ID)
		}
	}
	r.entries = append(r.entries, entry{def: def, render: render})
	return nil
}

// Definitions возвращает объявления панелей в порядке вывода (Order, ID).
func (r *Registry) Definitions() []Definition {
	ordered := r.ordered()
	out := make([]Definition, 0, len(ordered))
	for _, item := range ordered {
		out = append(out, item.def)
	}
	return out
}

// Render рисует все панели в порядке вывода. Отказ одной панели локализуется: её
// State получает Available=false, остальные рисуются.
func (r *Registry) Render(ctx context.Context) []State {
	ordered := r.ordered()
	states := make([]State, 0, len(ordered))
	for _, item := range ordered {
		state := State{Definition: item.def}
		if item.render == nil {
			state.Reason = ReasonUnavailable
			states = append(states, state)
			continue
		}
		panelCtx, cancel := context.WithTimeout(ctx, r.timeout)
		body, err := item.render(panelCtx)
		cancel()
		if err != nil {
			state.Reason = ReasonUnavailable
			states = append(states, state)
			continue
		}
		state.Available = true
		state.Body = body
		states = append(states, state)
	}
	return states
}

// ordered возвращает записи в детерминированном порядке вывода.
func (r *Registry) ordered() []entry {
	out := make([]entry, len(r.entries))
	copy(out, r.entries)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].def.Order != out[j].def.Order {
			return out[i].def.Order < out[j].def.Order
		}
		return out[i].def.ID < out[j].def.ID
	})
	return out
}
