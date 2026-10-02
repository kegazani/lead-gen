// Package domain — домен контекста request-fulfillment: aggregate root request,
// entity offer, value objects request-status, offer-content и request-target,
// доменные события жизненного цикла заявки и чистые инварианты статусной модели.
//
// Домен не знает о транспорте, хранилище, брокере и внешних системах: ссылки на
// чужие агрегаты хранятся по идентификаторам, а не как указатели. Поля агрегата
// закрыты, состояние меняется только его методами.
package domain
