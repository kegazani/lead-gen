## 1. Реальный рендер

- [x] 1.1 Реализовать `internal/adapters/renderer/chromium` — порт рендера поверх headless Chromium по CDP (`chromedp`): HTML → PDF и PNG.
- [x] 1.2 Конфигурация запуска (`ExecPath`/`WSURL`, таймауты), нормализация ошибок (нет браузера → честный отказ).
- [x] 1.3 Contract-тесты: при доступном Chromium — реальный рендер PDF/PNG; иначе `t.Skip`. `deterministic` — только тестовый двойник.
- [x] 1.4 Подключить реальный рендер в сборке контекста; `deterministic` не на продуктовом пути.
- [x] 1.5 Проверка: `go -C backend/contexts/content-production build/vet/test ./...`; `go -C tools/archlint run .`.

## 2. Проводка и документация

- [x] 2.1 [централизованно] Подключить конфигурацию рендера в `backend/cmd/lead-gen`.
- [x] 2.2 Обновить `docs/implementation-matrix.md` §0 (снять A2) после верификации; `openspec validate content-rendering --strict`.
