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
- **Value objects capability `landing-page-generator`** (новых сущностей и
  агрегатов нет — каталог не меняется): `brand-kit` (`BrandKit`), `company-profile`
  (`CompanyProfile`, личные контакты отделяются и в наполнение не попадают),
  `lead-form` (`LeadForm`, минимальный состав полей и приёмник в контуре клиента),
  `landing-materials` (`LandingMaterials`), `landing-structure`
  (`LandingStructure`: роли блоков и утверждение оператором).
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
- `RestoreContentVersion` — вернуться к предыдущей версии, публикуя её текст как
  новую неизменяемую версию (политика `immutable-content-version`); возврат к
  текущей версии отвергается.

Запросы (`internal/app/query`): `GetContent`, `GetRenderJob`, `GetLanding`,
`GetPresentation`, `GetLandingPage` (сборка публикуемой страницы и архива),
`ProposeLandingStructure` (перечень блоков по материалам до генерации текстов).
Чтение чужого тенанта неотличимо от отсутствия.

Сборка страницы живёт в `internal/landing` (чистый слой поверх `domain`):
самодостаточный HTML с инлайн-CSS, адаптивная раскладка, метаданные, бренд-комплект
или нейтральное оформление, форма заявки и архив размещения `index.html` +
`styles.css`. Внешних ресурсов страница не требует.

## Порты и адаптеры

Порты (`internal/ports`): `ContentPackageRepository`, `RenderJobRepository`,
`UnitOfWork`/`Transaction`/`Outbox`, `Clock`, `IDGenerator`, `Renderer`.

Адаптеры (`internal/adapters`):

- `persistence/inmemory` — in-memory репозитории поверх снимков
  (`domain.*Snapshot` + `Rehydrate*`) с оптимистичной блокировкой.
- `outbox/inmemory` — transactional outbox и unit-of-work; события кодируются в
  записи `platform/messaging/outbox` и публикуются relay при подключённом брокере.
- `idgen` — генератор `event_id`.
- `renderer/chromium` — **реальный движок** за портом `ports.Renderer`: headless
  Chromium по CDP (библиотека `chromedp`) верстает самодостаточный HTML и
  производит физический артефакт — PDF (документ, презентация) и PNG-превью
  (лендинг). Конфигурация — `Config` (`RendererExecPath`/`RendererWSURL`,
  `RendererTimeout`); недоступность браузера — честный отказ, а не фиктивный
  документ.
- `renderer/deterministic` — **dev/test-двойник**, а не движок. Адаптер не
  обращается к внешнему движку и не содержит лингвистических моделей, а
  детерминированно раскладывает текст версии на блоки. На продуктовом пути он
  остаётся только как режим без сконфигурированного Chromium (с предупреждением в
  логе); реальный движок — `renderer/chromium`.
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
POST /content/packages/{assetID}/versions/{versionID}/restore
GET  /content/packages/{assetID}?tenant_id=
GET  /content/packages/{assetID}/landing-page?tenant_id=&form_action=&format=archive
POST /content/packages/{assetID}/render-jobs
POST /content/produce
POST /content/landing-structure
GET  /content/render-jobs/{renderJobID}?tenant_id=
GET  /healthz
```

## Роли

`service.go` собирает срез из адаптеров и публикует роли:

- `api` — внутренний HTTP-сервер контекста.
- `worker` — воркер рендеринга (забирает `requested`/`rendering`, вызывает движок
  за портом) и, при подключённом брокере, relay публикации событий.

## Что осталось заглушкой

- **Движок рендеринга** подключён реально (`renderer/chromium`): headless Chromium
  по CDP. `renderer/deterministic` остаётся только dev/test-двойником и
  используется, лишь когда Chromium не сконфигурирован (dev-предупреждение в
  логе). Физический артефакт (PDF/PNG) движок производит, но не сохраняет:
  хранилище объектов вне границ среза, поэтому порт отдаёт структурированное
  наполнение, а байты доступны вызывающему через `RenderPDF`/`RenderPNG`.
- Вызов движка в `ProcessRender` выполняется внутри транзакции. Для реального
  внешнего движка вызов обязан быть вынесен за пределы транзакции, а её границы
  спроектированы под идемпотентный повтор.
- Хранилище — только in-memory (Postgres-адаптер и миграции не входят в срез).
- Потребители очередей брокера в срезе не регистрируются: relay публикует, но
  подписчиков нет — это логируется, а не скрывается.
- **Внешние контуры `landing-page-generator` не подключены** и заглушками не
  подменены: сбор сведений с сайта (движок 3.1), генерация текстов
  (`linguistics`), проверка на заимствование по сторонним источникам, транзитный
  приёмник заявок. Что сделано без них: приём и достаточность материалов,
  построение и утверждение структуры, исключение персональных данных, сборка
  страницы и архива, версионирование с возвратом. Генерация текстов остаётся
  нереализованной до подключения `linguistics`; собственного механизма генерации
  модуль не создаёт.

## Проверка

```bash
gofmt -l backend/contexts/content-production contracts/content-production
go -C backend/contexts/content-production test -count=1 ./...
go -C backend/contexts/content-production vet ./...
```
