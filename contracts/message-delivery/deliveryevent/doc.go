// Package deliveryevent объявляет опубликованный контракт delivery-event
// контекста message-delivery: исход доставки сообщения (доставлено или отказ).
//
// Контракт выражен типом и DTO, а не пакетами владельца: потребитель
// (engagement-observation, analytics, lead-management) держит только эту форму и
// не импортирует внутренние пакеты контекста-владельца. Событие публикуется
// асинхронно через transactional outbox после commit изменения агрегата message
// (relationship delivery-to-observation, delivery-to-analytics, delivery-to-lead).
//
// Тело контракта минимально: идентификаторы, непрозрачная ссылка на получателя
// и нормализованный исход ответа провайдера. Адрес получателя, тема и тело
// письма в контракт не попадают (payload-гигиена, data-governance).
package deliveryevent
