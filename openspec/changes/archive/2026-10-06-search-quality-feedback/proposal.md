## Why

Change `intelligent-lead-operations` реализовал read-модель `lead-quality-insights`
(`analytics`): распределение причин «не подходит», различение «нет сигнала» /
«опровергнуто» / «ошибка извлечения» и рекомендации оператору с основанием. Но
обратная связь в контур поиска осталась открытой (задача 4.5): в `lead-management`
**нет** изменяемого домена цели/источника/лимита — есть только операционная запись
задания поиска (`search_job`, append-only). Поэтому применение рекомендации
невозможно оформить как правку несуществующего агрегата политики поиска, а порт
`analytics/internal/ports.QualityFeedback` без подключённого владельца честно, но
бесполезно отвечает недоступностью.

Дополнительно: блоку `classify` агента (`agent-orchestration`) нужен read-контракт
оценки компании относительно задания. Каталог уже объявляет связь
`lead-relevance-for-agents` и контракт `lead-relevance` у `lead-management`, но
Go-пакета контракта нет — блок не может прочитать вердикт и свидетельства из
существующего `lead_assessment`.

Change закрывает обе дыры **без выдумывания сущностей**: применение рекомендации
выражается как **новый запуск поиска** (`search_job` с рекомендованными
критериями) — это и есть честное «изменение в контуре поиска»; read-контракт —
только чтение существующего агрегата `lead-assessment`.

## What Changes

- **Новая capability `search-recommendation` (владелец `lead-management`).**
  Применение рекомендации анализа как новый запуск поиска поверх существующего
  `search_job`: владелец валидирует структурное изменение, ставит задание с
  рекомендованными критериями, целями и лимитом и возвращает идентификатор
  созданного задания. Применение **идемпотентно**: повтор с тем же ключом
  (тенант, ссылка на рекомендацию, изменение) возвращает уже созданное задание, а
  не создаёт второе; ключ идемпотентности детерминированно задаёт идентификатор
  задания, а уникальность `id` — арбитр (без нового хранилища и миграций).
- **Новый опубликованный контракт `recommendation-apply` (владелец
  `lead-management`).** Структурный DTO изменения (вид, критерий, цели, лимит),
  `Port`, `ErrRejected`/`ErrUnavailable`, `InProc` + сетевой `httpport`; новая
  связь `search-feedback-from-analytics` (`analytics → lead-management`,
  `command · sync · http-json`).
- **Опубликован read-контракт `lead-relevance` (владелец `lead-management`).**
  Чтение вердикта, типа сигнала и свидетельств оценки по паре (задание,
  компания) из существующего агрегата `lead-assessment`; без новых агрегатов и
  сущностей; отказы `ErrRejected`/`ErrNotFound`/`ErrUnavailable`, `InProc` +
  `httpport`.
- **Структурное изменение рекомендации.** Порт `analytics`
  `ports.RecommendationChange` перестаёт нести прозу (`Action string`) и несёт
  **структурное** изменение контура поиска (`SearchChange`); адаптер потребителя
  `analytics` реализует `ports.QualityFeedback` поверх контракта
  `recommendation-apply` и подключается в `Build`, поэтому
  `ApplyRecommendationHandler` перестаёт отдавать безусловный `503`.
- **Каноническая модель обновляется в том же change:** `architecture/
  domain-catalog.yaml` (capability, контракт, связь), `docs/domain-model.md` и
  затронутые диаграммы `architecture/diagrams/*.puml`.

## Capabilities

### New Capabilities

- `search-recommendation`: применение рекомендации анализа качества поиска как
  нового запуска поиска в контуре `lead-management` — структурное изменение
  (критерий, цели, лимит), идемпотентность по ключу, идентификатор созданного
  задания как ссылка на применение; владелец — `lead-management`.

### Modified Capabilities

- `lead-relevance`: добавляется требование опубликованного read-контракта —
  чтение вердикта, типа сигнала и свидетельств оценки по паре (задание,
  компания) для потребителя (блок `classify` агента). Поведение оценки не
  меняется, новых агрегатов не вводится.

## Impact

**Затрагиваемые bounded contexts.**

- `lead-management` — владелец: новая capability `search-recommendation`, новый
  контракт `recommendation-apply`, публикация read-контракта `lead-relevance`,
  поверхность роли `api`.
- `analytics` — потребитель: адаптер `ports.QualityFeedback` поверх контракта
  `recommendation-apply`, структурный `ports.RecommendationChange`, проводка в
  `Build`; read-модель `lead-quality-insights` не меняется.
- `agent-orchestration` — потребитель read-контракта `lead-relevance` (блок
  `classify`); проводка в этом change не выполняется (зона владельца контекста).

**Доменная модель.** Новых агрегатов, сущностей, value objects и domain events не
вводится: применение рекомендации опирается на существующий `search_job`/агрегат
`lead-assessment` остаётся владельцем оценки. Добавляются capability
`search-recommendation`, контракт `recommendation-apply` и связь
`search-feedback-from-analytics`; `docs/domain-model.md` и диаграмма
`architecture/diagrams/context-relationships.puml` обновляются в этом же change.

**Код.** `contracts/lead-management/{recommendationapply,leadrelevance}`; в
`backend/contexts/lead-management` — сценарий применения, фабрики in-proc
адаптеров, эндпоинты роли `api`; в `backend/contexts/analytics` — адаптер
потребителя и структурный порт; проводка в `backend/cmd/lead-gen` (имена API
даются этим change, сама проводка — за точкой сборки).

**Риски.** (1) Идемпотентность держится на детерминированном `id` задания и
уникальности первичного ключа — нового хранилища ключей не заводится. (2)
Read-контракт не должен нести контакты, адреса и содержимое красной зоны: только
вердикт, тип сигнала и свидетельства оценки, принадлежащие `lead-management`.
(3) Оценка не переносится между заданиями: чтение строго по паре (задание,
компания).
