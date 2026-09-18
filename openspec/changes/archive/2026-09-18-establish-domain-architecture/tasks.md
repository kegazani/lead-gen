## 1. Канонический каталог

- [x] 1.1 Создать `architecture/domain-catalog.schema.json` с закрытой схемой для contexts, aggregates, aggregate roots, entities, value objects, domain events, policies, relationships и required_diagrams; проверять схему через Go-версию `archlint`, без Python-команд.
- [x] 1.2 Создать `architecture/domain-catalog.yaml` со всеми 14 bounded contexts, действующими capability, deployment targets, aggregates, domain events, policies и контрактами; проверить, что каждый идентификатор уникален и соответствует kebab-case.
- [x] 1.3 Перенести владельцев всех сущностей из `docs/system-map.md` в каталог, заменив процессы на owning contexts и сохранив зоны данных; проверить отсутствие сущностей без владельца и повторных идентификаторов.
- [x] 1.4 Описать направленные relationships между контекстами с pattern, integration и contract; проверить отсутствие self-loop, дубликатов и ссылок на неизвестные контексты.
- [x] 1.5 Для каждого bounded context явно описать aggregates с единственным aggregate root, входящими entities/value objects и invariants, а также domain events и policies; проверить совпадение owning context и ссылочную целостность всех элементов.

## 2. Архитектурный линтер

- [x] 2.1 Реализовать `tools/archlint/` как самостоятельный Go-модуль с `tools/archlint/go.mod`: загрузка каталога через `encoding/json`, проверка используемого подмножества JSON Schema и накопление всех ошибок; проверить канонический каталог командой `go -C tools/archlint run .` и не добавлять Python-код, Python runtime dependency или корневой `go.mod` приложения.
- [x] 2.2 Добавить семантические проверки уникальности, ссылочной целостности, единственного владельца, aggregate root и membership для entities/value objects, producing aggregate для domain events, ссылок policies и корректности relationships; проверить через `go -C tools/archlint test ./...` валидный и намеренно повреждённые каталоги.
- [x] 2.3 Добавить обнаружение capability из `openspec/specs/` и `openspec/changes/*/specs/` и проверку ровно одного primary context; проверить, что текущий набор покрыт без пропусков и дубликатов.
- [x] 2.4 Добавить проверку required_diagrams и маркеров `CTX:<context-id>`, а также `tools/archlint/README.md` с одной командой запуска; проверить тестом отсутствующую диаграмму и неизвестный маркер.

## 3. Модель домена и диаграммы

- [x] 3.1 Создать `docs/domain-model.md` с ubiquitous language, карточками всех bounded contexts, aggregates и их aggregate roots, entities/value objects, domain events, policies, правилами владения, контекстной матрицей capability и правилами эволюции; сверить идентификаторы с каталогом через Go-версию `archlint`.
- [x] 3.2 Создать `architecture/diagrams/bounded-context-map.puml` со всеми контекстами и их классификацией; проверить наличие каждого `CTX:<context-id>` из списка диаграммы.
- [x] 3.3 Создать `architecture/diagrams/context-relationships.puml` с направленными зависимостями и именами контрактов; проверить согласованность ссылок с relationships каталога.
- [x] 3.4 Создать `architecture/diagrams/outreach-flow.puml` для потока пул → аудитория → кампания → утверждение → доставка → наблюдение; проверить, что отправка не обходит `outreach-orchestration`.
- [x] 3.5 Создать `architecture/diagrams/data-ownership.puml` для красной, зелёной и аналитической зон; проверить, что каждая показанная сущность подписана owning context.

## 4. Согласование документации

- [x] 4.1 Обновить `docs/business-requirements.md`: добавить DDD-словарь и отобразить BRD-движки на bounded contexts, не меняя функциональные требования; проверить отсутствие утверждения, что движок является владельцем модели.
- [x] 4.2 Обновить `docs/architecture.md`: сделать context основной модульной границей, а процессы — composition/deployment boundary; проверить согласованность слоёв и будущего Go-дерева с `docs/domain-model.md`.
- [x] 4.3 Обновить `docs/system-map.md`: заменить процессных владельцев сущностей на owning contexts и добавить проекцию context ↔ process; проверить, что у каждой сущности ровно один context и процессы не названы владельцами модели.

## 5. Правила будущих changes и Go-миграция

- [x] 5.1 Обновить `openspec/config.yaml` правилами выбора affected contexts, синхронной правки каталога при изменении границ и обязательного запуска `archlint`; проверить YAML-синтаксис через OpenSpec CLI.
- [x] 5.2 Обновить proposal и design `migrate-platform-to-go` на context-first дерево `internal/contexts/<context-id>/{domain,app,ports,adapters}`; проверить отсутствие целевой общей структуры `internal/domain`, `internal/app` и `internal/ports`.
- [x] 5.3 Обновить tasks `migrate-platform-to-go`: каркас и импортный линтер строятся по каталогу, а межконтекстные импорты разрешаются только через contracts/composition; проверить, что задачи называют `archlint` и owning context.
- [x] 5.4 Уточнить `archive-order-and-rule-deltas`: `establish-domain-architecture` архивируется отдельным подтверждённым foundational-шагом до зависимых реализационных changes, не нарушая порядок рамка → Telegram; проверить непротиворечивость proposal, design и tasks.

## 6. Итоговая проверка

- [x] 6.1 Выполнить `go -C tools/archlint test ./...` и `go -C tools/archlint run .`; обе команды должны завершиться кодом 0, а в `tools/archlint/` не должно быть Python-файлов или зависимости от Python runtime.
- [x] 6.2 Выполнить `openspec validate establish-domain-architecture --strict` и `openspec status --change establish-domain-architecture`; валидация должна пройти, а все планировочные артефакты иметь статус done.
- [x] 6.3 Выполнить проверки согласованности `rg` для старой process-first структуры, процессных владельцев и неописанных capability; каждое оставшееся совпадение должно быть историческим или явно объяснённым.
- [x] 6.4 Зафиксировать отдельный следующий шаг архивации без её выполнения: до команды `openspec archive establish-domain-architecture` требуется новое подтверждение пользователя.

## 7. Согласование канона и представлений

- [x] 7.1 Добавить обязательное поле `invariants` каждому aggregate каталога и потребовать его схемой `architecture/domain-catalog.schema.json`; проверка: у каждого aggregate есть 1–3 инварианта, а `go -C tools/archlint run .` отвергает aggregate без `invariants`.
- [x] 7.2 Добавить в каталог 8 сущностей и 9 агрегатов, чтобы `integration-platform` перестал быть контекстом без агрегатов (13 → 22 агрегата; сущностей 28 → 36, событий 23 → 29, политик 20 → 23); проверка: у каждого из 14 контекстов минимум один aggregate, ссылочная целостность новых событий и политик подтверждена `archlint`.
- [x] 7.3 Перегруппировать агрегаты `campaign` и `lead-profile`, вынеся `call` и `contact` в собственные агрегаты, чтобы aggregate не смешивал платформенное и клиентское хранилище; проверка: событие `call-scheduled` производится агрегатом `call`, а `attribute-provenance` и `email-verification` принадлежат `contact`.
- [x] 7.4 Привести §Тактическая модель в `docs/domain-model.md` к каталогу: удалить параллельную модель и PascalCase-идентификаторы, исправить §Реализация на Go на context-first, дополнить ubiquitous language; проверка: состав агрегатов и идентификаторы документа совпадают с каталогом.
- [x] 7.5 Привести §4 `docs/system-map.md` к 36 сущностям каталога; проверка: у каждой сущности ровно один owning context, процессы владельцами не названы.
- [x] 7.6 Привести диаграммы `architecture/diagrams/*.puml` к канону; проверка: маркеры `CTX:<context-id>` сохранены, `audience-snapshot` и `call` показаны отдельными агрегатами.
- [x] 7.7 Добавить в `tools/archlint/` семантическую проверку «каждый контекст объявляет минимум один агрегат» с тестом; проверка: `go -C tools/archlint test ./...` проходит, а каталог с контекстом без агрегатов отвергается.
