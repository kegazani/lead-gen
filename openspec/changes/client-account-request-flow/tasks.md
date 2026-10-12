# tasks.md — client-account-request-flow

Порядок — от домена к адаптерам и UI. Статус задачи меняется только по критерию верификации (§4 AGENTS.md).

## 1. Домен platform-governance: учётка, идентификация, жизненный цикл, профиль

- [x] 1.1 `TenantUser` получает `passwordHash`; `NewUser` — `Email` и `Password`; логин клиента = email учётной записи. Проверка: `go test ./backend/contexts/platform-governance/...` зелёный, тесты подготовки пользователя покрывают хеш и обязательность email/пароля.
- [x] 1.2 `Tenant` получает поля `inn`, `companyEmail`, `website`, `legalData`; `RegisterTenant` создаёт тенант в состоянии `pending`, а не `active`. Проверка: тест `RegisterTenant` фиксирует `pending` и обязательность идентифицирующих данных.
- [x] 1.3 `TenantState` расширяется `pending`/`rejected`; методы `ApproveRegistration` (`pending→active`) и `RejectRegistration(description)` (`pending→rejected`, описание обязательно). Проверка: тесты переходов и запрета доступа у `pending`/`rejected`.
- [x] 1.4 VO `Profile` (`description`, `target`, `services[]`) и `ProfileStatus` (`empty`/`pending`/`active`/`rejected`); методы `SaveProfile`, `SubmitProfile`, `ApproveProfile`, `RejectProfile`; `SaveProfile` запрещён в `pending`. Проверка: тесты согласования профиля.
- [x] 1.5 Новые ошибки, действия аудита и события для регистрации/профиля. Проверка: тесты событий и аудита.

## 2. Порты и персистенция platform-governance

- [x] 2.1 Метод репозитория `FindByIdentity(inn, companyEmail)` для проверки уникальности. Проверка: контрактный suite in-memory и PostgreSQL.
- [x] 2.2 Снимок и перегидрация новых полей (`TenantSnapshot`, codec, SQL-колонки). Проверка: тест `RehydrateTenant` сохраняет новые поля и состояние.

## 3. App-команды platform-governance

- [x] 3.1 `RegisterClient` с новыми полями (email, пароль, ИНН, email компании, сайт, юрданные) и проверкой уникальности; результат без генерируемого логина. Проверка: тесты регистрации и отклонения дубля.
- [x] 3.2 Команды `ApproveRegistration` / `RejectRegistration`. Проверка: тесты.
- [x] 3.3 Команды `SaveProfile` / `SubmitProfile` / `ApproveProfile` / `RejectProfile`. Проверка: тесты.
- [x] 3.4 Команда входа `AuthenticateClient` (email+пароль по хешу) и `ResetClientPassword` (админ задаёт новый пароль). Проверка: тесты успеха/неверных данных/сброса.

## 4. HTTP-адаптер портала (platform-governance)

- [x] 4.1 Форма регистрации с новыми полями и сообщением «заявка на регистрацию отправлена». Проверка: тесты handler (неполная форма, дубль, успешная подача).
- [x] 4.2 Вход/выход и сессия портала (middleware + server store; cookie `portal_session`; тенант из сессии, не из `?tenant_id`). Проверка: тесты входа и редиректа неаутентифицированного.
- [x] 4.3 Экран профиля: услуги отдельными блоками со свёрнутым/развёрнутым описанием, отправка на согласование, блокировка правки в `pending`. Проверка: тесты и золотые рендеры.
- [x] 4.4 Главный экран: компактное окно со списком услуг и метриками (найдено/взаимодействий/откликов). Проверка: тесты отображения. *(метрики через контракт `campaign-report`; `Report` подключён в composition root; без владельца — честный «—»)*
- [x] 4.5 Боковое дерево истории заявок и SSE-обновление статуса. Проверка: тесты списка и подписки. *(дерево через `requestintent.List`; клиентский SSE — poll-backed по владельцу)*
- [x] 4.6 Сброс пароля: запрос клиента + админский сброс нового пароля. Проверка: тесты потока.

## 5. Домен request-fulfillment: needs-revision и лимиты

- [x] 5.1 Статус `needs-revision` (нетерминальный) и переходы `validating→needs-revision`, `processing→needs-revision`, `needs-revision→submitted`, `needs-revision→cancelled`. Проверка: тесты статусной модели.
- [x] 5.2 Методы `ReturnForRevision(reason)` и `Resubmit()`; события `RequestReturnedForRevision`/`RequestResubmitted`; `ReviseOffer` разрешён в `needs-revision`. Проверка: тесты агрегата.
- [x] 5.3 Репозиторий `CountByTenant` (нетерминальные) и `CountInWork` (`processing`). Проверка: контрактный suite.
- [x] 5.4 Лимиты: `SubmitRequest` — не более пяти нетерминальных; `StartProcessing` — не более одной в работе. Проверка: тесты команд.

## 6. HTTP-адаптер админки (request-fulfillment)

- [x] 6.1 Список заявок компании и переходы: возврат на доработку, отмена с комментарием (из `needs-revision` и `processing`). Проверка: тесты handler и золотые рендеры.
- [x] 6.2 SSE-обновление статуса в админке. Проверка: тесты потока. *(порт `RequestWatcher` + `GET /company/stream` + `company_requests.js`)*

## 7. Каталог, живой прогон, гейт

- [x] 7.1 Обновить `architecture/domain-catalog.yaml`, `docs/domain-model.md` и затронутые диаграммы при изменении состояний/событий агрегатов (новые события `request-returned-for-revision`/`request-resubmitted`, статус `needs-revision`, состояния `pending`/`rejected` тенанта). Проверка: `go -C tools/archlint run .` — код 0.
- [ ] 7.2 Живой сквозной прогон (compose): регистрация → проверка админом → профиль → заявка → статусы/доработка/отмена → живое обновление. Проверка: сценарий воспроизведён, ошибок в логах нет. **Должен включать новый анонимный сценарий:** корень → вход, взаимные CTA вход↔регистрация, навигация анонима, структурированная форма регистрации. *(Анонимный UX готов и входит в этот живой e2e — см. задачи §8; сама 7.2 остаётся открытой до фактического прогона.)*
- [x] 7.3 `make check` (build/test/vet/importlint/archlint/openspec) и `openspec validate client-account-request-flow --strict`. Проверка: зелёный.

## 8. UX анонимного входа и навигации

- [x] 8.1 Корень `/` ведёт `303` на `/login`; тест `TestRootRedirectsToRegister` переименован и обновлён в `TestRootRedirectsToLogin`. Проверка: `go test ./backend/contexts/platform-governance/...` зелёный.
- [x] 8.2 `portalNav(active, authenticated)` и флаг аутентификации во всех точках вызова (`false` на анонимных экранах, `true` на защищённых); `consoleNav` не затрагивается. Проверка: тесты зелёные, `go -C tools/archlint run .` — код 0.
- [x] 8.3 CTA взаимного перехода в `login`/`register`/`reset`/`register_sent`; новые ключи в `messages.go` (значения на русском через каталог; в шаблонах — только `{{ text }}`). Проверка: `TestTemplatesContainNoUserTextLiterals` зелёный.
- [x] 8.4 Секции `fieldset`/`legend` + `Field.Class="span-2"` для `DescriptionField`/`LegalDataField`; CSS-классы `.auth-form`/`.auth-form--narrow`/`.form-section`/`.auth-cta`/`.span-2` в `frontend/components/static/console.css` (в секции форм, брейкпоинт `@media (min-width: 52rem)` для двух колонок). Проверка: `TestRegisterScreen`/`TestRegisterSubmit` зелёные, smoke админки.
- [x] 8.5 Гейт: `make check`, `go -C tools/archlint run .`, `openspec validate client-account-request-flow --strict`. Проверка: зелёный.
