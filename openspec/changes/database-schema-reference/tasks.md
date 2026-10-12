## 1. Каркас справочника и принципы

- [x] 1.1 Создать `docs/database-schema-reference.md` с разделами: принципы, карта схем по зонам, разбор по контекстам, связи, сквозные механизмы, известные расхождения. Проверка: файл существует и содержит все шесть разделов.
- [x] 1.2 Описать принципы хранения (зелёная/красная зоны, schema-per-service, JSONB-снимок + денормализованные колонки, transactional outbox, ось `tenant_id`) со ссылками на `docs/architecture.md` §5 и `docs/implementation-matrix.md` §4. Проверка: каждый принцип подтверждается конкретным кодом (`backend/platform/migrate/apply.go`) или миграцией.

## 2. Разбор по контекстам

- [x] 2.1 Сверить карту схем по зонам с деревом миграций: 12 контекстов и платформенная схема `consoleauth`. Проверка: каждой перечисленной схеме соответствует каталог `backend/contexts/<ctx>/migrations` или `backend/platform/consoleauth/migrations`.
- [x] 2.2 Разобрать `lead-management` (зелёная и красная зоны): `lead_profile`, `search_job`, `search_job_event`, `outbound_message`, `lead_activity`, `email_template`, `lead_assessment`, `contact` — назначение, ключи, денормализованные колонки. Проверка: сверено со всеми 13 файлами `backend/contexts/lead-management/migrations/{green,red}`.
- [x] 2.3 Разобрать `request-fulfillment`, `outreach-orchestration`, `message-delivery`: таблицы, ключи, outbox. Проверка: сверено с файлами миграций этих контекстов.
- [x] 2.4 Разобрать `analytics`, `audience`, `content-production`, `engagement-observation`: таблицы, ключи, outbox. Проверка: сверено с файлами миграций этих контекстов.
- [x] 2.5 Разобрать `data-governance`, `integration-platform`, `platform-governance`, `agent-orchestration`, `consoleauth`: таблицы, ключи, outbox. Проверка: сверено с файлами миграций этих контекстов.

## 3. Связи и сквозные механизмы

- [x] 3.1 Раздел «Связи»: перечислить FK внутри одной схемы (`campaign`→`action_draft`/`operator_decision`, `processing_basis`→`suppression_entry`, `tenant`→`tenant_user`/`audit_record`, `adapter_registration`→`adapter_capability`). Проверка: все `REFERENCES` из миграций перечислены.
- [x] 3.2 Раздел «Связи»: логические связи между контекстами по идентификаторам (`tenant_id`, `company_id`, `lead_id`, `request_id`, `CampaignID`, `approved_action_id`, `correlation_id`/`causation_id`) и диаграмма потока. Проверка: каждый ключ встречается минимум в двух схемах согласно миграциям.
- [x] 3.3 Раздел «Сквозные механизмы»: идемпотентность, оптимистичная блокировка (`version`/`revision`), append-only журналы, частичные индексы, CHECK-инварианты. Проверка: каждое утверждение подтверждено конкретным индексом/ограничением/колонкой из миграций.

## 4. Известные расхождения

- [x] 4.1 Раздел «Известные расхождения»: три пункта — роли schema-per-service (владелец change `service-per-context`), RLS по тенанту (открытый факт), PII красной зоны помимо `contact` (открытый вопрос). Проверка: у каждого пункта есть факт по коду и владелец/статус; явно сказано, что разделение ролей здесь не закрывается.
- [x] 4.2 Явно зафиксировать подчинённость справочника `architecture/domain-catalog.yaml` и `docs/domain-model.md` (описывает хранилище, не владение). Проверка: в шапке документа есть соответствующий абзац.

## 5. Кросс-ссылки и журнал

- [x] 5.1 Добавить ссылку на справочник в `docs/architecture.md` §5 «Модель данных». Проверка: ссылка присутствует и ведёт на существующий файл.
- [x] 5.2 Добавить ссылку на справочник в `docs/implementation-matrix.md` §4 «Персистентность». Проверка: ссылка присутствует и ведёт на существующий файл.
- [x] 5.3 Зафиксировать изменение в `docs/progress-log.md`. Проверка: запись содержит имя change и результат.

## 6. Гейт приёмки

- [x] 6.1 `make check` зелёный (build / test / vet / importlint / archlint / openspec). Проверка: команда завершилась успешно.
- [x] 6.2 `go -C tools/archlint run .` — код возврата 0. Проверка: команда завершилась с кодом 0.
- [x] 6.3 `openspec validate database-schema-reference --strict` — valid. Проверка: команда сообщает `valid`.
