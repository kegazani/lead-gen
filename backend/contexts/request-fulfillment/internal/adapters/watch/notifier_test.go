package watch

import (
	"testing"
	"time"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
)

// TestNotifierWatchAndNotify проверяет, что подписчик получает уведомление об
// изменении тенанта, а подписчик другого тенанта — нет.
func TestNotifierWatchAndNotify(t *testing.T) {
	notifier := NewNotifier()
	notify, cancel := notifier.Watch(domain.TenantID("t1"))
	defer cancel()

	notifier.Notify(domain.TenantID("t2"))
	select {
	case <-notify:
		t.Fatalf("подписчик t1 получил уведомление об изменении t2")
	default:
	}

	notifier.Notify(domain.TenantID("t1"))
	select {
	case <-notify:
	case <-time.After(time.Second):
		t.Fatalf("подписчик t1 не получил уведомление")
	}
}

// TestNotifierCancelStopsNotifications проверяет, что после отписки уведомления
// больше не приходят.
func TestNotifierCancelStopsNotifications(t *testing.T) {
	notifier := NewNotifier()
	notify, cancel := notifier.Watch(domain.TenantID("t1"))
	cancel()

	notifier.Notify(domain.TenantID("t1"))
	select {
	case <-notify:
		t.Fatalf("отписанный подписчик получил уведомление")
	default:
	}
}

// TestNotifierCoalesces проверяет, что канал подписчика ёмкостью 1 коалесцирует
// несколько сигналов: переполнение не блокирует отправителя.
func TestNotifierCoalesces(t *testing.T) {
	notifier := NewNotifier()
	notify, cancel := notifier.Watch(domain.TenantID("t1"))
	defer cancel()

	// Первый сигнал занимает канал; последующие не блокируются и теряются.
	notifier.Notify(domain.TenantID("t1"))
	notifier.Notify(domain.TenantID("t1"))
	notifier.Notify(domain.TenantID("t1"))

	select {
	case <-notify:
	case <-time.After(time.Second):
		t.Fatalf("подписчик не получил сигнал")
	}
}
