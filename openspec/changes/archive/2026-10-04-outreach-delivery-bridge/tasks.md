## 1. Документы и каталог

- [x] 1.1 Добавить capability `delivery-bridge` контексту `message-delivery` в `architecture/domain-catalog.yaml` (верификация: `go -C tools/archlint run .` код 0)
- [x] 1.2 Добавить контракт `delivery-target` в список contracts контекста `lead-management` и связь `leads-for-delivery` (`lead-management → message-delivery`) (верификация: `go -C tools/archlint run .` код 0)
- [x] 1.3 Описать `delivery-bridge`, контракт и связь в `docs/domain-model.md` (верификация: описание согласовано с каталогом)
- [x] 1.4 Зафиксировать контракт материала письма: расширить `content-version` необязательным полем `Subject` (MINOR `1.0.0 → 1.1.0`) и добавить связь `content-to-delivery` (`content-production → message-delivery`) (верификация: `go -C tools/archlint run .` код 0; `openspec validate --strict` valid)

## 2. Контракт delivery-target (владелец lead-management)

- [x] 2.1 Создать модуль контракта `contracts/lead-management/deliverytarget` (`Resolver`, `Target{Address, Subject, Body}`, ошибки `ErrRejected`/`ErrUnavailable`, in-process адаптер) (верификация: контракт компилируется, есть тесты формы)
- [x] 2.2 Реализовать резолвер в `lead-management` по контактам и черновикам писем с изоляцией тенанта (верификация: разрешение `RecipientRef`/`MaterialVersionRef` реально возвращает адрес и материал; чужая ссылка не разрешается)
- [x] 2.3 Честная недоступность резолвера при неузвестной/неразрешимой ссылке (верификация: тест различает доменный отказ и сбой)
- [x] 2.4 Расширить контракт `contracts/content-production/contentversion` необязательным полем `Subject`; заполнять его в `NewContentVersion` из заголовка материала (верификация: контракт и `NewContentVersion` несут тему; пусто — честно, без выдумывания)
- [x] 2.5 Реализовать резолвер материала `contentversion.Source` → `ports.MaterialResolver` и публичную фабрику `NewContentMaterialResolver` в `message-delivery` (верификация: ссылка `asset@version` резолвится в тему/тело; `ErrNotFound`/`ErrUnavailable` маппятся)

## 3. Мост в message-delivery

- [x] 3.1 Реализовать адаптер-источник команд из `approved-action` (`internal/adapters/commandsource/approvedaction`), реализующий `ports.CommandSource` (верификация: утверждённое действие превращается в `domain.DeliveryCommand` и выдаётся роли `sender`)
- [x] 3.2 Разрешать `RecipientRef`/`MaterialVersionRef` через порт-резолвер; обрабатывать недоступность и неразрешимость как явный отказ (верификация: тест на неразрешимую ссылку)
- [x] 3.3 Отклонять не-email канал и невалидное действие без внешнего вызова (верификация: тесты на канал и валидацию)
- [x] 3.4 Идемпотентность по (тенант, утверждённое действие) (верификация: повтор не создаёт второго сообщения)

## 4. Связывание и проверки

- [x] 4.1 Связать `approvedaction.Publisher` владельца с мостом `message-delivery` в `backend/cmd/lead-gen` (верификация: сборка в in-process топологии; отправка достижима)
- [x] 4.2 Сквозная проверка «утверждено → отправлено» (верификация: утверждённое действие проходит путь до доставки; не-email/невалидное — отклоняются. Покрыто интеграционными тестами трёх контекстов: outreach approve→outbox→relay→приёмник; message-delivery приёмник+sink → команда → отправка; live-E2E — волна 3)
- [x] 4.3 Прогнать `go -C tools/archlint run .` (верификация: код 0)
- [x] 4.4 Прогнать `make check` (верификация: build/test/vet/importlint/archlint/openspec зелёные)
- [x] 4.5 Прогнать `make test-postgres` (верификация: PostgreSQL-suite message-delivery зелёный)
- [x] 4.6 Убедиться в отсутствии заглушки на пути «утверждено → отправлено» (`AGENTS.md` §3) (верификация: мост резолвит реальные ссылки; нет `deterministic`/`unavailable`-подмены на продуктовом пути)
