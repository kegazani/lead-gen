## Why

Синхронный `POST /lead-search` вызывает Firecrawl `Search`, `Crawl` и `Extract` напрямую из `SearchLeadsForRequest`, минуя budget factory и `firecrawl.Guard`. Поэтому этот путь обходит резервирование кредита и общие ограничения аккаунта, действующие для основного `RunSearch`.

## What Changes

- Направить каждый платный вызов Firecrawl из HTTP `/lead-search` через реальный Guard и бюджет задачи, применяя те же account balance, limiter и retry policy, что и основной поиск.
- Не отправлять платный вызов, если стоимость неизвестна/не поддерживается политикой или не может быть зарезервирована; резервировать до попытки и списывать только подтверждённый расход.
- При неизвестном исходе сохранять резерв и блокировать дальнейшие траты в пределах оставшегося бюджета; не считать вызов бесплатным.
- Добавить в публичный контракт `contracts/integration-platform/budget` явный терминальный API `Finish(ctx, taskID)` (или эквивалент), идемпотентный и не закрывающий задачу при незакрытых/неизвестных резервах.
- Ограничить process-local реестр конечной ёмкостью и настройками retention завершённых записей; неизвестные исходы не снимаются aggregate account-balance delta и остаются закреплёнными до точного operation-level evidence, активные записи не вытесняются. При исчерпании ёмкости — отказ до HTTP-вызова Firecrawl.
- Сохранять завершённые scope на настроенный TTL для replay того же task ID; удалять только после истечения TTL и при отсутствии резервов.
- При отмене прекращать запуск новых платных действий, но безопасно обрабатывать и сохранять уже полученные страницы.
- Сохранить существующие HTTP- и in-process семантики `/lead-search`; гарантировать одинаковое бюджетное поведение обоих путей.
- Проверить регрессию через собранный HTTP handler, реальный Firecrawl adapter на контролируемом `httptest` и memory repository; без live-запросов и использования кредитов провайдера.

## Capabilities

### New Capabilities

Не добавляются: изменение уточняет уже существующие capabilities.

### Modified Capabilities

- `firecrawl-credit-budget`: распространить резервацию, оценку стоимости, settlement, account/limiter policy и обработку неопределённого исхода на синхронный `/lead-search`.
- `lead-search`: требовать бюджетный Guard для всех Firecrawl Search/Crawl/Extract в HTTP и in-process путях; определить отказ до сети для неизвестной стоимости и поведение отмены с частичными результатами.

## Impact

- Bounded contexts: `integration-platform` владеет публичным budget contract, Guard и реестром; `lead-management` владеет вызовом `Finish` при terminal settled сценария и HTTP-контрактом поиска. Изменение остаётся внутри этих контекстов и не передаёт владение бюджетом поисковому контексту.
- Ожидаемые области кода: `backend/contexts/lead-management/internal/app/search_leads.go`, сборка сервиса в `backend/cmd/lead-gen`, Firecrawl contracts/wrappers и тесты.
- HTTP wire-форма и бизнес-семантика `/lead-search` не меняются.
- Реестр и reconciliation остаются process-local; после рестарта неизвестные исходы не сохраняются и требуют сверки с аккаунтом Firecrawl. Изменение не обещает production total billing cap, durable/account-wide billing или координацию между процессами. Неопределённый расход не освобождает резерв.
- Внешние системы/провайдеры и существующие финансовые зависимости не меняются; для тестов используются контролируемый HTTP adapter и memory repository, без live Firecrawl.
