// Package watch реализует подписку на изменения заявок тенанта поверх порта
// транзакции. Уведомление не несёт данных — подписчик сам перечитывает список,
// поэтому сигнал «что-то изменилось» всегда приводит к свежему срезу.
package watch

import (
	"sync"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
)

// Notifier — реестр подписчиков на изменения тенанта. Канал подписчика ёмкостью
// 1 коалесцирует пропущенные дельты: медленный подписчик не блокирует отправителя,
// а счётчик изменений ему не важен — он перечитывает состояние целиком.
type Notifier struct {
	mu       sync.Mutex
	watchers map[domain.TenantID]map[chan struct{}]struct{}
}

// NewNotifier создаёт пустой реестр подписчиков.
func NewNotifier() *Notifier {
	return &Notifier{watchers: make(map[domain.TenantID]map[chan struct{}]struct{})}
}

// Watch подписывает наблюдателя на изменения тенанта и возвращает канал
// уведомлений и функцию отписки. Повторный вызов отписки безопасен.
func (n *Notifier) Watch(tenantID domain.TenantID) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	n.mu.Lock()
	if n.watchers[tenantID] == nil {
		n.watchers[tenantID] = make(map[chan struct{}]struct{})
	}
	n.watchers[tenantID][ch] = struct{}{}
	n.mu.Unlock()

	cancel := func() {
		n.mu.Lock()
		if set := n.watchers[tenantID]; set != nil {
			delete(set, ch)
			if len(set) == 0 {
				delete(n.watchers, tenantID)
			}
		}
		n.mu.Unlock()
	}
	return ch, cancel
}

// Notify рассылает сигнал подписчикам тенанта. Уведомление не блокирует
// отправителя: переполненный канал просто теряет один сигнал — подписчик всё
// равно перечитывает свежий список.
func (n *Notifier) Notify(tenantID domain.TenantID) {
	n.mu.Lock()
	notify := make([]chan struct{}, 0, len(n.watchers[tenantID]))
	for ch := range n.watchers[tenantID] {
		notify = append(notify, ch)
	}
	n.mu.Unlock()

	for _, ch := range notify {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
