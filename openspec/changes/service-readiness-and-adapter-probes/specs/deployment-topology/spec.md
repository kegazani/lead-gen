## MODIFIED Requirements

### Requirement: Пробы живости и готовности

Процесс SHALL предоставлять `/livez` как process liveness, не зависящий от зависимостей. HTTP context SHALL сохранять `/healthz` как handler liveness. `/readyz` SHALL отражать readiness API по явно required core capability profiles; каждый из 12 bounded contexts SHALL владеть собственным endpoint/role readiness, списками required/optional dependencies и signals. Неперечисленная или отсутствующая dependency/profile SHALL NOT считаться optional или up: её состояние `unknown`. Общая семантика маршрутов не вводит capability и не создаёт централизованный dependency list. Provider status SHALL использовать существующий контракт `adapter-status` владельца `integration-platform`. Наличие маршрута или ключа SHALL NOT подтверждать readiness. Durable runtime включается в role dependencies только если владелец объявил его required. Compose SHALL проверять `/livez`, а не dependency readiness.

#### Scenario: Process liveness отдельно от зависимостей
- **WHEN** внешняя зависимость недоступна, но процесс отвечает
- **THEN** `/livez` остаётся успешным, а соответствующий `/readyz` может сообщать not-ready

#### Scenario: Compose probe
- **WHEN** Compose проверяет здоровье процесса
- **THEN** он использует `/livez`, а не `/readyz`

#### Scenario: Readiness требует подтверждённые required dependencies
- **WHEN** required dependency отсутствует, недоступна или имеет состояние `unknown`
- **THEN** роль не объявляется готовой

#### Scenario: Политика готовности принадлежит контексту
- **WHEN** роли требуется проверить готовность
- **THEN** используются signals и required/optional policy bounded context, которому принадлежит роль; общая топология не назначает за контекст его зависимости

#### Scenario: Неописанная dependency не становится optional
- **WHEN** dependency или capability profile отсутствует в конфигурации readiness
- **THEN** его состояние — `unknown`, и оно не учитывается как `up` или optional; required core profile становится not-ready

#### Scenario: Optional dependency отказала, независимый API продолжает работу
- **WHEN** optional dependency имеет `down` или `unknown`, а несвязанные core API paths работают
- **THEN** её capability остаётся видимой как degraded/unknown, но общий API `/readyz` остаётся ready

#### Scenario: Required capability неизвестна или отказала
- **WHEN** явно required core dependency имеет `unknown` или `down`
- **THEN** `/readyz` сообщает not-ready

#### Scenario: Degraded не является up для потребителя
- **WHEN** capability consumer получает состояние `degraded`
- **THEN** он не трактует его как `up`

#### Scenario: Статус адаптера без пробы
- **WHEN** provider adapter сконфигурирован в `integration-platform`, но реальная безопасная `Probe` не настроена
- **THEN** существующий `adapter-status` сообщает `unknown` с кодом `probe_not_configured`; он не сообщает `up`, а влияние на readiness определяется локальной policy контекста-потребителя

#### Scenario: Сохранение контекстного маршрута
- **WHEN** контекст уже предоставляет `/healthz`
- **THEN** маршрут сохраняется и не меняет смысл на проверку readiness внешних зависимостей

#### Scenario: Рантайм исполнения недоступен
- **WHEN** рантайм durable-исполнения недоступен, а процесс запущен
- **THEN** соответствующая роль сообщает not-ready через `/readyz`, тогда как `/livez` и `/healthz` продолжают отвечать как liveness

#### Scenario: Рантайм исполнения доступен
- **WHEN** рантайм durable-исполнения доступен и является required dependency роли
- **THEN** эта зависимость не блокирует `/readyz`; готовность также требует остальные required dependencies роли

#### Scenario: Роль процесса не равна готовности capability
- **WHEN** worker schedule/consumer процесс запущен, но capability profile не готов
- **THEN** process liveness остаётся успешной, а readiness отражает собственную role policy; один сигнал не подменяет другой

#### Scenario: HTTP endpoint только для HTTP роли
- **WHEN** роль доставки не хостит HTTP handler
- **THEN** для неё определяется role readiness без добавления нового HTTP endpoint

#### Scenario: Provider probe не подтверждена
- **WHEN** провайдер не документирует подтверждённую non-billable, bounded и non-mutating probe
- **THEN** adapter status остаётся `unknown`, включая `probe_not_configured`, и интеграционная задача остаётся открытой

#### Scenario: Зависший процесс детектируется пробой живости
- **WHEN** процесс перестаёт отвечать на `/livez` в пределах таймаута
- **THEN** Compose probe сообщает о нездоровье процесса

#### Scenario: Проба живости не зависит от готовности
- **WHEN** required dependency недоступна и `/readyz` сообщает not-ready
- **THEN** liveness probes `/livez` и `/healthz` продолжают сообщать только о доступности процесса/HTTP handler

#### Scenario: Пометка unhealthy не перезапускает контейнер
- **WHEN** Compose помечает контейнер `unhealthy`, но процесс не завершился
- **THEN** контейнер не перезапускается только из-за этой пометки; перезапуск определяется фактическим выходом процесса и настроенной restart policy
