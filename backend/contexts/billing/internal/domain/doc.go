// Package domain — домен контекста billing: aggregate root subscription-invoice
// (подписка, тарифные лимиты, потребление и счета) и aggregate root success-fee
// (вознаграждение за подтверждённый результат), их value objects, доменные
// события и чистые политики subscription-entitlement и success-fee-eligibility
// (architecture/domain-catalog.yaml).
//
// Домен не знает о транспорте, хранилище, брокере и внешних системах: ссылки на
// чужие агрегаты (тенант, сделка, результат) хранятся по идентификаторам, а не
// как указатели. Поля агрегатов закрыты, состояние меняется только их методами.
package domain
