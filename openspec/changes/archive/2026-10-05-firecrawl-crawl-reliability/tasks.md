## 1. Контракт порта crawl

- [x] 1.1 Добавить sentinel `crawl.ErrTimeout` (истечение времени обхода) с
  документацией, отличный от `crawl.ErrCancelled`; страницы возвращаются вместе с
  ошибкой.
- [x] 1.2 Уточнить документацию `crawl.Request.MaxTimeSeconds`: адаптер SHALL
  применять его как жёсткий предел всего обхода.

## 2. Адаптер Firecrawl

- [x] 2.1 `Crawl`: вычислить предельное время обхода из `MaxTimeSeconds` и
  дедлайна контекста, выполнять обход под `context.WithTimeout`.
- [x] 2.2 `pollCrawl`: backoff интервала опроса и предел числа итераций;
  завершение по дедлайну/отмене/пределу без бесконечного цикла.
- [x] 2.3 Нормализовать причину остановки: `crawl.ErrTimeout` при истечении
  времени, `crawl.ErrCancelled` при отмене; вернуть уже собранные страницы.
- [x] 2.4 Best-effort остановка удалённого задания обхода при остановке
  (таймаут и отмена), без зависания на истёкшем контексте.

## 3. Потребители обхода (lead-management)

- [x] 3.1 `SearchLeadsForRequest.crawlSite`: `ErrTimeout` — ограниченная
  остановка сайта; сохранить уже добытые страницы через кратко-ограниченный
  отсоединённый контекст, не роняя шаг.
- [x] 3.2 `RunSearch.crawlURL`: `ErrTimeout` — наравне с `ErrCancelled`
  (сохранение страниц, продолжение задания, без отмены задания).

## 4. Тесты

- [x] 4.1 Unit-тест адаптера: `httptest`-сервер отдаёт незавершающийся crawl
  (`scraping` без `completed`); обход с `MaxTimeSeconds` завершается `ErrTimeout`
  за ограниченное время (не висит). — `TestCrawlHonorsMaxTimeSecondsAgainstHangingCrawl`.
- [x] 4.2 Unit-тест адаптера: отмена контекста прерывает обход и возвращает
  `crawl.ErrCancelled` с уже собранными страницами. —
  `TestCrawlCancellationReturnsPartialPages` (существующий, остаётся зелёным).
- [x] 4.3 Unit-тест адаптера: при остановке по дедлайну возвращаются частичные
  страницы вместе с `ErrTimeout`. — `TestCrawlContextDeadlineKeepsPartialPages`,
  `TestCrawlExpiredDeadlineIsTimeoutNotCancelled`.
- [x] 4.4 Тест `lead-management`: обход с таймаутом сохраняет частичные страницы
  и не роняет шаг/задание (`search_leads.go`, `run_search.go`). —
  `TestSearchLeadsForRequestTimeoutKeepsPartialPages`,
  `TestRunSearchProcessesPagesFromTimedOutCrawl`.
- [x] 4.5 Существующие тесты отмены (`ErrCancelled`) остаются зелёными
  (регрессия семантики отмены).

## 5. Проверка

- [x] 5.1 `gofmt -l` пусто, `go build ./...`, `go vet ./...`, `go test ./...` в
  `backend/contexts/integration-platform` — зелёно.
- [x] 5.2 `go test ./internal/app/...` в `backend/contexts/lead-management` —
  зелёно.
- [x] 5.3 `go build ./...`, `go test ./...` в
  `backend/contexts/request-fulfillment` — зелёно.
- [x] 5.4 `openspec validate firecrawl-crawl-reliability --strict` — valid.
