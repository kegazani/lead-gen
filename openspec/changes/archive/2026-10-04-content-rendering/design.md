## Context

`docs/implementation-matrix.md` §0 A2. Мотивация — `proposal.md`.

## Goals / Non-Goals

**Goals:** реальный рендер HTML → PDF/PNG через headless Chromium (CDP, `chromedp`) за существующим портом рендера; конфигурируемый запуск; contract-тест при доступном Chromium.

**Non-Goals:** установка Chromium в среду (внешняя зависимость окружения); живой прогон без браузера (критерий открыт).

## Decisions

### 1. `chromedp` как CDP-клиент

Адаптер `internal/adapters/renderer/chromium` реализует порт рендера поверх `chromedp`. Chromium — внешняя система; путь/эндпоинт задаётся `Config` (`ExecPath` либо `WSURL`). Ошибка запуска — нормализованный отказ, не фиктивный документ.

### 2. `deterministic` — только тесты

Заглушка `deterministic` не собирается в продуктовый путь; остаётся тестовым двойником.

## Risks / Trade-offs

- **[Нет Chromium в окружении]** → Рендер честно недоступен; тесты `t.Skip`; живая приёмка открыта.
- **[Вес зависимости]** → Только `chromedp` (+ транзитивные `gobwas/ws`); приемлемо.

## Migration Plan

1. Добавить адаптер + тесты, не удаляя `deterministic`.
2. В сборке выбирать реальный рендер при заданной конфигурации Chromium; иначе dev-предупреждение.
