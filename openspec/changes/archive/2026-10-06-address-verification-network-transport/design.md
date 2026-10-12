## Context

См. `proposal.md` — Why. Текущее состояние: контракт `address-verification`
(`contracts/lead-management/addressverification`) объявлен как query-снимок и
реализован только in-process (`addressverification.InProc`) фабрикой
`NewAddressVerification` в `backend/contexts/lead-management/contract.go`. Сетевой
транспорт отсутствует (в `risk-screening` — «Отложено»). Каталог уже объявляет связь
`lead-verification-for-outreach` (`lead-management` → `outreach-orchestration`,
`transport: sync`, `mechanism: http-json`), поэтому модель и каталог менять не нужно.

Потребитель — сборщик фактов `outreach-orchestration`
(`internal/adapters/riskfacts`): он держит `addressverification.Port` и вызывает
`Verification(ctx, Query{TenantID, RecipientRef})`. Форма транспорта потребителя не
касается: in-proc и сетевой адаптеры реализуют один интерфейс.

Ориентир — уже существующий сетевой транспорт контракта `delivery-target`
(`contracts/lead-management/deliverytarget/httpport` + владельческий
`POST /delivery-target` в `lead-management`, change `live-e2e-contour`, Фаза 2).

## Goals / Non-Goals

**Goals:**

- Дать контракту `address-verification` сетевой транспорт той же формы, что и
  `delivery-target`: HTTP-адаптер у контракта + владельческая HTTP-поверхность.
- Сохранить наблюдаемое поведение: вердикт владельца и честная семантика
  недоступности одинаковы при co-location и при разделении сервисов.
- Сделать факты верификации достижимыми для `core` (только `outreach-orchestration`)
  без co-located `lead-management`.

**Non-Goals:**

- Проводка в `backend/cmd/lead-gen/**` и `compose.yaml` (выбор транспорта по
  топологии, флаг адреса `address-verification` для `core`, передача verifier-конфига
  владельцу) — общие файлы, оставлены оркестратору (см. `tasks.md`, открытая задача).
- Изменения каталога, `docs/**` и диаграмм: связь уже объявлена.
- Изменение формы потребителя (`riskfacts` уже принимает `addressverification.Port`).
- Новая capability или агрегат.

## Decisions

### Решение 1. Сетевой адаптер лежит рядом с контрактом

Новый пакет `contracts/lead-management/addressverification/httpport` реализует
`addressverification.Port` поверх `net/http` (`POST {base}/address-verification`).
Адаптер зависит только от стандартной библиотеки и самого контракта; доменных типов
владельца не несёт.

*Почему:* адаптер знает published language — его место рядом с контрактом, а не в
`platform/transport`. `platform` — общий примитив, не знающий контрактов; положить
туда адаптер значило бы заставить `platform` зависеть от контракта конкретного
владельца. Это тот же довод, что у `deliverytarget/httpport` и `inproc`.

*Альтернатива:* один общий HTTP-адаптер на все query-контракты — отклонена: wire-формат
и семантика исходов у каждого владельца свои.

### Решение 2. Владельческая поверхность отдаёт вердикт самого владельца

HTTP-поверхность `lead-management` (`internal/adapters/http/addressverification.go`,
маршрут `POST /address-verification`) держит `addressverification.Port`, собранный
фабрикой владельца `NewAddressVerification`, и делегирует ему вызов. Адаптер только
разбирает запрос и переводит исходы контракта в HTTP-коды; правила верификации не
повторяются.

*Почему:* единственный источник вердикта — политика владельца
(`domain.DefaultVerificationPolicy` + `app.ResolveDeliveryTarget` + `app.VerifyAddress`).
Повторять эту сборку в HTTP-адаптере (как сделано для `delivery-target`, где сценарий
односоставный) значило бы продублировать решение о вердикте и разойтись с in-proc
адаптером. Инъекция `addressverification.Port` в `Dependencies` переиспользует ровно ту
же реализацию.

*Альтернатива:* новый app-сценарий `VerifyRecipientAddress` — не нужен: сборка уже есть
в `contract.go`, а порт даёт HTTP-адаптеру только контрактную форму.

### Решение 3. Владелец получает конфигурацию внешней проверки

`Config` сервиса `lead-management` дополняется `AddressVerifierEndpoint` и
`AddressVerifierAPIKey`; `New` передаёт их в `NewAddressVerification`. Без них
`app.VerifyAddress` честно отвечает `ErrVerificationUnavailable`, и сетевая
поверхность вернула бы 503 вместо реального вердикта.

*Почему:* реальная верификация адреса требует внешнего сервиса (или DNS + verifier) —
это решение владельца, а не потребителя. Задаёт конфигурацию владельца оркестратор
(вне объёма).

*Альтернатива:* проверять только DNS на стороне поверхности — отклонена: это урезанный
вердикт, не совпадающий с политикой владельца (роль/catch-all/abuse/disposable
различает внешний сервис).

### Решение 4. Wire-формат и перевод исходов

Запрос: `{"tenant_id","recipient_ref"}`. Ответ (200): `{"recipient_ref","status","verified","reason","checked_at"}`.
Имена и стиль совпадают с `delivery-target`/`address-verification` envelope. Клиент:

- 400 → `addressverification.ErrRejected` (доменный отказ), прочие коды и сбой сети →
  `ErrUnavailable`;
- успешный ответ с неизвестным (`Status.Known() == false`) или отсутствующим статусом —
  `ErrUnavailable`: состояние владельца не выдумывается;
- `checked_at` необязателен; пустой — нулевое время, неразборчивый — `ErrUnavailable`.

*Почему:* это сохраняет семантику контракта (Решение 3 `risk-screening`) — недоступность
и нераспознанный ответ не превращаются ни в «подтверждён» (пропуск риска), ни в
«не подтверждён» (ложный риск).

### Решение 5. Смена транспорта не меняет потребителя

`riskfacts.Collector` не меняется: сетевой клиент — тот же `addressverification.Port`.
Выбор транспорта делает точка сборки по топологии (`deployment-topology`: «выбор
SHALL задаваться топологией, а не кодом потребителя»). Проводка выбора — оркестратор.

## Risks / Trade-offs

- **[Поверхность владельца без verifier-конфига честно отвечает 503]** → Это не
  заглушка, а явная нереализованность контура: оркестратор обязан передать конфиг
  владельцу (открытая задача в `tasks.md`); сборка логирует отсутствие владельцев.
- **[Сетевой вызов без общего дедлайна]** → Адаптер соблюдает контекст вызова (как
  `deliverytarget/httpport`); границы бюджета — за вызывающим. При необходимости
  задаётся `WithHTTPClient` при сборке.
- **[Приватность]** → Контракт остаётся ссылочным: адрес и PII в запросе/ответе не
  передаются, вердикт выражается ссылкой и статусом.
- **[Расхождение in-proc и сетевого вердикта]** → Обе поверхности используют одну
  фабрику `NewAddressVerification`; тесты сравнивают round-trip с in-proc.

## Migration Plan

1. Публикуется сетевой адаптер контракта и владельческая HTTP-поверхность; вердикт
   совпадает с in-proc.
2. Оркестратор задаёт владельцу verifier-конфиг и адрес контракта у `core`
   (`address-verification`), переключая топологию на сетевой транспорт.
3. Откат: убрать адрес контракта у потребителя — сборщик снова честно недоступен
   (прежнее поведение), заглушка не подставляется.
