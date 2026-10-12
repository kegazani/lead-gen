## 1. Заявка, отмена и черновики

- [ ] 1.1 Сверить текущие request/workflow edits и описать все mutation entrypoints отмены; отмена должна сохраняться до ответа, передаваться в workflow idempotently и не маркировать исполнение stopped до подтверждения. Верификация: HTTP/workflow tests покрывают repeat, active workflow и lost signal/ack без второго события.
- [ ] 1.2 Остановить дальнейшие activities после принятой отмены; не запускать новый search, draft или handoff. Верификация: Temporal workflow tests отменяют на фазах searching, drafting, handoff и approval и подтверждают отсутствие последующих activities.
- [x] 1.3 Сохранять каждый draft сразу после успешной генерации и прекращать цикл при отмене/ошибке записи. Верификация: regression подтверждает, что draft 1 доступен после отказа генерации draft 2, повтор не удаляет draft 1, cancellation не считается Failed. Проверено `go -C backend/contexts/request-fulfillment test ./internal/adapters/workflow -run 'Draft'`.
- [ ] 1.4 Проверить PostgreSQL upgrade/restart для заявки и черновиков на отдельной disposable PostgreSQL 17 без очистки пользовательских схем. Верификация: после перезапуска изолированного процесса сохранены request state, draft content, tenant и recipient reference.

## 2. Результаты лид-поиска

- [x] 2.1 Сделать ошибку записи профиля, контакта или job result явной; уже сохранённые лиды не теряются. Верификация: инъецированная ошибка записи профиля и job result проверена регрессиями `TestSearchLeadsForRequestReturnsProfilePersistenceFailure` и `TestRunSearchFailsWhenDurableResultCannotBeSaved`; профильные `go test ./internal/app` зелёные.
- [ ] 2.2 Исправить восстановление истёкшего running lease после быстрого рестарта и объединение результата с ранее сохранёнными ссылками без дублей. Верификация: PG-backed job прерывается, lease истекает, worker повторно подхватывает его, прежние result IDs сохраняются, повторные ID не добавляются.
- [ ] 2.3 Сохранить безопасную причину partial/cancel до workflow consumer и запретить новые платные действия после отмены. Верификация: HTTP/contract/workflow сценарии подтверждают передачу причины и отсутствие следующих search/crawl/extract вызовов.
- [ ] 2.4 Проверить связь поиска с заявкой на реальном PostgreSQL store; сохранённые лиды доступны после restart. Верификация: изолированный integration test перечитывает request-scoped result и пул после закрытия и повторного открытия adapters.

## 3. Отмена в outreach и durable delivery

- [x] 3.1 Добавить владельческую durable отмену заявки в outreach и fence handoff/approval, включая отмену раньше создания campaign. Верификация: owner HTTP contract test и PostgreSQL integration test покрывают cancel-before-handoff, cancel-after-handoff-before-approval, replay и запоздалый повтор handoff; профильные outreach suites зелёные. Межконтекстная передача из request-fulfillment реализована через опубликованный `outreach-handoff` cancellation operation.
- [x] 3.2 Заменить production approved-action memory queue/seen на PostgreSQL inbox с уникальным idempotency key, canonical payload conflict check и atomic sender claim. Верификация: PostgreSQL tests проверяют один concurrent claim, commit-before-ACK, replay/conflict и отсутствие второй отправки для ранее зарезервированного sending/unknown сообщения; composition выбирает durable queue при настроенном `supportGreenDSN`.
- [ ] 3.3 Применять delivery cancellation fence при enqueue, claim и retry; не возвращать `sending/unknown` в автоматический retry. Верификация: PG tests покрывают cancel-before-action, cancel-before-claim, retry race и unknown outcome без второго вызова mailer.
- [ ] 3.4 Проверить восстановление утверждённой ожидающей команды и message/outbox после перезапуска delivery worker. Верификация: изолированный PG test повторно собирает service adapters и получает ту же команду/состояние без дублей.

## 4. Gmail SMTP и честность исхода

- [x] 4.1 Включить обязательный STARTTLS и проверку сертификата до SMTP AUTH; применить общий deadline к командам сессии. Верификация: `go test ./internal/adapters/smtp` проходит с проверками порядка EHLO/STARTTLS/AUTH/DATA, отказа недоверенному TLS-сертификату и таймаута.
- [x] 4.2 Формировать стабильный RFC Message-ID и корректные MIME/RFC 2047 headers для кириллицы без попадания секретов в логи. Верификация: `mail.Message` несёт ID из владельческого delivery ID; smtp tests подтверждают стабильный sanitized Message-ID и корректное декодирование темы/тела.
- [x] 4.3 Разделить подтверждённый SMTP acceptance, явный provider rejection и transport-unknown после DATA; автоматический scheduler повторяет только доказуемо безопасные temporary outcomes. Верификация: `TestSendReturnsProviderRejectionBeforeDATAWithoutUnknownOutcome`/`TestSendReturnsUnknownWhenTLSClosesAfterDATAWithoutAcceptance` проходят; PostgreSQL `TestPostgresReservationReplayDoesNotSendUnknownTwice` проверяет durable `unknown`, отсутствие retry и повторного вызова mailer.
- [ ] 4.4 Проверить реальный Gmail acceptance и получение контрольного письма без раскрытия app password. Верификация: приложен run report с SMTP acceptance, message ID и фактическим получением в контрольном inbox; без доступного Gmail credentials задача остаётся открытой.

## 5. Интеграция и приёмка

- [ ] 5.1 Подключить durable stores/fences и mail transport в composition/deployment без memory fallback на production пути. Верификация: composition test подтверждает реальные PostgreSQL adapters, Gmail transport и fail-closed startup при недостающей обязательной конфигурации.
- [ ] 5.2 Выполнить сквозной сценарий: заявка → поиск/краулинг → сохранённые лиды → черновики → approval → durable delivery → Gmail. Верификация: на изолированном окружении лиды и письма видны после рестарта, письмо получено, повторные команды не создают вторую отправку.
- [ ] 5.3 Выполнить сценарии отмены до поиска, во время поиска, во время генерации, после handoff и до отправки; зафиксировать предел уже начавшейся SMTP операции. Верификация: сохранённые данные остаются, новые действия прекращаются, отправленное/unknown не объявляется отменённым.
- [ ] 5.4 Запустить профильные unit/integration/race suites, `make check`, `go -C tools/archlint run .`, `openspec validate request-workflow-data-durability --strict` и проверить отсутствие заглушек на пути. Верификация: все команды успешны; реальные PG/Gmail criteria не подменены fake acceptance.
- [ ] 5.5 Выполнить OpenSpec verify и archive только после прохождения реальных критериев и общих гейтов. Верификация: change archived; невыполненные внешние acceptance остаются открытыми.
