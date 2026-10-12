# tasks.md — observability-export

Порядок — от адаптера и порта к проводке, инфраструктуре и гейту. Статус задачи
меняется (`- [ ]` → `- [x]`) только по выполненному критерию верификации
(§4 AGENTS.md); заглушка на пути задачи не даёт её закрыть (§3 AGENTS.md).

## 1. Адаптер метрик за портом `Metrics`

- [x] 1.1 Добавить пакет-адаптер `backend/platform/observability/prommetrics`:
  реализация `observ.Metrics{Incr, Observe}` поверх
  `github.com/prometheus/client_golang/prometheus` (`Registry`,
  `CounterVec`/`HistogramVec`), имена метрик и бакеты — в одном месте пакета;
  типы SDK не выходят за пределы пакета. Проверено: `go build ./...` и unit-тест
  адаптера зелёные; `grep` показывает импорт SDK только в этом пакете.
- [x] 1.2 Добавить хендлер `/metrics` процесс-уровня
  (`promhttp.HandlerFor(registry, ...)`) и точку сборки, отдающую его на HTTP;
  метка `service` сохраняется у образцов. Проверено: unit-тест `httptest` на
  GET `/metrics` возвращает `Content-Type: text/plain; version=0.0.4` и тело
  содержит `http_requests_total`.

## 2. Зависимость и runtime-метрики

- [x] 2.1 Добавить `github.com/prometheus/client_golang` в `backend/platform/go.mod`
  (и точки сборки, где нужно) и зафиксировать версию; `go mod tidy` без diff.
  Проверено: `go mod tidy` повторно не меняет файлы, `go build ./...` зелёный.
- [x] 2.2 Регистрировать runtime/SLO-гистограммы в адаптере (реальные бакеты, не
  заглушки) и оставить `observ.MemoryMetrics` тест-двойником. Проверено:
  unit-тест гистограммы фиксирует наблюдения через адаптер; `MemoryMetrics` не
  используется в продуктовой проводке.

## 3. Настраиваемый уровень логирования

- [x] 3.1 В `backend/platform/observability/observ/logger.go` собрать `slog` с
  `slog.LevelVar`; добавить конструктор, принимающий уровень. Проверено:
  unit-тест `debug` пропускает запись, `info` — нет.
- [x] 3.2 Добавить флаг `-log-level` в `backend/cmd/lead-gen/main.go`, распарсить
  значение (`debug`/`info`/`warn`/`error`) и передать в логгер; неизвестное
  значение — ошибка конфигурации. Проверено: тест парсинга уровня; запуск с
  `-log-level=debug` пишет debug-записи.

## 4. Сквозная корреляция

- [x] 4.1 Расширить `observ.Fields` полем `WorkflowID` и выводить его в
  `Fields.attrs()`; протянуть поле через `transport` и workflow. Проверено:
  unit-тест `Fields.attrs()` содержит `workflow_id`; сборка затронутых пакетов.
- [x] 4.2 Логировать жизненный цикл задания в `backend/platform/jobs/jobs.go`
  (взятие/успех/неудача) с `task_id` задания. Проверено: unit-тест раннера
  проверяет записи журнала с `task_id` при успехе и при ошибке.
- [x] 4.3 Логировать публикацию и ошибку в `backend/platform/messaging/outbox/relay.go`
  с корреляцией из записи. Проверено: unit-тест relay на ошибку `Publish`
  проверяет запись лога без потери порядка публикации.

## 5. Relay: ошибки не глотаются

- [x] 5.1 Убрать `_ = r.publishPending(ctx)` в `Relay.Run`; логировать ошибку
  публикации и увеличивать счётчик отказов публикации (через порт `Metrics`),
  не прерывая цикл и не подтверждая запись. Проверено: unit-тест relay —
  при ошибке `Publish` счётчик отказов растёт, запись не подтверждена, `Run`
  продолжает проходы; `grep` не находит `_ = r.publishPending`.

## 6. Валидация кардинальности меток

- [x] 6.1 Повторно реализовать общую валидацию через инъецируемый закрытый
  каталог схем метрик и label policy: имя/вид метрики и точный набор labels,
  конечные значения, допустимые кортежи `port`/`adapter`/`operation`. Каталог
  собирается composition root из реальных emitters; runtime-авторегистрация
  запрещена, `observ` не содержит доменных схем/значений. Проверено: memory и
  Prometheus одинаково отвергают unknown metric, отсутствующие/лишние labels,
  динамические значения и кортеж `lead-search`/`smtp`/`publish`; known schema,
  допустимые кортежи, role/downstream/port-метрики принимаются.
- [x] 6.2 Проверить конечность наблюдений в обоих sinks до создания или изменения
  серии. Проверено: тесты на новую и существующую серию для `NaN`, `+Inf`, `-Inf`
  подтверждают отсутствие прироста/count/sum; конечные значения учитываются.
- [x] 6.3 Проверить неизменность и совместное применение внедрённого каталога:
  изменения входных slices/maps после сборки не меняют политику, оба sinks
  используют одинаковые схемы и tuple catalog. Проверено: тест инъекции и parity
  regression для валидного/невалидного образца.

## 7. Prometheus и таргеты

- [x] 7.1 Добавить сервис `prometheus` в `compose.yaml` (образ, healthcheck,
  публикация не нужна) и статические цели на контейнеры групп, у которых есть
  `/metrics`; пробросить `-prometheus-url http://prometheus:9090` в группу
  `platform`. Проверено: `docker compose config` без ошибок; цель `core:8080`
  (или адрес процесс-уровневого `/metrics`) видна в конфигурации Prometheus.
- [x] 7.2 Привести `deploy/tofu/modules/observability/prometheus.yml.tftpl` в
  соответствие: job `lead-gen-app` скрейпит `/metrics` процесс-уровневых целей,
  список приходит из env-слоя. Проверено: `tofu`/`terraform fmt` без ошибок,
  рендер шаблона валиден.

## 8. Гейт

- [x] 8.1 После закрытия блокеров конечных меток и безопасных ошибок выполнить
  `gofmt` и `make check` (build/test/vet/importlint/archlint/openspec) — зелёный.
  Подтверждено на итоговом diff: свежий `make check` завершился кодом 0; цель
  включает `fmt` (`gofmt -l`) и остальные перечисленные проверки.
- [x] 8.2 После изменений выполнить `go -C tools/archlint run .` — код 0;
  подтвердить, что граница «SDK только в адаптере» не нарушена. Подтверждено:
  команда завершилась кодом 0; импорты `client_golang` обнаружены только в
  `backend/platform/observability/prommetrics/metrics.go`.
- [x] 8.3 После изменений выполнить `openspec validate observability-export
  --strict` — valid. Свежая проверка итоговых артефактов завершилась valid.

## 9. Исправления review

- [x] 9.1 P0: счётчики запуска workflow добавлены в каталог `specs` адаптера
  (`workflow_started_total`/`workflow_start_error_total`/
  `workflow_start_already_started_total`, без высококардинальной метки
  `workflow_id`), а `workflow/client.go` эмитит эти имена без метки; идентификатор
  workflow остаётся в логах. Проверено: тест-инвариант «имя эмитится → есть в
  specs» и тест видимости счётчиков в `/metrics`; `go test -race` пакетов
  `observability/prommetrics` и `workflow` зелёные.
- [x] 9.2 P0/P1: `scrape_targets` в `deploy/tofu/envs/{local,bare-metal}/main.tf`
  указывают на `:8092`, а `-metrics-addr :8092` проброшен в `command` модуля
  `app`. Проверено: `tofu fmt -check` и `tofu validate` зелёные (временные
  `.terraform*` удалены).
- [x] 9.3 P2: счётчик `outbox_publish_failures_total` увеличивается только при
  ошибке `publisher.Publish` (типизированная `publishError`), ошибки чтения
  журнала и `MarkPublished` — только лог. Проверено: unit-тест различает
  источники ошибки; `go test -race` пакета `messaging/outbox` зелёный.
- [x] 9.4 P2: spec-сценарий корреляции согласован с реализацией — задание
  логирует `task_id`, `correlation_id` — у `outbox.Relay`/`transport`. Проверено:
  `openspec validate observability-export --strict` valid.
- [x] 9.5 P2: метод `Debug` добавлен в `observ.Logger` и `slogLogger`, есть
  использование в `main.go`; `-log-level=debug` реально пишет debug-записи.
  Проверено: unit-тест `TestLoggerDebugWritesDebugRecords`; `go test -race`
  пакета `observ` зелёный.

## 10. Безопасная нормализация ошибок заданий

- [x] 10.1 Добавить узкий интерфейс `FailureNormalizer` в `jobs` и внедрить
  владельческий конечный каталог из composition root; нормализовать обычные,
  wrapped и coded ошибки, сохраняя коды SearchJobError/ExportJobError и безопасные
  причины из `search-job-error-codes`. Пустой `CodedError.Message()` и неизвестный
  код дают фиксированный fallback, без `err.Error()`/`Unwrap()` в записи задания.
  Проверено: unit-регрессии для обычной/wrapped/coded-empty ошибок проверяют
  `LastError` и сериализованный JSON log на отсутствие sentinel secret и PII,
  а известные коды и разрешённая безопасная причина сохраняются.
- [x] 10.2 Убрать передачу исходного `handlerErr` в `Logger.Error`; логировать
  только нормализованный безопасный результат вместе с разрешёнными кодами.
  Проверено: JSON-лог на ошибках с sentinel secret/PII не содержит их ни в
  `msg`, ни в `error`, ни в атрибутах.

## 11. Безопасные ошибки outbox relay

- [x] 11.1 Заменить передачу исходных ошибок чтения, публикации и подтверждения
  в `Logger.Error` фиксированной безопасной категорией этапа; не сериализовать
  `err.Error()` или wrapped cause. Проверено: relay regression test с secret/PII
  подтверждает, что JSON-лог содержит safe category и не содержит исходный
  sentinel; счётчик ошибок публикации и повтор публикации сохраняют прежнее
  поведение.

## 12. Повторная верификация blockers

- [x] 12.1 Запустить focused tests для `observ`, `prommetrics`, `jobs`,
  `messaging/outbox` и затронутой composition root; зафиксировать команды и
  успешные результаты. Это не закрывает общие гейты 8.1–8.3.

## 13. Закрытие security-review и актуализация handoff

- [x] 13.1 Закрыть безопасностью полный путь логирования `lead-management`:
  расширить `Config`-проводку так, чтобы один эффективный
  `jobs.FailureNormalizer` (cfg или общий safe fallback) использовали `Runner`,
  `RunSearch` и `progressReporter`; добавить владельческий helper
  `internal/app/search_diagnostics.go`. В `Handle` передавать в фатальный
  `Logger.Error` только `nil` вместо ошибки и нормализованные `error_code`,
  `reason`, `diagnostic`, `task_id`, сохранив возвращаемую ошибку/цепочку и
  `EventError.Code`. Заменить ВСЕ raw errors и небезопасные атрибуты на путях
  `run_search.go` (включая бюджет, search, crawl, extract, вычисление/чтение/
  ревизию/сохранение оценки) и `progress.go`; использовать конечные
  `diagnostic_code`: `budget_record_failed`, `search_request_failed`,
  `crawl_request_failed`, `extract_request_failed`, `assessment_compute_failed`,
  `assessment_read_failed`, `assessment_revision_failed`, `assessment_save_failed`,
  `result_record_failed`. Удалить query, URL и производные domain/company из этих
  логов и соседнего Info «страница обработана», оставив безопасные счётчики,
  `task_id`, `service`/конфигурацию. Не менять глобальный logger, platform,
  `jobs`, `main.go`/runners или бизнес-поведение; `SearchJobError.Error/Unwrap`
  не менять и уточнить комментарии о raw-тексте «для лога».

  Проверить через публичный `Handle` JSON-логгером sentinel secret+PII в обычной,
  wrapped и coded ошибке, а также query/URL: sentinel не встречается нигде,
  spy подтверждает `Logger.Error(err=nil)`, безопасные коды/диагностика есть,
  возвращаемая цепочка, `EventError`, метрики и cancellation сохранены. Покрыть
  все нефатальные ветви и retry `RecordResult`; проверить nil-normalizer fallback
  и идентичность эффективного нормализатора у Runner/RunSearch/reporter. Критерий
  этой задачи — профильные тесты (включая race) и тесты `cmd/lead-gen`, если
  затронуты; финальные gates 8.1–8.3 не запускать для закрытия заранее и не
  закрывать их без независимой проверки итогового diff. Сбой `RecordProgress`/
  `RecordEvent` не менять: его отдельная наблюдаемость — отдельная задача C3.

  Проверенное покрытие:
  `internal/app/search_diagnostics_test.go` содержит тесты публичного `Handle` на
  обычную/wrapped/coded фатальную ошибку, сохранение цепочки, `EventError.Code`,
  nil в `Logger.Error`, метрику и безопасный fallback нормализатора; на
  `assessment_compute_failed` с plain LLM error и sentinel+PII, подтверждая
  неблокирующее поведение и то, что этот фиксированный этап намеренно не вызывает
  `FailureNormalizer`; на cancellation в ожидании `ctx.Done` с `jobs.ErrCancelled`,
  метрикой `cancelled` без `failed` и без `EventError`; на фиксированный
  `assessment_revision_failed` через `logAssessmentWarn` с реальным JSON logger;
  а также на retry `RecordResult`, остальные нефатальные диагностические коды,
  чтение/сохранение оценки и бюджет.

  `service_search_diagnostics_test.go` проверяет собранный `Service.New` через
  публичный `RoleRunner.Run(ctx)` и durable memory job store: distinctive
  нормализатор отражён в JSON-логах `RunSearch` и `jobs.Runner`, `Logger.Error`
  получает nil, в `EventError` остаётся исходный `budget.exhausted`, а в записи
  задания хранится нормализованный безопасный код/текст. Отдельно проверены отмена
  выполняющегося задания через store (status/метрика `cancelled`, без `failed` и
  `EventError`), успешный `страница обработана` Info без query, URL-составляющих,
  domain/company и обязательность Logger. Общая ссылка нормализатора в сборке
  `RunSearch`, `Runner` и `progressReporter` сверена по `service.go`, а ссылка
  `RunSearch`/reporter дополнительно проверяется
  `TestSearchDiagnosticsUsePassedNormalizerInstance`.

  Допустимые reassessment-переходы и валидность снимков покрывает
  `internal/app/search_diagnostics_assembly_test.go`; недопустимый переход без
  мутации проверяет `TestAssessmentInvalidReassessmentDoesNotMutateValidAggregate`.
  Ошибка `ChangeVerdict` не объявляется достижимой через обычный `Handle` при
  валидном сохранённом агрегате. Обычные и race-тесты всего модуля
  `backend/contexts/lead-management` прошли. Независимые финальные reviewers
  `ses_edbabea89` и `ses_edbabea7` подтвердили отсутствие блокеров и ясность
  13.1. Свежее подтверждение итогового diff и покрытие сценариев записаны в
  `verification.md`; гейты 8.1–8.3 выполнены.
- [x] 13.2 Обновить `docs/progress-log.md` результатом третьего цикла проверки, открытым security blocker, статусом change и точным следующим действием; проверить чтением согласованность журнала с этим `tasks.md`, включая открытые задачи и неизменность 8.1–8.3. Исторический handoff третьего цикла выполнен; после четвёртого цикла актуальный статус и следующий шаг обновлены вверху журнала, предыдущая запись сохранена как история.
