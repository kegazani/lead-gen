# Контекст `engagement-observation`

Go-модуль `lead-gen/backend/contexts/engagement-observation` — реализация bounded
context `engagement-observation` из `architecture/domain-catalog.yaml`. Каталог
назван так же, как контекст и сервис; имя Go-пакета — `engagementobservation`
(идентификатор пакета не может содержать дефис).

Контекст квалифицирует сырые сигналы доставки, переходов и ответов в события
взаимодействия **без трекинговых пикселей**: события приходят от провайдера,
письма или события доставки, но не от пикселя. Открытие письма измеряется
пикселем, а policy `pixel-free-observation` такой сигнал не квалифицирует.

## Соответствие контексту

| Каталог | Слой | Содержимое |
|---|---|---|
| корень модуля | сборка сервиса | `ServiceID = "engagement-observation"`, `New(Config)`, раннеры ролей `ingest` и `worker`, конструкторы адаптеров опубликованных контрактов |
| `internal/domain` | домен | агрегат `engagement` (AR `engagement-event`), VO `engagement-kind` и `signal-confidence`, событие `engagement-observed`, policy `pixel-free-observation` |
| `internal/app` | приложение | use cases `IngestEvent`, `RecordTransition`, `GetEngagement` |
| `internal/ports` | порты | хранилище событий, журнал дедупликации сигналов, реестр трекинг-ссылок, outbox, unit-of-work, часы и генератор идентификаторов |
| `internal/adapters` | адаптеры | in-memory хранилище/журнал/реестр, unit-of-work с outbox, транспортный потребитель события доставки, публичный HTTP-адаптер роли `ingest` |

Публикуемые контракты контекста (`engagement-event`, `read-signal`) — отдельный
модуль `lead-gen/contracts/engagement-observation`. Потребители импортируют
контракт и не видят внутренние пакеты владельца.

## Инварианты

- **Входящий webhook сначала дедуплицируется.** Приём сигнала в одной транзакции
  проверяет журнал дедупликации, сохраняет событие и только затем отмечает сигнал
  обработанным; повтор не создаёт второго события.
- **Сырой сигнал не равен квалифицированному событию.** `domain.RawSignal` —
  вход, `domain.Qualify` — единственный способ получить агрегат, поэтому
  квалификация всегда проверяет вид, исход и уверенность.
- **Пиксель не квалифицируется.** Вид из набора `open`/`pixel`/`view`/
  `impression` отвергается `ErrPixelBasedKind` и в домене, и в контракте.

## Роли

- `ingest` — публичный HTTP-сервер приёма:
  - `POST /events` — принять сырой сигнал (`202` новое, `200` повтор,
    `422` пиксельный сигнал, `400` некорректный запрос);
  - `GET /r/{ref}` — разрешить трекинг-ссылку (`read-signal`), учесть переход и
    увести на адрес назначения (`302`, `404` HTML для неизвестной ссылки);
  - `GET /engagements/{id}?tenant_id=...` — прочитать зафиксированное событие;
  - `GET /healthz` — проверка живости.
- `worker` — публикация событий взаимодействия (relay transactional outbox) и
  подписка на очередь `delivery-event` контекста `message-delivery`. Без
  подключённого брокера роль честно сообщает об отсутствии источника и не делает
  вид, что обрабатывает сообщения.

## Границы

`domain` не импортирует `app`, `ports`, `adapters`, другие контексты и
`backend/platform`. `app` видит свой `domain`, свои `ports` и опубликованные
контракты. `ports` видит контракты и свой `domain`. Драйверы хранилищ и брокера
допустимы только в `adapters`. Межконтекстно контекст читает только
`lead-gen/contracts/...`. Границы проверяет `go -C tools/importlint run .`.

## Объём среза

Реализованы домен, use cases, порты и адаптеры. Хранилище событий, журнал
дедупликации, реестр ссылок и outbox доступны и в PostgreSQL
(`internal/adapters/persistence/postgres`, схема `engagement-observation`,
миграции `migrations/`), и в памяти. In-memory-адаптеры остаются тестовым и
dev-носителем: продуктовая сборка получает PostgreSQL-адаптеры
(`NewPostgresPersistence`) при заданном DSN зелёной зоны, без DSN — in-memory
dev-режим с предупреждением. Транспортный потребитель разбирает тело события
доставки в форме `delivery-event`; контракт `delivery-event` у владельца
(`message-delivery`) ещё каркас волны 0, поэтому форма разбирается адаптером и
уточнится, когда владелец наполнит контракт.

## Проверка

```sh
gofmt -l backend/contexts/engagement-observation contracts/engagement-observation
go -C backend/contexts/engagement-observation test -count=1 ./...
go -C backend/contexts/engagement-observation vet ./...
go -C tools/importlint run .
```

PostgreSQL-адаптеры проверяются отдельно против реальной базы (иначе тесты
пропускаются):

```sh
LEADGEN_POSTGRES_TEST_DSN='postgres://...?sslmode=disable' \
  go -C backend/contexts/engagement-observation test ./internal/adapters/persistence/postgres/...
```
