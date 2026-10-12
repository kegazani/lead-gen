## 1. Общий слой: канонический состав навигации

- [x] 1.1 Закрепить инвариант каркаса в `frontend/components`: общая навигация состоит из `ConsoleSections` (Заявки, Исполнение, Лиды, Отчёты, Рассылки) в этом порядке, а `templates/nav.html` не рендерит вторичные группы, если `Nav.Groups` пуст. Проверка: `go test ./frontend/components/...` (тесты состава и каталога сообщений зелёные).
- [x] 1.2 Убедиться, что `ConsoleNavWithContext`/`ConsoleNavMessagesWithContext` остаются единственным способом сборки общей навигации и не добавляют групп. Проверка: поиск по коду потребителей `ConsoleNav*` не находит присвоений `Nav.Groups` вне раздела вторичной навигации; тест общего слоя подтверждает пустые `Groups` по умолчанию.

## 2. Консоль лидов: убрать группу, перенести вторичную навигацию внутрь экранов

- [x] 2.1 Убрать вторичную группу «Экраны лидов» из `consoleSidebar` (`backend/contexts/lead-management/internal/adapters/http/views.go`), оставив только каноническую навигацию. Проверка: `go test ./backend/contexts/lead-management/...`; в HTML экранов лидов нет `<p class="sidebar-group-label">`. — сделано: `consoleSidebar` возвращает только `ConsoleNavWithContext`; проверено `TestLeadsSubnavReplacesSecondarySidebarGroup` и golden-файлами (`grep` — 0 вхождений группы), `go test ./...` контекста зелёный.
- [x] 2.2 Добавить внутриэкранную панель переходов по экранам раздела «Лиды» (Главная, Карточки, Лиды, Воронка) с подсветкой текущего экрана; ссылки строятся через `components.ConsoleHrefWithContext`. Проверка: тест на присутствие панели, подсветку текущего экрана и отсутствие группы в общем сайдбаре. — сделано: `consoleSubnav` + `Page.Subnav` на экранах раздела; проверено `TestLeadsSubnavReplacesSecondarySidebarGroup`, `TestLeadsSidebarStableAcrossScreens`.
- [x] 2.3 Проверить, что ссылки вторичной навигации раздела «Лиды» несут активные `tenant_id` и `actor`. Проверка: тест навигации с непустым контекстом тенанта/актора. — сделано: `ConsoleHrefWithContext` в `consoleSubnav`; проверено `TestLeadsSubnavReplacesSecondarySidebarGroup` (`tenant_id=tenant-config&amp;actor=operator` в ссылках панели).

## 3. Консоль рассылок: убрать группу, перенести вторичную навигацию внутрь экранов

- [x] 3.1 Убрать вторичную группу «Экраны рассылок» из `consoleNav` (`backend/contexts/outreach-orchestration/internal/adapters/http/console.go`), оставив каноническую навигацию. Проверка: `go test ./backend/contexts/outreach-orchestration/...`; в HTML нет группы сайдбара. **Выполнено:** `consoleNav(tenant, actor)` возвращает только `ConsoleNavWithContext(...ConsoleSectionOutreach...)`, `Nav.Groups` не присваивается; неиспользуемый ключ `msgOutreachScreens` удалён. Доказательство: `go test ./...` в `outreach-orchestration` — ok; тест `TestOutreachSubnavReplacesSidebarGroup` подтверждает отсутствие `sidebar-group-label`/«Экраны рассылок» в разметке.
- [x] 3.2 Добавить внутриэкранную панель переходов по экранам раздела «Рассылки» (очередь черновиков, зависшие) с подсветкой текущего и сохранением `tenant_id`/`actor`. Проверка: тест на панель, подсветку и параметры ссылок. **Выполнено:** добавлен `outreachSubnav(active, tenant, actor) *components.Tabs`; `Page.Subnav` проставлен на экранах очереди черновиков, черновика, состава пакетов (`drafts` активен) и зависших (`stalled` активен); ссылки через `ConsoleHrefWithContext` несут `tenant_id`/`actor`. Доказательство: `TestOutreachSubnavReplacesSidebarGroup` проверяет панель, активный экран и параметры ссылок.

## 4. Чистка состава меню без удаления экранов

- [x] 4.1 Убрать из общей навигации пункты «Отправка», «Ассистент», «Форумы»; маршруты и обработчики `/send`, `/assistant`, `/forum-signals` не трогать. Проверка: тест, что в HTML общей навигации нет ссылок на эти адреса. — сделано: пункты были во вторичной группе «Экраны лидов» и удалены вместе с ней; проверено `TestLeadsSubnavReplacesSecondarySidebarGroup` (нет `href="/send`, `href="/assistant`, `href="/forum-signals`).
- [x] 4.2 Подтвердить, что экраны убранных пунктов остаются доступными по прямым адресам. Проверка: `GET /send`, `GET /assistant`, `GET /forum-signals` отвечают как прежде (тест или живой прогон); ни один обработчик не удалён. — сделано: маршруты не трогались; проверено `TestLeadsRemovedMenuScreensRemainReachable` (все три — 200).

## 5. Остальные консоли: канонический состав и ровно один активный раздел

- [x] 5.1 Проверить, что `request-fulfillment` (`internal/adapters/http/server.go`), `analytics` (`internal/adapters/http/*`) и `platform-governance` (`internal/adapters/http/handlers_console.go`) рендерят только канонический состав без групп. Проверка: тесты навигации соответствующих консолей зелёные.
- [x] 5.2 Закрепить, что на каждой консоли активным отмечен ровно один канонический раздел (leads → Лиды; request-fulfillment → Заявки/Исполнение; analytics → Отчёты; outreach → Рассылки). Проверка: тест на подсветку активного раздела по экрану.

## 6. Приёмка, документация и гейт

- [x] 6.1 Обновить затронутые тесты навигации под новый состав: `backend/contexts/lead-management/internal/adapters/http/console_nav_test.go`, `frontend/components/messages_test.go` и тесты консоли рассылок. Проверка: перечисленные тесты зелёные.
- [x] 6.2 Гейт сборки: `make check` (build / test / vet / importlint / archlint / openspec) — зелёный. Проверка: команда завершается кодом 0. **Выполнено:** `make check` — exit 0.
- [x] 6.3 Архитектурная граница: `go -C tools/archlint run .` — код 0; `importlint` — 0 запрещённых рёбер. Проверка: команды возвращают код 0. **Выполнено:** archlint — OK (`12 контекстов / 58 capability / 46 связей`); `importlint` — OK (0 запрещённых рёбер).
- [x] 6.4 Живой прогон на раздельной топологии: переходы между консолями разных портов без визуального скачка сайдбара, активный раздел подсвечен на загруженном экране, убранные пункты отсутствуют, экраны разделов «Лиды» и «Рассылки» достижимы через внутриэкранные панели. Проверка: зафиксированный прогон с описанием наблюдаемого результата.
- [x] 6.5 Подтвердить, что доменная модель не затронута: `architecture/domain-catalog.yaml`, `docs/domain-model.md` и диаграммы править не требуется (агрегаты, capability, владение и связи не меняются). Проверка: перечень затронутых файлов change'а не содержит каталога и доменной модели.
- [x] 6.6 Обновить `docs/progress-log.md` (точка подхвата: сделанное, решения, открытые вопросы). Проверка: запись добавлена.
- [x] 6.7 Валидация change: `openspec validate console-shell-navigation --strict` — valid. Проверка: команда завершается успешно.
