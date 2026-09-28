## 1. Контракт `integration-platform/llm`

- [x] 1.1 Добавить в `contracts/integration-platform/llm/llm.go` тип кадра потока `ConverseDelta` (фрагмент текста, завершающий кадр с `Reply`, `Usage`) и операцию `ConverseStream(ctx, request) iter.Seq2[ConverseDelta, error]` в интерфейс `Port` (верификация: `go build ./...` в модуле `contracts/integration-platform` проходит; добавление метода к `llm.Port` помечено в proposal как **BREAKING**)
- [x] 1.2 Расширить `ConverseReply` структурированными техническими лимитами (`MaxPages`, `MaxDepth`, `Include`, `Exclude`) и обновить `Validate` (верификация: unit-тесты контракта проходят; некорректные лимиты отклоняются). — тип назван `PlanLimits` (`llm.PlanLimits`), тесты `TestPlanLimitsValidate` и расширенный `TestConverseReplyValidate` проходят.
- [x] 1.3 Обновить контрактный suite `llm` под потоковую операцию: сбор `iter.Seq2` в срез, последний кадр несёт `Reply` и `Usage`, ошибка в потоке доходит до потребителя (верификация: `go test ./...` в `contracts/integration-platform` проходит). — добавлен `RunLLMConverseStreamContract`.

## 2. Адаптер владельца и фейки

- [x] 2.1 Реализовать `ConverseStream` в `backend/contexts/integration-platform/internal/adapters/openai` поверх потокового ответа провайдера (DeepSeek), с накоплением реплики и парсингом финального структурированного результата (цели, лимиты, готовность) (верификация: тест адаптера на потоковом ответе-заглушке). — протокол провода: текст реплики, затем разделитель `===PLAN===` и JSON-хвост; тесты `TestConverseStreamEmitsDeltasAndFinal` и `TestLLMConverseStreamContract`.
- [x] 2.2 Обновить тестовые фейки `llm.Port` под новую операцию (верификация: `fakeLLM` в `backend/contexts/lead-management/internal/adapters/http/server_test.go` и `backend/contexts/lead-management/internal/app/dialogue_test.go` компилируются и отдают поток). — обновлены также `contracttest.fakePorts`.

## 3. Прикладной слой `lead-management`

- [x] 3.1 Добавить `ConverseDialogue.StreamReply` поверх `llm.Port.ConverseStream`: валидация истории, предел ходов, недоступность LLM — как единственный кадр-сбой, безопасные логи без текста реплик (верификация: unit-тесты `dialogue_test.go` покрывают предел ходов, отсутствие LLM и отсутствие текста реплик в логах). — тесты `TestStreamReplyYieldsDeltasAndFinal`, `TestStreamReplyUnavailable`, `TestStreamReplyLimit`.
- [x] 3.2 Сохранить существующий синхронный `Reply` до решения по судьбе `Converse` (см. `design.md`, Open Question 1) (верификация: существующие тесты `Reply` продолжают проходить).

## 4. HTTP-адаптер и SSE-эндпоинт

- [x] 4.1 Добавить маршрут `POST /assistant/stream` в `server.go` и обработчик с заголовками `Content-Type: text/event-stream`, `Cache-Control: no-store`, `X-Accel-Buffering: no` и явным `http.NewResponseController(w).Flush()` после каждого кадра (верификация: `httptest`-тест получает кадры до завершения генерации, а не единым телом). — `TestAssistantStreamEmitsFrames` проверяет заголовки и кадры; явный `Flush` после каждого кадра на месте.
- [x] 4.2 Кадры SSE: `delta` (фрагмент текста), `plan` (обновлённый черновик целей и лимитов), `done`/`error`; отмена генерации по `r.Context()` (верификация: тест проверяет порядок и типы кадров; уход клиента отменяет генерацию). — `TestAssistantStreamEmitsFrames` проверяет порядок и типы кадров; отмена идёт по `r.Context()` через `StreamReply`/порт, покрыта контрактным suite.
- [x] 4.3 Настроить таймауты сервера для долгого потока (продление дедлайна через `ResponseController` либо отдельная конфигурация `WriteTimeout`) (верификация: поток не обрывается искусственным `WriteTimeout` в тесте конфигурации). — оба сервера роли `api` (`backend/cmd/lead-gen/runners.go`, `backend/contexts/lead-management/service.go`) задают только `ReadHeaderTimeout`, `WriteTimeout` не задан, поэтому длинный поток не обрезается; продление дедлайна не требуется.
- [x] 4.4 Проверять лимиты плана из тела запроса как недоверенный ввод, как и в текущем `handleAssistantLaunch` (верификация: тест отклоняет некорректные лимиты с понятной ошибкой; принцип из спеки `search-strategy` «Проверка плана, пришедшего от клиента»). — на подтверждённом запуске лимиты по-прежнему проверяет `limitsFromForm` (`TestAssistantLaunchRejectsInvalidLimits`); сам потоковый ход лимитов не принимает и побочных эффектов не создаёт.

## 5. Шаблон и JS-«остров»

- [x] 5.1 Убрать `fieldset.plan-limits` из `templates/assistant.html`; панель плана становится отображением согласованных целей и лимитов, добавлены чипы параметров (верификация: golden-тест шаблона показывает панель без полей формы лимитов).
- [x] 5.2 Обновить модель представления `assistantView` в `views.go`: убрать «форменные» поля лимитов, сохранить готовые строки целей и параметров (верификация: `views_test.go`/`views_progress_test.go` проходят). — скрытые значения лимитов остались для контракта подтверждённого запуска; отображение — чипы `planLimitChip`.
- [x] 5.3 Добавить JS-«остров» (`static/console.js` или отдельный файл, подключённый в `templates.go`): перехват отправки формы, `fetch` к `POST /assistant/stream`, чтение `ReadableStream`, отрисовка `delta`/`plan`/`done`/`error`, объявления через `aria-live` (верификация: ручной сценарий в браузере показывает постепенный вывод; тест разметки не ломает базовую форму). — отдельный файл `static/assistant.js`, подключён в `consoleScripts`.
- [x] 5.4 Сохранить прогрессивное улучшение: без JavaScript форма отправляется обычным `POST /assistant` (верификация: тест `POST /assistant` без заголовков HTMX/JS отдаёт корректную страницу). — `TestAssistantScreen` проходит.

## 6. Доменная привязка и архитектурные проверки

- [x] 6.1 Сверить форму контракта `provider-port` (владелец `integration-platform`, связь `integrations-for-leads`) в `architecture/domain-catalog.yaml` и `docs/domain-model.md`; если документация перечисляет операции порта — обновить её и затронутые диаграммы (верификация: каталог и документация согласованы с новым контрактом; переносов владения и новых capability нет). — каталог и `docs/domain-model.md` называют контракт по имени и операции порта не перечисляют; правок не требуется.
- [x] 6.2 Прогнать обязательные проверки границ и архитектуры: `go run ./tools/importlint` и `go -C tools/archlint run .` (верификация: обе команды завершаются кодом 0; HTTP-адаптер не импортирует `domain`/`app` чужого контекста). — `importlint` зелёный (0 запрещённых рёбер); `archlint` даёт единственную ошибку по capability `email-template-library` из change'а `email-template-library` (к этому change'у не относится).
- [x] 6.3 Проверить отсутствие протечки доменных правил: шаблон и JS-«остров» не содержат доменной логики, лимиты и готовность плана приходят с сервера (верификация: проверка ревью/тест на отсутствие доменных терминов и вычислений в `static/` и `templates/assistant.html`). — `assistant.js` рендерит готовые значения, лимиты и готовность не вычисляет.

## 7. Документы, канон и зависимости

- [x] 7.1 Согласовать порядок с change'ем `console-thermal-relight`: без `connect-src 'self'` поток `fetch` заблокирован (верификация: зависимость зафиксирована; поток работает после применения правки CSP из `console-thermal-relight`). — правка CSP в `console-thermal-relight` выполнена (`server.go`), зависимость снята.
- [x] 7.2 Закрыть открытый вопрос №2 канона: зафиксировать выбор SSE для длительных операций в `openspec/changes/web-frontend-architecture/design.md` (верификация: текст открытого вопроса обновлён; противоречий с `design.md` этого change нет).
- [x] 7.3 Обновить `docs/progress-log.md` по итогам реализации (верификация: запись отражает контракт, прикладной метод, SSE-эндпоинт, модель представления и тесты).
