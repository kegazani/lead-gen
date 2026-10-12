# tasks.md — platform-console-auth

Порядок — от конфигурации к маршрутам и живой проверке. Статус задачи меняется
только по критерию верификации (§4 AGENTS.md).

## 1. Конфигурация и manager

- [x] 1.1 Добавить `ConsoleAuth consoleauth.Config` в `platformgovernance.Config`;
  в `service.go` собрать `consoleauth.Manager`, если задан логин, и передать в
  `httpapi.Dependencies`. Проверка: `go build ./backend/contexts/platform-governance/...`.
  **Проверено.**
- [x] 1.2 Проводка в `buildPlatformGovernance`: логин/пароль оператора и общий
  `Store` (PostgreSQL, схема `consoleauth`). Проверка: сборка `cmd/lead-gen`.
  **Проверено.**
- [x] 1.3 Fail closed для неполной пары `ConsoleAuth.Username`/
  `ConsoleAuth.Password` внутри `platform-governance.Build`, включая прямой путь
  вызова builder; runner builder передаёт конфигурацию, если задан любой компонент;
  не создавать manager с пустым паролем. Проверка: regression tests для
  username-only/password-only на service builder, password-only через runner
  builder, отказа пустого login и сохранения полного empty dev-поведения.
  **Проверено:** прямые Build/runner regression tests, HTTP login с пустым
  паролем не выдаёт cookie/сессию; password whitespace остаётся буквальным.
- [x] 1.4 Принимать пароль только из внешнего `CONSOLE_PASSWORD_FILE`; удалить
  raw `CONSOLE_PASSWORD` ENV и argv-передачу. Проверка: тесты отвергают legacy
  ENV, symlink, non-regular file, неверный owner UID и любой mode кроме owner-only
  `0400`, подтверждают `O_NOFOLLOW`, `fstat` и чтение из того же descriptor;
  сообщения не содержат значения секрета. Документация содержит только file
  path, владельца и mode. **Проверено:** unit/race и container acceptance
  reader’а в app image под UID 65532: `0400` владельца принят; root owner,
  group/world read и symlink отклонены.

## 2. Вход и защита консоли

- [x] 2.1 Маршруты `GET /console/login`, `POST /console/login`,
  `GET /console/logout` и шаблон `console_login.html`; подписи — из каталога.
  Проверка: тест рендера и входа зелёный. **Проверено:** `TestConsoleAuthGuard`.
- [x] 2.2 Middleware `consoleauth` на `/console/**` и `/platform/**`
  (публичны вход и статика; `panels/toggle` исключён — у него свой CSRF-токен);
  при пустом `ConsoleAuth` — прежнее поведение. Проверка: без сессии — редирект
  на вход; с сессией — доступ. **Проверено живьём.**
- [x] 2.3 `consoleActor` берёт субъекта из сессии, затем форму/query/заголовок.
  Проверка: тест субъекта из сессии зелёный. **Проверено.**
- [x] 2.4 `handleConsoleRoot` ведёт на `/console/registrations`. Проверка: тест
  редиректа зелёный. **Проверено живьём** (`/console` → `/console/registrations`).
- [x] 2.5 Формы консоли (`tenant/switch`, `users/roles/*`, `registrations/*`)
  несут CSRF-токен сессии; иначе middleware отклоняет POST. Проверка:
  `approve` без токена → 403, с токеном → 303. **Проверено живьём.**

## 3. Живой прогон и гейт

- [x] 3.1 Проверить живьём acceptance-тестом
  `TestPlatformConsoleAuthLivePostgres`: production `buildPlatformGovernance`,
  реальный HTTP поверх TCP, отдельная помеченная PostgreSQL 17 child database и
  production migrations `platform-governance`/`consoleauth`; пароль читается
  `resolveConsolePassword` из внешнего owner-only `0400` файла текущего runtime
  UID. Тест создаёт pending-заявки через настоящий `POST /register`, проверяет
  анонимный redirect, операторский вход, раздел регистраций, approve/reject с
  формами и CSRF, отсутствие изменений тенанта/audit/outbox/command key при
  пустом/неверном CSRF, активное состояние, audit и новый outbox event
  `tenant-access-changed` с точным `Change` (`tenant-registered` либо
  `tenant-registration-rejected`) при валидном решении,
  публичный вход клиента, отдельные сессии и доступ к `/profile`; логи не должны
  содержать пароль, cookie, CSRF или данные формы. Использовать только явный
  `LEADGEN_PLATFORM_AUTH_ACCEPTANCE=1` и выделенный
  `LEADGEN_PLATFORM_AUTH_ACCEPTANCE_DSN`; при включённом opt-in отсутствие DSN —
  ошибка. Не использовать `admin/admin`, Compose, внешние providers или рабочую
  БД. Команда и требования к sacrificial PG17 описаны в
  `docs/platform-console-auth-acceptance.md`. Миграционный cwd определяется от
  пути test file; cleanup разрешает DROP только для точного уникального имени,
  после успешного CREATE именно этим fixture, при совпадении захваченных OID базы
  и owner и отсутствии соединений. Marker проверяется дополнительно; после сбоя
  COMMENT выполняются повторная проверка/установка, а отсутствие marker может
  быть принято только при полном in-memory доказательстве создания. Любое
  несовпадение marker/owner/OID запрещает DROP и выдаёт инструкцию ручной проверки.
  Cleanup защищён mutex, успешный повтор — no-op, а неудачный DROP сохраняет
  parent connection для повторной попытки. Unit-тесты покрывают COMMENT error,
  foreign marker/identity, transient DROP retry и повторный успешный cleanup.
  Сессионный CSRF извлекается из реально отрисованной HTML-формы;
  все HTTP логи проверяются на секреты, ошибки и 5xx. **Проверено тестером живьём
  (2026-10-11) на отдельном disposable PostgreSQL 17:** opt-in `TestPlatformConsoleAuthLivePostgres`
  прошёл с production `buildPlatformGovernance`, HTTP-входом оператора и паролем
  из внешнего owner-only file; отсутствие/неверный CSRF не изменили состояние,
  audit, outbox или command key; approve/reject создали ожидаемые audit и outbox
  записи; клиентский login/profile сохранили отдельную сессию; секретов в логах нет.
  Подтверждены также `make check`, cmd race tests, archlint и strict validation.
- [x] 3.2 `make check` и `openspec validate platform-console-auth --strict`.
  Проверка: зелёный. **Проверено (2026-10-11):** `make check` exit 0
  (69 OpenSpec checks passed, 0 failed); `go -C tools/archlint run .` exit 0;
  `openspec validate platform-console-auth --strict` valid.
