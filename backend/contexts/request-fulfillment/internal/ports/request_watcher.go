package ports

import "lead-gen/backend/contexts/request-fulfillment/internal/domain"

// RequestWatcher — подписка на изменения заявок тенанта.
//
// Подписчик получает сигнал «что-то изменилось» без данных: он сам перечитывает
// актуальный список. Порт отделён от RequestRepository намеренно: хранилище
// заявок не обязано уметь рассылать уведомления (например, PostgreSQL), поэтому
// реализацию подписки поставляет адаптер уведомлений поверх транзакции.
type RequestWatcher interface {
	// Watch подписывает наблюдателя на изменения тенанта и возвращает канал
	// уведомлений и функцию отписки. Уведомление не несёт данных.
	Watch(tenantID domain.TenantID) (<-chan struct{}, func())
}
