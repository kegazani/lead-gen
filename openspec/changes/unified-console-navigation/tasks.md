# tasks.md — unified-console-navigation

Порядок — от общего слоя разделов к навигации, конфигурации и живой проверке.
Статус задачи меняется только по критерию верификации (§4 AGENTS.md). Задачи,
выполненные и подтверждённые гейтом/тестами/живым прогоном, помечены
«Проверено».

## 1. Общий слой: платформенные разделы в каноническом списке

- [x] 1.1 Добавить `ConsoleServicePlatform` и платформенные разделы в
  `ConsoleSections` (`frontend/components/console.go`); подписи — из каталога
  (`msgSection*`). Проверка: `ConsoleNav` возвращает 12 разделов. **Проверено** —
  `ConsoleSections` содержит 12 разделов (`TestConsoleNavShowsBothGroupsOnEveryConsole`).
- [x] 1.2 Дополнить каталог сообщений `frontend/components/messages.go`
  ключами `msgSectionTenant`/`Users`/`Registrations`/`Integrations`/`Audit`/
  `Topology`/`Operations` (русские значения). Проверка: тест подписей зелёный.
  **Проверено** — `go test ./frontend/components/...` ok.
- [x] 1.3 Активность раздела — по ключу и по префиксу адреса; `consoleOwner`
  сопоставляет и по пути раздела. Проверка: тест активности по пути зелёный.
  **Проверено** — `TestConsoleNavMarksActiveByPath` зелёный.

## 2. Платформенный базовый адрес во всех консолях

- [x] 2.1 Добавить `ConsoleServicePlatform` в `components.ConsoleBases` в
  `backend/cmd/lead-gen/main.go`; платформенный префикс уже входит в
  `consolePrefixes`, поэтому `externalConsolePrefixes` пробрасывает его во все
  пять сервисов. Проверка: сборка `cmd/lead-gen`. **Проверено** — флаг
  `-console-platform-base`/`CONSOLE_PLATFORM_BASE_PATH` (дефолт `/admin`);
  `make check` (build) зелёный.
- [x] 2.2 Убрать собственный список платформенных пунктов из `consoleNav`
  (`handlers_console.go`), оставить построение через общий слой. Проверка: тест
  состава навигации без дублей зелёный. **Проверено** — `consoleNav` делегирует в
  `components.ConsoleNavMessagesWithContext`; `TestConsoleNavUnified` зелёный.

## 3. Тесты

- [x] 3.1 Обновить `frontend/components/console_test.go`: навигация содержит
  операторские и платформенные разделы; добавлен тест «на каждой консоли видны
  обе группы разделов» и тест активности по пути. Проверка: `go test
  ./frontend/components/...`. **Проверено** — `TestConsoleNavShowsBothGroupsOnEveryConsole`,
  `TestConsoleNavMarksActiveByPath` зелёные.
- [x] 3.2 Обновить golden-тесты консолей (`-update`). Проверка: golden-тесты
  зелёные. **Проверено** — `make check` (test) зелёный, 67 passed.

## 4. Живой прогон и гейт

- [ ] 4.1 Пересобрать затронутые контейнеры; проверить живьём на
  `/intake/validation`, `/lead-console/`, `/reporting/dashboard`,
  `/mailing/outreach/drafts`: в сайдбаре все 12 разделов, ссылки на одном
  origin, при клике сайдбар не сужается. Проверка: сценарий воспроизведён.
  *Частично:* на `/intake/validation`, `/lead-console/`,
  `/reporting/dashboard` (после входа оператора) сайдбар содержит все 12
  разделов; `/mailing/outreach/drafts` отвечает `503 «очередь черновиков
  недоступна»` — честная недоступность самого экрана (§3 AGENTS.md), не дефект
  навигации; для полного закрытия нужен рабочий экран очереди.
- [x] 4.2 `make check` и `openspec validate unified-console-navigation
  --strict`. Проверка: зелёный. **Проверено** — `make check` exit 0
  (build/test/vet/lint/arch/openspec; 67 passed, 0 failed); `openspec validate
  unified-console-navigation --strict` — valid.
