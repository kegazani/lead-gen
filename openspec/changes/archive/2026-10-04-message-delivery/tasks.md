## 1. Домен, порты и контракт

- [x] 1.1 Расширить `ports.OutgoingMessage` полями темы и тела письма; обновить юнит-тесты порта.
- [x] 1.2 Дополнить `contracts/message-delivery/deliverycommand.Command` необязательными полями темы и тела (аддитивно, minor-версия схемы); обновить `Validate` и контрактный suite.
- [x] 1.3 Обновить `Slice.mapCommand` для переноса темы и тела из контракта в доменную команду.
- [x] 1.4 Убедиться, что `domain.DeliverabilityPolicy` и агрегаты не требуют изменений; при необходимости — уточнить инварианты тестами.

## 2. Реальный почтовый адаптер (A1)

- [x] 2.1 Реализовать `internal/adapters/mailer/provider` — реализацию `ports.Mailer` через контракт `mail` (реальный SMTP-адаптер владельца `integration-platform`).
- [x] 2.2 Нормализовать ответ провайдера в `ports.ProviderResponse`; ошибку транспорта возвращать как `ports.ErrMailerUnavailable`, отказ провайдера — как `failed`.
- [x] 2.3 Тест адаптера против локального SMTP/fake-сервера: успешная отправка, отказ, недоступность (без сети наружу).

## 3. Стоп-лист через suppression-policy

- [x] 3.1 Реализовать `internal/adapters/suppression/policy` — реализацию `ports.SuppressionList` поверх `contracts/data-governance/suppressionpolicy`.
- [x] 3.2 При недоступности источника возвращать ошибку, а не пустой список; покрыть тестом.
- [x] 3.3 Проверить, что проверка стоп-листа выполняется до вызова провайдера (тест порядка).

## 4. PostgreSQL-персистентность (B)

- [x] 4.1 Реализовать `internal/adapters/persistence/postgres` для `MessageRepository` (upsert снимка, поиск по id и по ключу идемпотентности, уникальность ключа).
- [x] 4.2 Реализовать PG-хранилище `SenderReputationRepository`.
- [x] 4.3 Реализовать PG outbox и `UnitOfWork` над `pgx.Tx` (transactional outbox: запись события атомарна с изменением агрегата).
- [x] 4.4 Добавить миграции `backend/contexts/message-delivery/migrations/green/` (схема контекста: сообщения, попытки, репутация, outbox).
- [x] 4.5 Тесты персистентности (в т.ч. `make test-postgres`): рестарт не теряет состояние, повтор не дублирует отправку, клон-изоляция.
- [x] 4.6 Проверить, что in-memory адаптеры остаются только для тестов и dev; на продуктовом пути заглушек нет.

## 5. Роль schedule

- [x] 5.1 Реализовать тикер повторных окон доставки: выбор сообщений с временным исходом и запуск повторной попытки.
- [x] 5.2 Параметры интервала и числа попыток — конфигурацией; покрыть тестом.
- [x] 5.3 Убрать из роли `schedule` сообщение о нереализованности.

## 6. Сборка в cmd/lead-gen

- [x] 6.1 Добавить параметры: DSN зелёной зоны, почтовый провайдер (`mail`/SMTP), стоп-лист (`suppression-policy`).
- [x] 6.2 Собирать PG-персистентность и применять миграции при заданном DSN; mailer и стоп-лист — реальные адаптеры.
- [x] 6.3 Без DSN/провайдера — явный dev-режим с предупреждением в лог; молчаливая подмена заглушкой запрещена.
- [x] 6.4 Тесты сборки сервиса (`build_service_test.go`): реальная сборка и dev-режим.

## 7. Документация и проверки

- [x] 7.1 Обновить `backend/contexts/message-delivery/README.md` (убрать пункты «что осталось заглушкой», закрытые этим change; зафиксировать оставшийся мост).
- [x] 7.2 Обновить `docs/domain-model.md` и `docs/system-map.md` в части `message-delivery`, если описание расходится с реализацией.
- [x] 7.3 Проверить `deploy/services.yaml` (группа `delivery`): роли и механизм соответствуют сборке.
- [x] 7.4 Обновить `docs/implementation-matrix.md` §0: снять пункты A1 и B(`message-delivery`) после верификации.
- [x] 7.5 Прогон гейта приёмки: `make check`, `go -C tools/archlint run .`, `openspec validate message-delivery --strict`.

## Замечание (вне объёма)

- Каталог `architecture/domain-catalog.yaml` объявляет связь `integrations-for-delivery` (и `integrations-for-leads`) через `provider-port`, тогда как код использует реальный контракт `mail` (SMTP-адаптер). Разрыв унаследован от `lead-management` (там тот же паттерн) и относится к отдельному change выравнивания каталога/диаграмм, а не к этому.
- Мост `approved-action → delivery-command` (разрешение ссылок в адрес/контент) — отдельный change (Non-Goal `design.md`).
