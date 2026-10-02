# content-production

Bounded context «Производство контента» (поддерживающий, `classification:
supporting`): генерация, версионирование и рендеринг текстов, презентаций,
документов и лендингов. Реализован как работающий вертикальный срез: домен →
порты → адаптеры → приложение → контракты → сборка сервиса.

Состав объявлен каталогом доменной архитектуры `architecture/domain-catalog.yaml`,
`contexts[content-production]`.

## Домен

| Сущность каталога | Где живёт | Что это |
|---|---|---|
| `content-asset` | `ContentPackage` (корень) | контент-пакет |
| `content-version` | `ContentPackage.Versions` | неизменяемая версия текста |
| `landing-content` | `ContentPackage.Landing` | наполнение сгенерированного лендинга |
| `document` | `ContentPackage.Document` | сгенерированный документ |
| `render-job` | `RenderJob` (корень) | задание рендера |

- **Value objects**: `content-locale` (`ContentLocale`), `render-format`
  (`RenderFormat`: `landing-page`, `presentation`, `document`).
- **Доменные события**: `content-version-created` (производит `content-package`),
  `content-rendered` (производит `render-job`).
- **Политика**: `immutable-content-version` — опубликованная версия не
  редактируется; новый текст создаёт новую версию. Выражена структурно (у
  `ContentVersion` нет мутаторов) и зафиксирована `PreservesPublishedVersions`.
- **Инварианты**: версия неизменяема; нумерация версий с единицы; наполнение
  лендинга и документ ссылаются на существующую версию; рендер указывает точную
  версию контента и шаблон; артефакт соответствует формату задания.

Хранилище, транспорт и внешний движок рендеринга домен не знает.

## Прикладной слой

Команды (`internal/app/command`):

- `CreateContentPackage` — создать пакет с первой версией → `content-version-created`.
- `PublishContentVersion` — опубликовать новую неизменяемую версию →
  `content-version-created`.
- `RequestRender` — поставить задание рендера (`requested`).
- `ProduceContent` — обеспечить пакет/версию и поставить рендер; идемпотентна по
  `RequestID` (он же — идентификатор задания). Основа контракта `content-request`.
- `ProcessRender` — исполнить задание воркером: `rendering` → `rendered` (событие
  `content-rendered`) либо `failed`.

Запросы (`internal/app/query`): `GetContent`, `GetRenderJob`, `GetLanding`,
`GetPresentation`. Чтение чужого тенанта неотличимо от отсутствия.

## Порты и адаптеры

Порты (`internal/ports`): `ContentPackageRepository`, `RenderJobRepository`,
`UnitOfWork`/`Transaction`/`Outbox`, `Clock`, `IDGenerator`, `Renderer`.

Адаптеры (`internal/adapters`):

- `persistence/inmemory` — in-memory репозитории поверх снимков
  (`domain.*Snapshot` + `Rehydrate*`) с оптимистичной блокировкой.
- `outbox/inmemory` — transactional outbox и unit-of-work; события кодируются в
  записи `platform/messaging/outbox` и публикуются relay при подключённом брокере.
- `idgen` — генератор `event_id`.
- `renderer/deterministic` — **заглушка**, а не движок. Честное имя: адаптер не
  обращается к внешнему движку и не содержит лингвистических моделей, а
  детерминированно раскладывает текст версии на блоки. Реальные capability
  (`linguistics`, `presentation-generator`, `landing-page-generator`) подключаются
  отдельным адаптером того же порта `ports.Renderer`.
- `http` — HTTP-адаптер роли `api`.

## Контракты

`lead-gen/contracts/content-production/`:

- `contentrequest` — `content-request`: команда производства контента.
- `contentversion` — `content-version`: чтение неизменяемой версии.
- `renderedartifact` — `rendered-artifact`: чтение готового артефакта (лендинг,
  презентация). Уже существовал, не переписывался.

In-proc адаптеры владельца — `contract.go` (`NewContentRequest`,
`NewContentVersion`, `NewRenderedArtifact`). При co-location вызываются в процессе;
при разделении сервисов та же форма даётся сетевым клиентом.

## HTTP (роль `api`)

```
POST /content/packages
POST /content/packages/{assetID}/versions
GET  /content/packages/{assetID}?tenant_id=
POST /content/packages/{assetID}/render-jobs
POST /content/produce
GET  /content/render-jobs/{renderJobID}?tenant_id=
GET  /healthz
```

## Роли

`service.go` собирает срез из адаптеров и публикует роли:

- `api` — внутренний HTTP-сервер контекста.
- `worker` — воркер рендеринга (забирает `requested`/`rendering`, вызывает движок
  за портом) и, при подключённом брокере, relay публикации событий.

## Что осталось заглушкой

- **Движок рендеринга** — `renderer/deterministic`: детерминированная раскладка
  текста, без внешнего движка и без LLM. Это единственная честная заглушка среза.
- Вызов движка в `ProcessRender` выполняется внутри транзакции, потому что движок
  локальный и детерминированный. Для реального внешнего движка вызов обязан быть
  вынесен за пределы транзакции, а её границы спроектированы под идемпотентный
  повтор.
- Хранилище — только in-memory (Postgres-адаптер и миграции не входят в срез).
- Потребители очередей брокера в срезе не регистрируются: relay публикует, но
  подписчиков нет — это логируется, а не скрывается.

## Проверка

```bash
gofmt -l backend/contexts/content-production contracts/content-production
go -C backend/contexts/content-production test -count=1 ./...
go -C backend/contexts/content-production vet ./...
```
