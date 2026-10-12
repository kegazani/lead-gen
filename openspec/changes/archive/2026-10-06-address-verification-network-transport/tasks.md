## 1. Сетевой адаптер контракта address-verification

- [x] 1.1 Реализовать `contracts/lead-management/addressverification/httpport` — HTTP-адаптер `addressverification.Port` (`POST {base}/address-verification`, wire-формат `tenant_id`/`recipient_ref` → снимок `status`/`verified`/`reason`/`checked_at`), зависящий только от стандартной библиотеки и контракта. Проверка: `go build ./...` в модуле `contracts/lead-management`; `var _ addressverification.Port = (*Client)(nil)`.
- [x] 1.2 Реализовать перевод исходов: 400 → `ErrRejected`, прочие коды и сбой сети → `ErrUnavailable`; успешный ответ с отсутствующим/неизвестным (`Status.Known() == false`) статусом или неразборчивым телом — `ErrUnavailable`. Проверка: тест `client_test.go` покрывает успех, `ErrRejected`, `ErrUnavailable`, пустой/нераспознанный статус, неразборчивое тело, недоступный сервис, некорректный базовый адрес.

## 2. Владельческая HTTP-поверхность lead-management

- [x] 2.1 Добавить `Dependencies.AddressVerification addressverification.Port` и поле сервера; реализовать обработчик `POST /address-verification` (`internal/adapters/http/addressverification.go`), делегирующий вызов порту владельца; исходы контракта → коды: `ErrRejected` → 400, `ErrUnavailable` → 503, прочее → 500; незамонтированный порт → 503. Проверка: тест обработчика и round-trip сетевого клиента к серверу.
- [x] 2.2 Зарегистрировать маршрут `POST /address-verification` и внести его в публичные пути `authMiddleware` (эндпоинт контракта, без сессии/CSRF). Проверка: `TestAddressVerificationHTTPRoundTrip` проходит без сессии.
- [x] 2.3 Дополнить `Config` сервиса `lead-management` полями `AddressVerifierEndpoint`/`AddressVerifierAPIKey` и передать их в `NewAddressVerification` при сборке `Dependencies.AddressVerification`; поверхность отдаёт реальный вердикт владельца, а не выдуманный. Проверка: тест `addressverification_http_test.go` в пакете `leadmanagement` сверяет вердикт сетевой поверхности с in-proc `NewAddressVerification` (подтверждённый адрес → `verified=true`).

## 3. Достижимость фактов у потребителя (outreach-orchestration)

- [x] 3.1 Зафиксировать, что сборщик фактов работает с сетевым транспортом без изменения формы: тест `internal/adapters/riskfacts` собирает `Collector` поверх `addressverification/httpport` (тестовый сервер контракта) и подтверждает признаки по сети. Проверка: чисто → пусто; неподтверждённый адрес → `address-unverified`; недоступный владелец → `domain.ErrRiskFactsUnavailable` (не «рисков нет»).

## 4. Проводка и живая проверка (оставлено оркестратору)

- [x] 4.1 **Проводка в `cmd`/`compose` + живая проверка выполнены оркестратором.** Флаг/ENV `LEADGEN_ADDRESS_VERIFICATION_URL` у потребителя (`core`), сборка сетевого клиента `addressverification/httpport` в `assembleRiskFacts` при разделении; владельцу (`demand`) передаётся verifier-конфиг (`AddressVerifierEndpoint`/`APIKey`). **Живьём (раздельная топология):** `core` логирует `риск-скрининг подключён (risk-screening)` (владельцы `delivery-target`/`address-verification` достижимы по сети); `POST demand:8082/address-verification` отвечает от самого владельца. Владелец достижим; остаточный `503` в ответе — не «нет владельца», а **честная недоступность внешнего провайдера проверки адресов** (`провайдер проверки не сконфигурирован`; BRD §8 — провайдер не выбран), т.е. допустимое явное состояние по `AGENTS.md` §3. Гейт: `make check` exit 0 (63 passed); archlint 51 связь.

## 5. Гейт

- [x] 5.1 Проверить `go build ./...` и `go test ./...` затронутых модулей (`contracts/lead-management`, `backend/contexts/lead-management`, `backend/contexts/outreach-orchestration`). Проверка: обе команды зелёные.
- [x] 5.2 Проверить `openspec validate address-verification-network-transport --strict`. Проверка: valid. (Опционально: `make check` / `go -C tools/archlint run .`; docker-контур не пересобирать.)
