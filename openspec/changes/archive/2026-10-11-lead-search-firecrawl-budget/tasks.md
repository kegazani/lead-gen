## 1. Инвентаризация policy и wiring

- [x] 1.1 Сверить все Firecrawl Search/Crawl/Extract вызовы `SearchLeadsForRequest`, supported cost shapes, TaskBudget lifecycle и Guard lifetime с кодом.
- [x] 1.2 Передать настроенный `FirecrawlMaxCredits` из lead-gen composition в owner service; применять его к отдельному process-local бюджету `RunSearch` и синхронного `/lead-search`, повторно используя общий Guard/limiter/account policy без raw client.
- [x] 1.3 Зафиксировать семантику `0` как отсутствие только per-task cap; проверить, что fresh account balance и известный точный reserve обязательны независимо от cap, а неизвестный balance/cost запрещает вызов.

## 2. Подключение Guard

- [x] 2.1 Сделать существующий `-firecrawl-max-credits` эффективным для обоих путей и добавить budgeted wrappers для Search/Crawl/Extract в synchronous `/lead-search`; admission, оценка и reserve должны предшествовать сети.
- [x] 2.2 Реализовать settlement только подтверждённого использования; неопределённый outcome удерживает резерв и запрещает использовать его повторно.
- [x] 2.3 Связать отмену с остановкой новых затратных действий и обработкой/сохранением уже полученных partial pages.
- [x] 2.4 Сохранить существующие HTTP wire semantics и parity с in-process вызовом; исключить raw Firecrawl путь из сборки сценария.
- [x] 2.5 При неподтверждённой цене Extract не вызывать его; если `/lead-search` не может продолжить без Extract, возвращать честный частичный/недоступный результат без сообщения о полном успехе.

## 3. Ограниченный lifecycle TaskBudget registry

- [x] 3.1 Расширить публичный контракт `contracts/integration-platform/budget`: добавить идемпотентный `Finish(ctx, taskID)` или эквивалент к жизненному циклу `Factory.Begin`; сохранить владение контрактом и registry за `integration-platform`.
- [x] 3.2 Задать конечные явно именованные настройки registry capacity и TTL завершённых scope с bounded defaults, не трактуя их как стоимость вызова или внешний финансовый cap; один task ID — одна запись/бюджет.
- [x] 3.3 Реализовать settlement gate для `Finish`: terminal settled только после подтверждения/освобождения всех reservations, без outstanding reservation и unknown outcome; aggregate account-balance delta не является operation-level evidence и не снимает unknown pin.
- [x] 3.4 Вызвать `Finish` из `RunSearch` и `SearchLeadsForRequest` только для terminal settled исходов; ошибка/cancellation/partial result не должны автоматически освобождать неизвестный резерв.
- [x] 3.5 Удерживать settled scope для replay того же task ID до TTL; разрешать eviction только после TTL и без reservations. Active/unknown/outstanding scope не вытесняются ни по возрасту, ни ради capacity.
- [x] 3.6 При полной ёмкости после удаления безопасно истёкших settled scope возвращать стабильную capacity error и fail closed до Firecrawl HTTP; не сбрасывать active/unknown budget.
- [x] 3.7 Добавить contract/registry и caller tests: идемпотентность Finish, unknown pin без operation-level evidence, reservations, capacity без eviction, TTL/replay, pressure status, сериализованный account refresh и Finish на путях обоих поисков.
- [x] 3.8 Зафиксировать process-local unknown tracking, ограничение aggregate balance как не-operation-level evidence и обязательность свежего balance только для последующей account admission; не обещать durable cross-process billing или production total billing cap.

## 4. Сквозная регрессия

- [x] 4.1 Добавить регрессию через фактически собранный HTTP `POST /lead-search`: admission denial приводит к нулю запросов к Firecrawl.
- [x] 4.2 Проверить реальный Firecrawl adapter на контролируемом `httptest.Server` и memory repository для Search/Crawl/Extract, settlement и частичного результата.
- [x] 4.3 Проверить неопределённый сетевой исход, отсутствие повторной бесплатной траты, отмену и HTTP/in-process parity.
- [x] 4.4 Не выполнять live Firecrawl вызовов/тратить кредиты без изолированного аккаунта.

## 5. Верификация

- [x] 5.1 Запустить `make check`, `go -C tools/archlint run .` и `openspec validate lead-search-firecrawl-budget --strict`; устранить найденные расхождения.
- [x] 5.2 Подтвердить отсутствие изменений доменного каталога и сохранение владения бюджетным контрактом за `integration-platform`; явно описать process-local ограничения, выполнить итоговый review и не закрывать задачи до их критериев.
