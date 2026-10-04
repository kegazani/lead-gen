# audience

Bounded-контекст `audience` (core, capability `lead-segmentation`): скоринг лида,
сегментация целевой аудитории и фиксация неизменяемого снимка аудитории кампании.

Модель — по `architecture/domain-catalog.yaml` и `docs/domain-model.md`. Это
вертикальный срез: домен, прикладные use cases, порты, in-memory адаптеры,
опубликованные контракты и работающая сборка ролей.

## Домен (`internal/domain`)

Агрегат **`audience-segment`** (root `segment`, сущность `lead-score`):

- value objects: `segment-criteria` (порог оценки, обязательные сигналы, режим
  «все/любой»), `score-value` (оценка в `[0, 1]`);
- событие: `segment-membership-changed` (включение/исключение лида, версия и
  размер сегмента);
- политика: `segment-eligibility` (`policy.go`, чистая функция `Eligible`);
- инвариант «скоринг воспроизводим по версии модели»: сегмент фиксирует
  `model-version`, словарь сигналов и веса задаёт таблица моделей `v1`.

Агрегат **`audience-snapshot`** (root `audience-snapshot`):

- value object: `snapshot-criteria` (выражение сегмента + версия сегмента +
  версия модели на момент фиксации);
- событие: `audience-snapshot-created`;
- инварианты «после фиксации снимок неизменяем» (нет мутаторов, `frozen`) и
  «кампания ссылается на снимок, а не на живой сегмент» (обеспечивает
  потребитель по контракту).

Снимки состояния (`SegmentSnapshot`, `AudienceSnapshotState`) и `Rehydrate*`
восстанавливают агрегаты с проверкой инвариантов и не эмитят события.

## Приложение (`internal/app`)

- `command.CreateSegment` — создание сегмента;
- `command.ScoreLead` — скоринг лида и сегментация (событие при смене состава);
- `command.FixAudienceSnapshot` — фиксация снимка аудитории;
- `command.RefreshAudience` — регулярный пересчёт состава и фиксация снимков
  изменившихся сегментов (роль `schedule`);
- `query.GetAudienceSnapshot` — снимок по идентификатору (отдаётся контрактом и
  HTTP);
- `query.SegmentMembers` — состав сегмента с оценками.

Команды идут через `ports.UnitOfWork`: агрегат и его события пишутся в одной
транзакции (transactional outbox).

## Порты и адаптеры

- `ports`: `SegmentRepository`, `AudienceSnapshotRepository`, `UnitOfWork`,
  `Outbox`, `Clock`, `IDGenerator`; потребляемый контракт `lead-profile` —
  антикоррупционный DTO в `ports/contracts`.
- `adapters/persistence/inmemory` — репозитории сегментов и снимков (тесты и
  dev-режим);
- `adapters/persistence/postgres` — PostgreSQL-репозитории сегментов (оптимистичная
  блокировка) и снимков (upsert), unit-of-work с атомарной записью outbox и
  журнал публикации; схема `audience`, миграции в `migrations/`;
- `adapters/outbox/inmemory` — unit-of-work и кодирование событий в записи
  outbox платформы;
- `adapters/idgen` — генератор `event_id` / `snapshot_id`;
- `adapters/http` — HTTP-адаптер роли `api`.

## Контракты

Опубликованное определение — модуль `lead-gen/contracts/audience`:

- `audiencesnapshot` — Port + DTO снимка (интеграция query, транспорт
  sync/http-json; потребители — `outreach-orchestration`, `request-fulfillment`);
- `segmentmembership` — Port + DTO состава сегмента;
- `envelope` — общий конверт контрактов.

In-proc адаптеры владельца — в `contract.go` (`NewAudienceSnapshot`,
`NewSegmentMembership`).

## Роли и сборка (`service.go`, `audience.go`)

- `api` — HTTP-сервер: `POST /segments`, `POST /segments/{id}/scores`,
  `GET /segments/{id}/members`, `POST /audience-snapshots`,
  `GET /audience-snapshots/{id}`, `GET /healthz`;
- `worker` — публикация доменных событий из outbox через relay при заданном
  издателе; без издателя честно сообщает, что публикация не собрана;
- `schedule` — тикер регулярного пересчёта и фиксации снимков.

## Осталось заглушкой

- Сетевые адаптеры контрактов не реализованы: co-location связывает контракты
  in-proc (`contract.go`), сетевая форма — точка расширения.
- Продуктовая сборка получает PostgreSQL-адаптеры при заданном DSN
  (`Config.Persistence`/`NewPostgresPersistence`); без DSN `Build`/`New`
  собирают in-memory адаптеры (только dev) и предупреждают об этом.
- `worker` без заданного `Config.Publisher` не публикует события.
- Потребление `engagement-event` (поведенческая сегментация) не подключено:
  профиль лида приходит в команду `ScoreLead` как вход контракта `lead-profile`.

## Проверка

```bash
gofmt -l backend/contexts/audience contracts/audience
go -C backend/contexts/audience test -count=1 ./...
go -C backend/contexts/audience vet ./...
go -C tools/importlint run .

# PG-suite адаптеров (нужен Docker; строка подключения — LEADGEN_POSTGRES_TEST_DSN):
LEADGEN_POSTGRES_TEST_DSN='postgres://...' \
  go -C backend/contexts/audience test ./internal/adapters/persistence/postgres/...
```
