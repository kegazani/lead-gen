## 1. analytics

- [x] 1.1 PostgreSQL-адаптер хранилища read-model/отчётов контекста `analytics` (`internal/adapters/persistence/postgres`).
- [x] 1.2 Миграции `backend/contexts/analytics/migrations/`.
- [x] 1.3 Contract-тесты адаптера (`LEADGEN_POSTGRES_TEST_DSN`).
- [x] 1.4 Подключить PG в сборке `analytics` при заданном DSN (в контексте), in-memory — dev.

## 2. audience

- [x] 2.1 PostgreSQL-адаптер хранилищ `audience` (снимок сегмента, членство).
- [x] 2.2 Миграции `backend/contexts/audience/migrations/`.
- [x] 2.3 Contract-тесты адаптера.
- [x] 2.4 Подключить PG в сборке `audience` при заданном DSN.

## 3. content-production

- [x] 3.1 PostgreSQL-адаптер хранилищ `content-production` (запросы, версии, отрендеренные артефакты).
- [x] 3.2 Миграции `backend/contexts/content-production/migrations/`.
- [x] 3.3 Contract-тесты адаптера.
- [x] 3.4 Подключить PG в сборке `content-production` при заданном DSN.

## 4. data-governance

- [x] 4.1 PostgreSQL-адаптер хранилищ `data-governance` (зоны, основания, стоп-лист).
- [x] 4.2 Миграции `backend/contexts/data-governance/migrations/`.
- [x] 4.3 Contract-тесты адаптера.
- [x] 4.4 Подключить PG в сборке `data-governance` при заданном DSN.

## 5. engagement-observation

- [x] 5.1 PostgreSQL-адаптер хранилищ `engagement-observation` (события взаимодействия, сигналы).
- [x] 5.2 Миграции `backend/contexts/engagement-observation/migrations/`.
- [x] 5.3 Contract-тесты адаптера.
- [x] 5.4 Подключить PG в сборке `engagement-observation` при заданном DSN.

## 6. integration-platform

- [x] 6.1 PostgreSQL-адаптер хранилищ `integration-platform` (регистрации адаптеров, вызовы интеграций).
- [x] 6.2 Миграции `backend/contexts/integration-platform/migrations/`.
- [x] 6.3 Contract-тесты адаптера.
- [x] 6.4 Подключить PG в сборке `integration-platform` при заданном DSN.

## 7. platform-governance

- [x] 7.1 PostgreSQL-адаптер хранилищ `platform-governance` (тенанты, пользователи, аудит).
- [x] 7.2 Миграции `backend/contexts/platform-governance/migrations/`.
- [x] 7.3 Contract-тесты адаптера.
- [x] 7.4 Подключить PG в сборке `platform-governance` при заданном DSN.

## 8. Централизованные проверки и точки сборки

- [x] 8.1 Добавить новые контексты в цель `make test-postgres` (`Makefile`; цель покрывает 11 контекстов).
- [x] 8.2 Подключить PG-адаптеры в `backend/cmd/lead-gen` (DSN зелёной зоны) для всех контекстов.
- [x] 8.3 Проверить `docs/implementation-matrix.md` §0 пункт B после верификации; обновить.
- [x] 8.4 Гейт: `make check`, `go -C tools/archlint run .`, `openspec validate context-persistence --strict`.
