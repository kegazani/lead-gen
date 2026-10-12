## Why

Пакетный путь утверждения `outreach-orchestration` (capability `risk-screening`) берёт
признак риска «адрес не подтверждён» у владельца `lead-management` (контракт
`address-verification`). В раздельной топологии контейнер `core` исполняет только
`outreach-orchestration`: co-located `lead-management` рядом нет, а сетевой транспорт
контракта `address-verification` не реализован (в change `risk-screening` он отмечен
как отложенный). Поэтому владельцы `delivery-target`/`address-verification` не
сконфигурированы для `core`, и сборщик фактов честно отказывает (503): реальные
риск-факты недостижимы на раздельном контуре. Выдумывать вердикт вместо отказа
запрещено (`AGENTS.md` §3). Каталог уже объявляет связь
`lead-verification-for-outreach` с `transport: sync` / `mechanism: http-json` — не
хватает её реализации.

## What Changes

- Реализуется сетевой транспорт контракта `address-verification` (владелец
  `lead-management`): HTTP-адаптер у `contracts/lead-management/addressverification/httpport`,
  реализующий `addressverification.Port` (`POST {base}/address-verification`), и
  владельческая HTTP-поверхность в `lead-management` (`POST /address-verification`).
- Владельческая сборка `lead-management` получает конфигурацию внешней проверки адреса
  (адрес и ключ сервиса проверки), чтобы сетевая поверхность отдавала реальный вердикт,
  а не честную недоступность.
- Сетевой адаптер переводит HTTP-коды владельца в исходы контракта: доменный отказ
  (400) — `ErrRejected`, прочие коды и сбой сети — `ErrUnavailable`; успешный, но
  неразборчивый либо с неизвестным статусом ответ — тоже `ErrUnavailable`. Пустой
  ответ не трактуется как «адрес подтверждён» и не трактуется как «адрес не
  подтверждён» — это честная недоступность.
- Форма потребителя не меняется: `outreach-orchestration` (`internal/adapters/riskfacts`)
  работает с тем же `addressverification.Port`; сетевой клиент и in-proc адаптер
  взаимозаменяемы, а выбор транспорта задаёт топология (`deployment-topology`).
- **Вне объёма:** проводка в `backend/cmd/lead-gen/**` (флаг адреса
  `address-verification` для `core`, передача verifier-конфигурации владельцу) и
  правки `compose.yaml` — общие файлы, оставлены оркестратору (см. `tasks.md`).

## Capabilities

### New Capabilities

Нет. Каталог уже объявляет контракт `address-verification` и связь
`lead-verification-for-outreach`; capability не вводится.

### Modified Capabilities

- `risk-screening`: добавляется требование о достижимости фактов верификации адреса
  при разделении сервисов по процессам — владелец публикует сетевую поверхность
  контракта `address-verification`, а потребитель получает тот же вердикт по сети, не
  меняя форму и честную семантику недоступности. Ранее сетевой транспорт был явно
  отложен, и признак был достижим только при co-location.

## Impact

- **Affected bounded contexts:** `outreach-orchestration` (потребитель фактов риска),
  `lead-management` (владелец верификации адреса). Состав контекстов, агрегатов и
  связей не меняется: связь `lead-verification-for-outreach` уже объявлена в
  `architecture/domain-catalog.yaml` с `mechanism: http-json`; каталог, `docs/**` и
  диаграммы не правятся.
- **Код:** новый пакет
  `contracts/lead-management/addressverification/httpport`; владельческая поверхность
  `backend/contexts/lead-management/internal/adapters/http/addressverification.go` и
  регистрация маршрута; фабрика `NewAddressVerification` подключается в сборке
  сервиса; конфигурация `lead-management` дополняется адресом и ключом сервиса
  проверки; тест достижимости фактов по сети у `outreach-orchestration`
  (`internal/adapters/riskfacts`).
- **Спеки:** дельта `specs/risk-screening/spec.md` (ADDED) в этом change.
- **Ограничения:** признак берётся только у владельца; недоступность владельца не
  трактуется как «рисков нет»; реализация не вводит заглушку и не подменяет вердикт
  владельца.
