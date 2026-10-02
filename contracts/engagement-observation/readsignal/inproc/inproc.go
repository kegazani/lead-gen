// Package inproc — in-process адаптер контракта read-signal.
//
// Контракт read-signal объявляет две операции владельца: Resolver (разрешить
// непрозрачную ссылку в адрес) и Recorder (учесть состоявшийся переход). При
// co-location потребитель вызывает их прямым вызовом в том же процессе; при
// разделении сервисов ту же форму даёт сетевой адаптер.
//
// Адаптер лежит рядом с контрактом: он знает только типы readsignal.LinkRef и
// readsignal.Transition и не импортирует ни внутренние пакеты владельца, ни
// platform. Переход к сетевому адаптеру не меняет наблюдаемое поведение: вызов
// принимает context.Context и уважает отмену и дедлайн.
package inproc

import (
	"context"
	"errors"

	"lead-gen/contracts/engagement-observation/readsignal"
)

// ErrUnavailable — владелец read-signal не подключён: операция честно сообщает
// об этом, а не выдаёт пустой успех.
var ErrUnavailable = errors.New("readsignal/inproc: владелец не подключён")

// Handler — прямой вызов операций read-signal. Незаданная функция честно
// сообщает о недоступности владельца.
type Handler struct {
	// ResolveFunc разрешает ссылку в адрес назначения.
	ResolveFunc func(ctx context.Context, ref readsignal.LinkRef) (string, error)
	// RecordFunc учитывает состоявшийся переход.
	RecordFunc func(ctx context.Context, transition readsignal.Transition) error
}

// Resolve реализует readsignal.Resolver. Незаданная функция возвращает
// readsignal.ErrNotFound: без адреса переходить некуда.
func (h Handler) Resolve(ctx context.Context, ref readsignal.LinkRef) (string, error) {
	if h.ResolveFunc == nil {
		return "", readsignal.ErrNotFound
	}
	return h.ResolveFunc(ctx, ref)
}

// Record реализует readsignal.Recorder. Незаданная функция сообщает о
// недоступности владельца, а не выдаёт переход за учтённый.
func (h Handler) Record(ctx context.Context, transition readsignal.Transition) error {
	if h.RecordFunc == nil {
		return ErrUnavailable
	}
	return h.RecordFunc(ctx, transition)
}

// InProc заворачивает обработчики в Handler для прямого вызова в одном процессе.
func InProc(handler Handler) Handler { return handler }

// Проверки соответствия операций контракту на этапе компиляции.
var (
	_ readsignal.Resolver = Handler{}
	_ readsignal.Recorder = Handler{}
)
