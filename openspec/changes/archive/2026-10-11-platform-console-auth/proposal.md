## Why

Консоль управления `platform-governance` (`/console/**`) не имеет входа: она
полагается на субъекта из параметра `?actor=` или заголовка `X-Actor`, а
аутентификация объявлена «вне среза». На практике администратор не может
открыть админку: корень `/console` редиректит на `/console/users` без тенанта и
актора, и обработчик отвечает `400` («не задан тенант»). Раздел «Регистрации»,
подтверждающий заявки, доступен только при вручную добавленном `?actor=admin`.
Другие операторские консоли (заявки, лиды) используют общий серверный вход
`consoleauth`; консоль платформы выпадает из этого механизма.

## What Changes

- **Вход оператора консоли.** `platform-governance` получает вход по общему
  `consoleauth`: `GET/POST /console/login`, `GET /console/logout`, серверная
  сессия в общем cookie `session` и общий `Store` (PostgreSQL). Логин задаётся
  `CONSOLE_USER`; пароль точка входа читает только из внешнего
  `CONSOLE_PASSWORD_FILE`.
- **Защита маршрутов консоли.** `/console/**` и `/platform/**` требуют сессию;
  без неё — редирект на `/console/login`. Публичные: страница входа и статика.
  Клиентский портал (`/login`, `/register`, `/portal/**`) не затрагивается.
- **Субъект из сессии.** Действующий субъект консоли берётся из сессии
  (username), а `?actor=`/`X-Actor` остаются фолбэком для dev и тестов.
- **Рабочий лендинг.** `GET /console` ведёт на `/console/registrations` — раздел
  платформенного оператора, доступный без выбранного тенанта, — а не на битый
  `/console/users`.

## Capabilities

### New Capabilities

Нет.

### Modified Capabilities

- `platform-access`: доступ к консоли управления платформой требует
  аутентифицированного оператора через серверную сессию; субъект действия берётся
  из сессии. Права проверяются на сервере независимо от входа.

## Impact

- **Затрагиваемые bounded contexts:** `platform-governance` (HTTP-адаптер консоли,
  конфигурация сервиса). Общий технический примитив `backend/platform/consoleauth`
  переиспользуется, не меняется.
- **Код:** `backend/contexts/platform-governance/internal/adapters/http/{server.go,
  handlers_console_login.go, templates/console_login.html, messages.go}`,
  `backend/contexts/platform-governance/service.go`, `backend/cmd/lead-gen/runners.go`.
- **Доменная модель:** не меняется.
- **Внешние зависимости:** не добавляются; общий стор сессий уже существует.
