## Why

Рендеринг документов/презентаций/лендингов в `content-production` выполняет заглушка `internal/adapters/renderer/deterministic`: PDF/лендинги не производятся реально (`docs/implementation-matrix.md` §0, A2). Изменение реализационное (`skip_specs: true`) — требования capability (`presentation-generator`, `landing-page-generator`, `linguistics`) не меняются, реализуется уже описанное поведение.

## What Changes

- За портом рендера подключается реальный headless Chromium по CDP (библиотека `chromedp`); заглушка `deterministic` остаётся только для тестов/dev.
- Конфигурация (путь к исполняемому Chromium или адрес CDP-эндпоинта, таймауты) — из `Config`; отсутствие браузера даёт честную ошибку, а не фиктивный результат.
- Покрытие рендера тестами, запускаемыми при доступном Chromium; иначе — `t.Skip`.

## Capabilities

### New Capabilities

Нет (`skip_specs: true`).

### Modified Capabilities

Нет.

## Impact

- **Affected bounded contexts:** `content-production` (владелец). Внешняя система — headless Chromium (CDP).
- **Код:** `backend/contexts/content-production/internal/adapters/renderer/**`, сборка контекста; зависимость `github.com/chromedp/chromedp`; проводка — централизованно.
- **Домен:** без изменений.
