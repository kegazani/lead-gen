## Why

Живой прогон 2026-10-04: одобрение заявки запускает Temporal workflow `Fulfillment`
→ activity `StartProcessing` → `SearchLeads`. Шаг поиска идёт по контракту
`lead-search` (`lead-management`) и доходит до Firecrawl `POST /v1/crawl`. В логах
`demand` виден только старт обхода:

```
firecrawl: обход: параметры запроса url=https://crmacademy.ru/vnedrenie-crm limit=50 maxDiscoveryDepth=3
```

после чего activity `SearchLeads` не завершается **>12 минут**: нет ни результата,
ни ошибки, ни таймаута, ни ретрая.

**Корень.** Адаптер Firecrawl завершает асинхронный обход только по терминальному
статусу задания (`completed`/`failed`/`cancelled`) или по отмене/дедлайну
контекста вызывающего. Собственного предела времени обхода у адаптера нет, а
поле контракта `crawl.Request.MaxTimeSeconds` **адаптером не используется вовсе**.
Опрос статуса идёт фиксированным интервалом (`pollInterval`) без backoff и без
предела числа итераций: пока движок отвечает «выполняется» (`scraping`), цикл
опроса не завершается. Тогда вызывающий шаг держится до серверного
`StartToCloseTimeout` activity (по флагу — 10 минут) и не приходит ни к результату,
ни к внятной ошибке. Единственный фактический тормоз — дедлайн контекста
вызывающего, а он не гарантирован (например, при вызове через сетевой контракт
без дедлайна) и всё равно не отражает объявленный `MaxTimeSeconds`, который
адаптер молча игнорирует.

## What Changes

- Адаптер Firecrawl `Crawl`: `crawl.Request.MaxTimeSeconds` становится **жёстким
  пределом всего обхода** (а не одного HTTP-запроса); при отсутствии — действует
  дедлайн контекста. Опрос статуса асинхронного задания идёт с **backoff** и
  предельным числом итераций (последний предохранитель от бесконечного цикла).
- Нормализованная причина остановки: новый sentinel контракта `crawl.ErrTimeout`
  (истекло время) отделён от `crawl.ErrCancelled` (отмена вызывающим). Адаптер
  **не маскирует таймаут успехом**: уже собранные страницы возвращаются вместе с
  ошибкой, как и при отмене; удалённое задание best-effort останавливается.
- Потребители обхода (`lead-search` в `lead-management`, воркер `RunSearch`)
  трактуют `crawl.ErrTimeout` так же, как `crawl.ErrCancelled`: это ограниченная
  остановка одного сайта, а не отмена задания — уже добытые страницы сохраняются,
  шаг/задание завершаются успехом в разумное время.
- Дельта спеки `integration-call-resilience`: обход обязан завершаться за
  ограниченное время и не выдавать таймаут за успех.

**Не входит:** правки `backend/cmd/**` (флаги и конфиг таймаутов), контрактов вне
`integration-platform`, миграций, `architecture/**`, `docs/**`.

## Capabilities

### New Capabilities

Нет.

### Modified Capabilities

- `integration-call-resilience`: ADDED — требование о жёстком дедлайне
  асинхронного обхода внешнего провайдера и нормализованной причине его остановки
  (`crawl.ErrTimeout`), с сохранением уже собранных страниц.

## Impact

**Код:** `contracts/integration-platform/crawl` (sentinel `ErrTimeout`),
`backend/contexts/integration-platform/internal/adapters/firecrawl`
(дедлайн обхода, backoff и максимум опроса, нормализация исхода),
`backend/contexts/lead-management/internal/app` (`search_leads.go`, `run_search.go`
— обработка `ErrTimeout`).

**Activity `request-fulfillment`:** шаг поиска уже отправляет heartbeat
(`startHeartbeat`); `StartToCloseTimeout`/`HeartbeatTimeout` длинных activities
задаются политикой исполнения, а её значения приходят из флагов
`backend/cmd/lead-gen` (`-workflow-long-timeout`, `-workflow-heartbeat-timeout`) —
этот файл не меняется. Правка адаптера делает шаг ограниченным по времени заметно
раньше этих серверных таймаутов.

**Домен:** доменная модель, агрегаты, каталог `architecture/domain-catalog.yaml`,
схема данных и `docs/**` не меняются.
