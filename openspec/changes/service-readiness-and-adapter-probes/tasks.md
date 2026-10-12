## 1. Общий контракт и инвентаризация

- [ ] 1.1 Сверить `/livez`, `/healthz`, `/readyz`, роли и process lifecycle всех 12 contexts; записать endpoint coverage отдельно от capability profiles. Non-HTTP роли (включая delivery) получают role readiness без нового HTTP endpoint.
- [ ] 1.2 Добавить общий конечный status/probe contract в `backend/platform/readiness`: профили capability с явными required/optional checks; статусы `up`/`down`/`degraded`/`unknown`; отсутствие required check означает `unknown` и not-ready, пустой профиль не даёт успеха, optional `down`/`unknown` видим как `degraded`, но не блокирует core readiness. Каждый check содержит закрытый reason code, `observed_at` и признак required/optional. Probe ограничена caller deadline и `Config.Timeout` (default 1s), результат кэшируется по TTL и coalesce по стабильному глобальному ключу зависимости, не содержащему tenant/user/request identity. Caller context values не передаются probe: ей передаётся отдельный evaluator-owned timeout context; caller timeout/cancel прекращает только его ожидание и не отменяет shared probe. Cache ограничен `Config.CacheCapacity` (default 256) и содержит только завершённые snapshots; вытеснять in-flight probe запрещено, а невозможность безопасно принять новый результат/ключ даёт `unknown/probe_capacity`. Одновременно реально исполняется не более `Config.MaxInflight` probe (default 32); при насыщении новый ключ получает `unknown/probe_capacity` без запуска goroutine. Если probe не кооперативна, `Evaluate` всё равно возвращает по deadline со статусом `unknown/probe_timeout`, но goroutine и её inflight slot остаются до фактического возврата — Go не позволяет принудительно убить goroutine. Ошибки провайдера переводятся только в фиксированные allowlisted codes без выдачи raw URL, credentials, body или error. Snapshot неизменяемы. Покрыть unit-тестами статусы, required/optional, nil/missing, expiry, concurrent coalescing разных caller contexts по глобальному ключу, отсутствие caller context values, timeout, sanitization, caller cancellation isolation, blocked non-cooperative probe, inflight/cache bounds и отказ вытеснения активной работы. Context-specific профили и интеграции не входят в задачу.
- [ ] 1.3 Исправить существующий `integration-platform` `adapter-status`: configured + nil Probe даёт `unknown` с `probe_not_configured`, никогда `up`; обновить контракт и регрессии без новой capability.
- [ ] 1.4 В каждом context определить владельческий список required/optional dependencies и endpoint каждой HTTP-роли, сверяя реальную сборку. Отсутствующее описание/зависимость даёт unknown, никогда implicit optional/up. Разделить process readiness (worker schedule/consumer lifecycle и т. п.) и capability profiles.

## 2. Реальные probes и composition

- [ ] 2.1 Подтвердить provider contract Firecrawl и реализовать probe только если она bounded, non-billable и без мутаций; иначе `unknown`, задача открыта.
- [ ] 2.2 Подтвердить provider contract LLM и реализовать probe только если она bounded, non-billable и без completion/мутаций; иначе `unknown`, задача открыта.
- [ ] 2.3 Подтвердить SMTP probe contract и реализовать только bounded connect/verify без отправки; иначе `unknown`, задача открыта.
- [ ] 2.4 Инвентаризировать прочие реально подключённые providers/adapters; реализовать только подтверждённые bounded, non-billable, non-mutating probes; прочие оставить `unknown` и с открытой задачей.
- [ ] 2.5 Подключить подтверждённые probes в `backend/cmd/lead-gen/runners.go` к существующему adapter-status `integration-platform` и соответствующей composition; ключ/host сами по себе не означают `up`.

## 3. Process и общие маршруты

- [ ] 3.1 Сохранить `/livez` как process liveness и Compose probe; dependency failure не меняет liveness.
- [ ] 3.2 Сохранить `/healthz` как HTTP handler liveness; не подменять readiness.
- [ ] 3.3 Реализовать `/readyz` HTTP API по явно required core capability profiles; endpoint каждой роли и dependency policy задаёт её context. Required `down`/`unknown` => not-ready; optional `down`/`unknown` => видимый degraded/unknown без down общего API, если независимые пути работают. `degraded` никогда не является `up`; проверить cache/singleflight и отсутствие probe storms.

## 4. Контексты — изолированные группы

- [ ] 4.1 `integration-platform`: существующий status API, безопасные реальные probes и собственные readiness signals/policies ролей; неизвестные providers не маркировать `up`.
- [ ] 4.2 `lead-management` и `request-fulfillment`: каждый context предоставляет собственные readiness signals, dependency lists и policies ролей; проверить API и фактические probes.
- [ ] 4.3 `agent-orchestration` и `outreach-orchestration`: каждый context предоставляет собственные signals, dependency lists и policies, включая durable runtime только там, где он явно required.
- [ ] 4.4 `message-delivery` и `content-production`: каждый context предоставляет собственные signals, dependency lists и policies; delivery role readiness не требует нового HTTP endpoint; не использовать отправку/рендер как probe.
- [ ] 4.5 `platform-governance` и `data-governance`: каждый context предоставляет собственные signals, dependency lists/policies и безопасные runtime checks.
- [ ] 4.6 `audience` и `analytics`: каждый context предоставляет собственные signals, dependency lists/policies и safe dependency status.
- [ ] 4.7 `engagement-observation`: context предоставляет собственные signals, dependency lists/policies и safe dependency status.
- [ ] 4.8 Сверить итоговую матрицу всех 12 contexts/roles с реальными endpoints; не закрывать покрытие при отсутствующих маршрутах или неподключённых probes.
- [ ] 4.9 Проверить все 12 contexts явно: `lead-management`, `audience`, `outreach-orchestration`, `message-delivery`, `request-fulfillment`, `agent-orchestration`, `platform-governance`, `data-governance`, `integration-platform`, `content-production`, `engagement-observation`, `analytics`; для каждого зафиксировать роли, собственные required/optional lists, endpoint status и unknown items.

## 5. Верификация

- [ ] 5.1 Проверить nil Probe => `unknown/probe_not_configured`; отсутствующий/unknown required dependency => not-ready; optional down/unknown видимы, но не опускают API при рабочих независимых путях; `degraded` не трактуется как up; credentials/payload/raw errors не раскрываются.
- [ ] 5.2 Проверить совместимость routes, `/livez` Compose probes, все 12 context endpoints/roles и границы timeout/cache под concurrency.
- [ ] 5.3 Запустить `make check`, `go -C tools/archlint run .` и `openspec validate service-readiness-and-adapter-probes --strict`; подтвердить, что изменение содержит только delta `deployment-topology` и не добавляет capability каталогу. Не отмечать задачи внешних probes выполненными без подтверждённой реальной операции.
