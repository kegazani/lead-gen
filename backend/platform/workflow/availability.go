package workflow

import "sync/atomic"

// Availability — атомарный признак доступности рантайма durable-исполнения.
//
// Один признак на процесс: выставляется при подключении к Temporal, снимается при
// деградации, а читается пробой готовности сервиса. Проба живости от него не
// зависит: процесс остаётся живым и без рантайма, но честно сообщает о
// неготовности обслуживать работу.
type Availability struct{ ready atomic.Bool }

// NewAvailability возвращает признак недоступного рантайма: до успешного
// подключения сервис не готов.
func NewAvailability() *Availability { return &Availability{} }

// MarkReady помечает рантайм доступным. Вызов на nil-приёмнике безопасен.
func (a *Availability) MarkReady() {
	if a != nil {
		a.ready.Store(true)
	}
}

// MarkNotReady помечает рантайм недоступным. Вызов на nil-приёмнике безопасен.
func (a *Availability) MarkNotReady() {
	if a != nil {
		a.ready.Store(false)
	}
}

// Ready сообщает о доступности рантайма. nil-приёмник (рантайм не подключён в
// этой сборке) считается готовым: отсутствие зависимости — не неготовность.
func (a *Availability) Ready() bool {
	if a == nil {
		return true
	}
	return a.ready.Load()
}
