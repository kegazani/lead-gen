# tasks.md — registration-approval-console

Порядок — от read-модели к экрану и живой проверке. Статус задачи меняется только
по критерию верификации (§4 AGENTS.md).

## 1. Read-модель списка тенантов

- [x] 1.1 Расширить `ports.TenantSummary` полями проверки компании (`INN`,
  `CompanyEmail`, `Website`, `LegalData`, `Description`, `RegistrationReason`,
  `CreatedAt`, `ProfileStatus`); обновить комментарий порта. Проверка:
  `go build ./backend/contexts/platform-governance/...` зелёный. **Проверено.**
- [x] 1.2 Заполнить новые поля в `inmemory` и `postgres` реализациях `List`
  (для postgres — расширить `selectTenantList` и `Scan`). Проверка:
  `TestRepositoryList` зелёный в обоих адаптерах. **Проверено.**
- [x] 1.3 Пробросить поля через `query.TenantView` и `ListTenantsHandler`.
  Проверка: тест запроса списка зелёный. **Проверено.**

## 2. Раздел доступа и каталог сообщений

- [x] 2.1 Добавить `app.SectionRegistrations` в `access_matrix.go` с правами
  `view`/`manage` → `domain.RightManageTenants`. Проверка: тест матрицы зелёный.
  **Проверено.**
- [x] 2.2 Добавить ключи каталога `messages.go` (пункт меню, заголовки, колонки,
  кнопки, сообщения) со значениями на русском. Проверка: тест каталога зелёный.
  **Проверено.**

## 3. Экран консоли и действия

- [x] 3.1 Объявить маршруты `GET /console/registrations`,
  `POST /console/registrations/{tenantID}/approve|reject`,
  `POST /console/registrations/{tenantID}/profile/approve|reject`.
  Проверка: маршруты собраны, `NewServer` не падает. **Проверено.**
- [x] 3.2 Реализовать обработчики: список заявок и профилей, доступ через
  `platformOperator`, решения через существующие команды, POST/redirect/GET,
  обязательный комментарий при отклонении регистрации. Проверка: handler-тесты
  зелёные. **Проверено:** `handlers_registrations_test.go`.
- [x] 3.3 Добавить пункт `consoleNav` «Регистрации» и шаблон
  `console_registrations.html`. Проверка: `TestTemplatesContainNoUserTextLiterals`
  и тест рендера зелёные. **Проверено.**
- [x] 3.4 Добавить сообщения-подтверждения в `consoleNotice`. Проверка: тест
  зелёный. **Проверено.**
- [x] 3.5 **Проводка прав оператора.** В боевой сборке `platform-governance` не
  задавались `Grants`, поэтому команды одобрения возвращали `403` (право не
  проверяемо). Добавлен публичный `platformgovernance.OperatorGrants` и проводка
  в `buildPlatformGovernance`: консольный оператор получает роль администратора
  платформы. Проверка: живой `approve` возвращает `303`, вход одобренного клиента
  — `303` на `/portal`. **Проверено (до фикса было 403).**

## 4. Тесты и живой прогон

- [x] 4.1 Handler-тесты: pending-заявка видна; `approve` переводит в `active`;
  `reject` без причины не проходит; `reject` с причиной оставляет `rejected`;
  не-оператор получает отказ; профиль на согласовании виден и подтверждается.
  Проверка: `go test ./backend/contexts/platform-governance/...` зелёный.
  **Проверено.**
- [x] 4.2 Пересобрать `platform` и проверить живьём: зарегистрировать клиента →
  заявка видна в `/console/registrations` → одобрить → клиент входит в портал;
  отклонение без причины → 400, с причиной → 303, вход отклонённого → 401.
  Проверка: сценарий воспроизведён, ошибок в логах нет. **Проверено.**
- [x] 4.3 `make check` и `openspec validate registration-approval-console --strict`.
  Проверка: зелёный. **Проверено:** `make check` exit 0 (64 passed, 0 failed);
  archlint OK; validate valid.
