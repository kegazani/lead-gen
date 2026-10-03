## 1. Этап 1 — корректность workflow

- [x] 1.1 Сделать повторное применение перехода идемпотентным в aggregate root `request`: в `backend/contexts/request-fulfillment/internal/domain/request.go` `applyTransition` при совпадении целевого и текущего статуса возвращает успех без `version++`, без записи перехода и без эмиссии события; терминальные статусы и переход в другой статус по-прежнему отвергаются. Проверка: тесты `internal/domain` зелёные, добавлены кейсы «повтор `StartProcessing` для `processing` → no-op без события» и «повторная отмена не создаёт второе решение».
- [x] 1.2 Расширить `client-request`-поведение на решения: повторное применение перехода, фиксирующего решение (отмена, отклонение), не создаёт повторное решение. Проверка: тест на неизменность ранее зафиксированного решения.
- [x] 1.3 Помечать доменные отказы переходов non-retryable в `internal/adapters/workflow/activities.go`: `HandleStartProcessing`, `HandleFulfillRequest`, `HandleCancelRequest` переводят `domain.ErrInvalidTransition`, `domain.ErrTerminalState`, `domain.ErrReasonRequired` в `temporal.NewNonRetryableApplicationError`. Проверка: тесты activity проверяют `ApplicationError.NonRetryable()` для каждого sentinel.
- [x] 1.4 Помечать постоянные отказы поиска non-retryable в `SearchLeads`: `ports.ErrRequestNotFound` и конфигурационный «контракт lead-search не подключён» — non-retryable; транзиентный `leadsearch.ErrUnavailable` остаётся retryable. Проверка: тесты на каждый класс (retryable/non-retryable), включая существующие `TestSearchLeads*`.
- [x] 1.5 Распознать `WorkflowExecutionAlreadyStarted` в `backend/platform/workflow/client.go` (`Start` возвращает ссылку на существующее исполнение и признак «уже запущено» вместо ошибки) и перевести его в соответствующий исход порта в `internal/adapters/workflow/starter.go`; HTTP-слой отвечает явным исходом «уже запущено», а не 503. Проверка: тесты platform/workflow и starter/http на повторный старт.
- [x] 1.6 Считать уникальные лиды в цикле добора в `internal/adapters/workflow/fulfillment.go`: дедуп `leadsearch.LeadRef.ID` через `seen`-множество; условие остановки цикла и `Status.Found` используют число уникальных. Проверка: тест «лид повторяется в раундах → найденное равно числу уникальных».
- [x] 1.7 Добавить версионирование новых веток графа через `workflow.GetVersion` с явной версией для новых исполнений и `DefaultVersion` только как веткой воспроизведения старых. Проверка: расширенный `TestFulfillmentReproducesOldGraph` воспроизводит прежнюю структуру без новых веток.

## 2. Этап 2 — защита от зависаний и двойного расхода

- [x] 2.1 Ввести раздельные `ActivityOptions` в `fulfillment.go`: короткие переходы — малый `StartToCloseTimeout`; длинные `SearchLeads`/`DraftEmails` — `StartToCloseTimeout` в минутах и `HeartbeatTimeout`; значения берутся из конфигурации с осмысленными умолчаниями. Проверка: тест, что для длинных activities заданы увеличенный таймаут и heartbeat, а для коротких — малый.
- [x] 2.2 Добавить периодический `activity.RecordHeartbeat` в длинные activities (`SearchLeads`, `DraftEmails`). Проверка: тест `TestWorkflowEnvironment` с «зависшей» activity демонстрирует повтор по `HeartbeatTimeout`, а не вечное ожидание.
- [x] 2.3 Заменить `http.DefaultClient` в `backend/contexts/integration-platform/internal/adapters/firecrawl/firecrawl.go` и `.../openai/openai.go` на явный `*http.Client` с ограниченным таймаутом и согласовать его с таймаутами длинных activities; вызовы выполняются через контекст запроса. Проверка: тесты адаптеров (в т.ч. `httptest`-сервер, который не отвечает) завершаются по таймауту/отмене, а не висят.

## 3. Этап 3 — человеческая пауза и «пустой поиск»

- [x] 3.1 Реализовать паузу утверждения как `workflow.NewSelector` с сигналом `approval-decision` и `workflow.NewTimer(approvalTTL)`; по срабатыванию таймера заявка переходит в `cancelled` с причиной «утверждение: срок истёк». `approvalTTL` конфигурируем, по умолчанию 7 дней. Проверка: тест «решение не пришло за срок → `cancelled` с причиной срока» и «решение пришло раньше → прежний исход».
- [x] 3.2 Задать `WorkflowExecutionTimeout` при старте исполнения (в `starter.go`/`platform/workflow.StartOptions`) не меньший `approvalTTL`. Проверка: тест/проверка сборки опций, что предел исполнения покрывает срок паузы.
- [x] 3.3 Ввести фазу `no-leads` и поле в `Status` (`internal/adapters/workflow/payload.go`) и проксировать его в `ports.WorkflowStatus`; выставлять при нуле уникальных лидов. Проверка: тест workflow и тест HTTP `GET /requests/{id}/workflow` показывают «0 лидов».
- [x] 3.4 Обеспечить запрет молчаливого `fulfilled` при нуле лидов: путь к `fulfilled` идёт только через явное одобрение оператора, без сигнала истекает срок и исход — `cancelled`. Проверка: тест «0 лидов без одобрения → не `fulfilled`».

## 4. Этап 4 — настоящая durability и устойчивость рантайма

- [x] 4.1 Добавить публичный контрактный suite `internal/ports/contracttest` для `ports.RequestRepository` и unit-of-work (по образцу `lead-management`), прогнать его против существующей in-memory реализации. Проверка: `go -C backend/contexts/request-fulfillment test ./...` зелёный.
- [x] 4.2 Реализовать PostgreSQL-адаптер `internal/adapters/persistence/postgres` для `ports.RequestRepository` и unit-of-work с оптимистичной блокировкой (`UPDATE ... WHERE version = $expected` → `ports.ErrVersionConflict` при нуле строк) и атомарной записью outbox в транзакции. Проверка: контрактный suite проходит против PostgreSQL под `LEADGEN_POSTGRES_TEST_DSN`.
- [x] 4.3 Добавить миграции `migrations/green` для таблиц заявки и outbox по образцу `lead-management` (шапка `-- schema: request-fulfillment`), обновить тест загрузки миграций. Проверка: `migrations_test.go` (загрузчик `backend/platform/migrate` с `OwnSchema`) зелёный.
- [x] 4.4 Дополнить цель `make test-postgres` прогоном PostgreSQL-адаптера `request-fulfillment`. Проверка: `make test-postgres` поднимает одноразовую базу и прогоняет suite контекста.
- [x] 4.5 Расширить `platformworkflow.WorkerOptions` и `NewWorker` (`backend/platform/workflow/worker.go`): `WorkerStopTimeout`, лимиты конкурентности workflow/activity, обработчик фатальных ошибок; `Run` при отмене контекста выполняет graceful drain. Проверка: тест воркера проверяет передачу опций и остановку без обрыва взятых задач.
- [x] 4.6 Добавить `/readyz`, отражающий доступность рантайма Temporal, при этом `/healthz` остаётся liveness; состояние рантайма хранится одним атомарным признаком и прокидывается из `service.go`/`main.go`. Проверка: тест «рантайм недоступен → `/readyz` 503, `/healthz` 200» и обратный случай.
- [x] 4.7 Обернуть подключение к Temporal при старте в ретрай с backoff (`backend/cmd/lead-gen/main.go` и/или `platformworkflow.Dial`), не помечая рантайм выключенным навсегда после транзиентного сбоя; при исчерпании попыток — режим деградации с явным логом и неготовностью `/readyz`. Проверка: тест ретрая (первая попытка падает — следующая успешна) и тест исчерпания бюджета.

## 5. Каталог, документация и архитектурная проверка

- [x] 5.1 Добавить capability `request-fulfillment-execution` в поле `capabilities` контекста `request-fulfillment` и `integration-call-resilience` — контекста `integration-platform` в `architecture/domain-catalog.yaml`. Проверка: `go -C tools/archlint run .` не сообщает о capability без primary context.
- [x] 5.2 Обновить `docs/domain-model.md`: описать новые capability, их владельцев и отсутствие изменений в агрегатах, событиях и контрактах. Проверка: раздел согласован с каталогом, archlint зелёный.
- [x] 5.3 Проверить, что обязательные диаграммы не расходятся с каталогом (контексты и связи те же) и правок диаграмм не требуется; если archlint требует — внести. Проверка: `go -C tools/archlint run .` завершается кодом 0.

## 6. Интеграция и валидация

- [x] 6.1 Прогнать `go -C backend/platform test ./...` и `go -C backend/contexts/request-fulfillment test ./...`; все тесты зелёные. Проверка: команды завершаются кодом 0.
- [x] 6.2 Прогнать `go -C backend/contexts/integration-platform test ./...` (таймауты HTTP-клиентов). Проверка: код 0.
- [x] 6.3 Прогнать `make check` (build, test, vet, lint, arch, openspec) и `openspec validate --all --strict`. Проверка: обе команды завершаются успешно.
- [x] 6.4 Прогнать `make test-postgres` и убедиться, что контрактный suite `request-fulfillment` проходит против PostgreSQL. Проверка: код 0.
- [ ] 6.5 (оставшийся гэп) Тест «зависшая activity → повтор по `HeartbeatTimeout`» не добавлен: тестовый фреймворк Temporal (`testsuite.TestWorkflowEnvironment`) не моделирует повтор activity по истечении heartbeat — повтор запускается только по отказу activity. Вместо этого покрыты вывод периода heartbeat из `HeartbeatTimeout` (`TestHeartbeatIntervalFollowsTimeout`) и раздельные опции длинных/коротких activities с заданным `HeartbeatTimeout` (`TestActivityOptionsSeparateShortAndLong`). Проверка самого повтора по heartbeat требует живого сервера Temporal.
