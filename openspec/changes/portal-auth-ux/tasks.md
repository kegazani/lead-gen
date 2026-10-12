# tasks.md — portal-auth-ux

Порядок — от общего оформления к разметке контекста и живой проверке. Статус
задачи меняется только по критерию верификации (§4 AGENTS.md).

## 1. Общий слой оформления (frontend/components)

- [x] 1.1 Добавить в `frontend/components/static/console.css` классы auth-карточки
  (`.auth-shell`, `.auth-card`, широкий вариант для регистрации) и включить
  карточку в стеклянное оформление. Проверка: `go test ./frontend/components/...`
  зелёный; классы присутствуют в собранном CSS. **Проверено:** тест зелёный, в
  живом `/static/console.css` классы есть.
- [x] 1.2 Убедиться, что каркас без сайдбара центрирует содержимое и не применяет
  grid `body:has(> .sidebar)`. Проверка: рендер страницы без `Sidebar` не содержит
  `class="sidebar"`. **Проверено:** живой `/login` не содержит `class="sidebar"`.

## 2. Анонимные экраны портала (platform-governance)

- [x] 2.1 Анонимные обработчики (`handleLoginForm`, `handleRegisterForm`,
  `renderRegisterError`, `handleResetForm`, `handleResetSubmit`,
  `handleRegisterSubmit`/экран подтверждения) получают пустой `Sidebar` и модель
  с `AuthTabs` (сегментированный выбор «Вход | Регистрация» через фрагмент
  `tabs`). Проверка: handler-тесты зелёные; на анонимных экранах нет бокового
  меню с рабочими разделами. **Проверено:** `renderLogin`/`authTabs` в
  `server.go`, тесты адаптера зелёные.
- [x] 2.2 Шаблоны `login.html`, `register.html`, `reset.html`,
  `register_sent.html` оборачиваются в `.auth-shell > .auth-card`; на входе и
  регистрации выводится переключатель вкладок, на регистрации активна
  «Регистрация». Проверка: `TestTemplatesContainNoUserTextLiterals` зелёный
  (тексты — только через `text`). **Проверено:** живые `/login` и `/register`
  отдают карточку и переключатель с верной активной вкладкой.
- [x] 2.3 Каталог `messages.go` пополняется ключами подписей переключателя
  «Вход»/«Регистрация» (значения на русском). Проверка: catalog-тест зелёный.
  **Проверено:** ключи `portal.auth.login`/`portal.auth.register`, тесты зелёные.
- [x] 2.4 Форма регистрации сохраняет секции «Компания», «Учётная запись»,
  «Реквизиты компании» и `span-2` для широких полей; ширина ограничена
  карточкой. Проверка: `TestRegisterScreen`/`TestRegisterSubmit` зелёные.
  **Проверено:** `TestRegisterFormStructure` зелёный, живой HTML с секциями.
- [x] 2.5 Обновить `portal_nav_internal_test.go` и `portal_flow_test.go` под
  новый анонимный UX (нет рабочего сайдбара; переключатель на входе/регистрации).
  Проверка: `go test ./backend/contexts/platform-governance/...` зелёный.
  **Проверено:** пакеты `...platform-governance` зелёные.

## 3. Аутентифицированный портал без изменений

- [x] 3.1 Подтвердить, что `/portal`, `/profile`, `/portal/request` по-прежнему
  рендерят боковое меню «Портал»/«Профиль»/«Заявка» и переключатель темы.
  Проверка: тест защищённых экранов зелёный. **Проверено:**
  `TestAuthenticatedNavigation` зелёный.

## 4. Живой прогон и гейт

- [x] 4.1 Пересобрать контейнер `platform` и запросить живьём `GET /login` и
  `GET /register`: в ответе нет `class="sidebar"`, есть `.auth-card`, на
  регистрации активна вкладка «Регистрация», поля собраны в секции.
  Проверка: разметка из живого ответа соответствует требованию; ошибок в логах
  нет. **Проверено:** `docker compose build platform` + `up -d platform`;
  живой `/login` — карточка + переключатель, сайдбара нет; `/register` —
  секции и активная вкладка регистрации.
- [x] 4.2 `make check` (build/test/vet/importlint/archlint/openspec) и
  `openspec validate portal-auth-ux --strict`. Проверка: зелёный.
  **Проверено:** `make check` exit 0 (63 passed, 0 failed); archlint OK
  (12 контекстов / 60 capability / 51 связь).
