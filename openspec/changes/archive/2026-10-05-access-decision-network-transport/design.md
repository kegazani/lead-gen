## Контекст

Контракт `contracts/platform-governance/accessdecision` содержит интерфейс
`Decider` (синхронная проверка `CanAct(ctx, actor, tenant) (bool, error)`) и
снимок `RuleSnapshot` (policy). В `cmd/lead-gen` `assembleAccessDecision` строит
решение так:

- владелец `platform-governance` выбран → срез связывается с контрактом
  in-process адаптером (`accessdecisioninproc.Bind`);
- владелец не выбран → честный `accessdecision.Unavailable`.

Сетевого транспорта нет, поэтому при разделении сервисов потребители получают
`Unavailable`, хотя владелец жив. Потребители уже держат `accessdecision.Decider`
(тонкие адаптеры `internal/adapters/access` в `outreach-orchestration` и
`data-governance`), поэтому менять их код не нужно — достаточно подставить
другую реализацию контракта.

## Решение 1. Форма контракта принадлежит контракту

DTO запроса/ответа и путь эндпоинта (`CanActRequest`, `CanActResponse`,
`PathCanAct = "/access-decision"`) объявляются в пакете `accessdecision`.
Владелец (HTTP-адаптер `platform-governance`) и клиент (`httpport`) используют
их как единый источник истины, поэтому wire-формат не расходится между
владельцем и потребителем. Пакет контракта остаётся на стандартной библиотеке.

## Решение 2. Владелец: `POST /access-decision`

Маршрут добавляется в `routes()` HTTP-адаптера `platform-governance`. Обработчик
вызывает существующий use case `DecideAccess` (RBAC не воспроизводится) и
возвращает `{"allowed": bool}`. Непустой `DecideAccess` обязателен: без него
(как в тестах сборки сервера) обработчик отвечает `503 Service Unavailable` —
честная недоступность, не выдуманное разрешение. Пустой актор — ошибка
`DecideAccess`, а не положительное решение.

Существующий admin-эндпоинт `GET /api/tenants/{tenantID}/access` не трогаем: он
принадлежит внутреннему API управления и не является транспортом published
контракта.

## Решение 3. Клиент: `accessdecision/httpport`

Пакет-адаптер реализует `accessdecision.Decider` поверх `net/http` по образцу
`contracts/lead-management/leadsearch/httpport`:

- `New(baseURL, ...Option)` проверяет абсолютный http(s)-URL на этапе сборки;
- `CanAct` шлёт `POST {base}/access-decision` с телом `CanActRequest`;
- `2xx` → разбор `CanActResponse.Allowed`;
- любой иной код, сбой сети и неразборчивый ответ → `accessdecision.ErrUnavailable`
  (никогда не `true`).

Бюджет вызова ограничивается `http.Client.Timeout` (по умолчанию 5 с, как
in-process `defaultTimeout`), а также уважается `context.Context` вызывающего:
дедлайн и отмена соблюдаются, недоступность — отказ.

## Решение 4. Выбор транспорта по топологии в `cmd/lead-gen`

В `buildOptions` добавляется `accessDecisionURL` (флаг `-access-decision-url`,
ENV `LEADGEN_ACCESS_DECISION_URL`). `assembleAccessDecision`:

1. адрес задан → `accessdecisionhttpport.New(url)` → `opts.accessDecision`;
2. иначе — прежнее поведение: in-proc связывание владельца, если он выбран,
   иначе `Unavailable`.

Так co-location остаётся без сети, а раздельный контур (`demand`/`core` указывают
адрес `platform-governance`) получает решение по сети. Недоступность владельца не
маскируется.

## Решение 5. Каталог: два вида связи на один контракт

`access-decision` имеет две грани: снимок правил (policy, async) и синхронную
проверку решения (query). Существующие связи `platform-access-for-outreach` и
`platform-access-for-data-governance` описывают policy-снимок и остаются
async/amqp. Синхронная проверка добавляется отдельными связями
`platform-access-check-for-outreach` и
`platform-access-check-for-data-governance` с `integration: query`,
`transport: sync`, `mechanism: http-json` — это то, что реально работает после
настоящего change. Уникальность связей в каталоге учитывает `integration`,
поэтому сосуществование policy и query для одного контракта корректно.
