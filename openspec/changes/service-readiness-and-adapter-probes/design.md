## Context

См. мотивацию в `proposal.md` и контракты в `specs/`. Фактические ограничения: `cmd` предоставляет process `/livez`, Compose проверяет его; context `/healthz` — handler liveness; `/readyz` есть только в `request-fulfillment` и `agent-orchestration`. `integration-platform` владеет adapter-status/provider probes. Изменений реализации этим планом ещё не выполнено.

## Goals / Non-Goals

**Goals:**
- Уточнить общие `/livez`, `/healthz` и `/readyz` в существующей `deployment-topology`, не создавая capability.
- Сохранить ownership: каждый контекст определяет readiness policy и signals своих ролей; `integration-platform` обслуживает существующий `adapter-status` и provider probes.
- Сохранять честный неизвестный статус, пока конкретная внешняя проверка не подтверждена и не подключена.

**Non-Goals:**
- Создавать capability `service-health-readiness` или назначать общую readiness capability одному из 12 контекстов.
- Изменять доменные модели, владение или отношения каталога.
- Считать конфигурационное наличие провайдера доказательством доступности.
- Выполнять платные, отправляющие или изменяющие данные health операции.

## Decisions

1. **Общий контракт размещается в существующей capability.** `/livez`, `/healthz` и `/readyz` описываются только delta `deployment-topology`; отдельная `service-health-readiness` не вводится. Альтернатива новой capability отклонена: общие probe routes не образуют отдельную доменную capability.
2. **Роль и capability profile различаются.** Процесс роли (например, worker schedule/consumer lifecycle) оценивается отдельно от capability profile. Каждый контекст владеет перечнем своих required/optional dependencies и endpoint для HTTP-ролей; общий `/readyz` агрегирует только явно required core capability profiles. Неописанная зависимость не становится optional по умолчанию; отсутствующий profile/dependency даёт `unknown`, а не `up`. Нехостящие HTTP delivery роли получают role readiness, но не новый HTTP endpoint.
3. **Готовность учитывает impact scope.** По каждой capability видны `up`, `degraded`, `down` или `unknown` и safe reason. Required `down` или `unknown` делает core profile и `/readyz` not-ready. Optional `down`/`unknown` остаётся видимым как degraded/unknown по capability, но не опускает общий API, если независимые пути обслуживаются. `degraded` не считается `up` ни агрегатором, ни capability consumer.
4. **Adapter status принадлежит `integration-platform`.** Существующий `adapter-status` при configured adapter без `Probe` сообщает `unknown` с safe reason `probe_not_configured`; новая capability не вводится. Потребитель применяет собственную явную required/optional policy.
5. **Probe результат ограничен состоянием и временем.** Каждая probe получает evaluator-owned context с deadline `Config.Timeout`; caller context, включая его values, не передаётся, чтобы отмена/идентичность одного caller не влияли на shared probe. Caller ждёт не дольше собственного deadline или probe timeout, даже если probe игнорирует cancellation. Go-goroutine нельзя принудительно завершить: probe обязана кооперативно завершаться при отмене контекста; иначе её слот остаётся занятым до фактического возврата, а результат для ожидающих — `unknown/probe_timeout`. Максимум одновременно исполняющихся probe — `Config.MaxInflight` (по умолчанию 32); достижение предела для нового ключа без уже существующего flight завершается `unknown/probe_capacity` без запуска goroutine. TTL-кэш ограничен `Config.CacheCapacity` (по умолчанию 256 завершённых snapshots); при заполнении допускается вытеснять только завершённые cache entries, никогда in-flight работу. Если безопасного места нет, результат для нового ключа — `unknown/probe_capacity`. Отмена одного caller прекращает только его ожидание и не отменяет общий probe. Cache TTL/coalescing используют стабильный глобальный ключ зависимости, одинаковый для всех callers; tenant/user/request-specific keys запрещены. Вызовы non-billable, bounded и без мутаций. Без подтверждённого provider contract статус остаётся `unknown`, задача открыта; синтетический `Probe` запрещён.
6. **Внедрение изолируется по контекстам.** Сначала исправляется `adapter-status`, затем role readiness и локальные policies. `/livez` (процесс), `/healthz` (HTTP handler) и `/readyz` (готовность API) остаются разными сигналами; общая composition не подменяет ownership.

## Risks / Trade-offs

- [У провайдера нет подтверждённой бесплатной безопасной операции] → статус остаётся `unknown`, required capability not-ready; задача остаётся открытой.
- [Probe игнорирует отмену контекста] → goroutine невозможно убить; finite `MaxInflight` ограничивает утечку, занятый слот не переиспользуется до фактического возврата, а последующие новые ключи закрываются `unknown/probe_capacity`.
- [Проба даёт ложное ощущение бизнес-доступности] → контракт утверждает только проверенное соединение/health capability, не успех бизнес-операции; тип проверки отражается safe code.
- [Кэш задерживает отражение восстановления/сбоя] → ограничить TTL и проверять верхнюю границу возраста в тестах; readiness не использовать как механизм синхронного выполнения бизнес-запроса.
- [Различия 12 контекстов приводят к неполному покрытию] → задачи охватывают все 12 контекстов и роли; общая библиотека/endpoint не заменяют локальные dependency lists и policies.

## Migration Plan

1. Добавить общий статус/probe contract и тесты, изменить трактовку nil Probe на `unknown`.
2. Инвентаризировать provider adapters и внедрить в `integration-platform` только подтверждённые безопасные реальные пробы; composition использует существующий adapter-status.
3. Реализовать readiness signals и локальные required/optional policies изолированными группами contexts, сохраняя существующие маршруты.
4. Проверить по всем 12 context/role endpoints и конфигурациям Compose; фиксировать непокрытые роли как открытые задачи.
5. Откат: можно отключить readiness polling/новое подключение probes, но нельзя возвращать nil Probe → `up`; неизвестная зависимость остаётся fail-closed для readiness.

## Open Questions

- Для каждого внешнего провайдера требуется подтвердить документированный endpoint/команду auth/health, её биллинговую семантику и побочные эффекты. До подтверждения соответствующая probe задача остаётся открытой, состояние — `unknown`.
- Общие пределы evaluator: `Timeout` — настраиваемый timeout вызова (дефолт 1s); `TTL` — настраиваемый TTL (ноль отключает cache); `CacheCapacity` — конечная ёмкость завершённых cache entries (дефолт 256); `MaxInflight` — конечный предел реально не завершившихся probe goroutines (дефолт 32). Для конкретных провайдеров значения timeout/TTL могут быть строже, но лимиты ёмкости остаются конечными.
