## Why

Текущая архитектура организована вокруг процессов развёртывания и BRD-движков, поэтому один `worker` формально владеет несвязанными жизненными циклами, а границы бизнес-модели приходится восстанавливать по нескольким документам. До создания Go-каркаса нужно сделать bounded contexts первичной и машиночитаемой архитектурной осью, иначе структура пакетов закрепит случайную процессную декомпозицию.

## What Changes

- Добавляется main capability `domain-architecture`, задающая обязательный каталог bounded contexts и их DDD-модели: aggregates, aggregate roots, entities, value objects, domain events и policies, а также правила владения, допустимые зависимости и проверку архитектурных изменений.
- Добавляются `architecture/domain-catalog.yaml` и JSON Schema каталога; каталог становится каноническим источником идентификаторов контекстов, их ответственности, aggregate boundaries, владельцев данных, capability, процессов, событий, policies и связей.
- Добавляется самостоятельный Go-модуль `tools/archlint/`: локальная и CI-проверка схемы каталога, агрегатных инвариантов, уникальности владельцев, ссылочной целостности и запрещённых зависимостей. Python-код и Python runtime для этого tooling не используются; корневой `go.mod` приложения остаётся в scope `migrate-platform-to-go`.
- Добавляются `docs/domain-model.md` и четыре PlantUML-диаграммы: карта контекстов, зависимости, основной поток исходящего касания и владение данными по зонам.
- `docs/business-requirements.md`, `docs/architecture.md` и `docs/system-map.md` переходят к DDD-словарю: bounded context — граница модели и владения, процесс — единица развёртывания, движок — функциональная возможность.
- `openspec/config.yaml` получает обязательные правила для будущих changes: выбрать затронутые контексты, обновлять каталог при изменении границ и прогонять `archlint`.
- `migrate-platform-to-go` переводится на context-first структуру Go-пакетов и проверки импортов между контекстами.
- Порядок архивации уточняется: `establish-domain-architecture` архивируется после проверки первым среди foundational changes, чтобы `domain-architecture` появилась в `openspec/specs/` до зависимых реализационных changes; текущие правила `MODIFIED` сохраняются.

## Capabilities

### New Capabilities

- `domain-architecture`: канонические bounded contexts с явными aggregates, aggregate roots, entities/value objects, domain events и policies, единственное владение доменной моделью, явные межконтекстные зависимости и автоматическая проверка архитектурных границ.

### Modified Capabilities

Нет. Поведение существующих продуктовых capability не меняется; они распределяются по контекстам как архитектурная классификация.

## Impact

- **Архитектурные артефакты:** новые каталог, схема, модель домена и четыре диаграммы в `architecture/`.
- **Инструменты:** новый Go-инструмент `tools/archlint/`; проверка не требует Python runtime, сети или production-зависимостей приложения.
- **Документы:** DDD-правки `docs/business-requirements.md`, `docs/architecture.md`, `docs/system-map.md` без изменения продуктового поведения.
- **OpenSpec:** новые правила в `openspec/config.yaml`; согласование `migrate-platform-to-go` и `archive-order-and-rule-deltas`.
- **Будущий код:** Go-пакеты группируются сначала по bounded context, а внутри — по прикладным, доменным, портовым и адаптерным слоям; процессы собирают контексты в composition root и не определяют их границы.
- **Совместимость:** публичных API и production-кода пока нет; миграции данных не требуется.
