## 1. Ошибки и классификация provider

- [ ] 1.1 В `integration-platform` реализовать типизированные HTTP/transport ошибки для OpenAI и Firecrawl; сохранить status, retry-after и неопределённость исхода; покрыть 429, 5xx, permanent 4xx и ошибки транспорта.
- [ ] 1.2 Реализовать парсер `Retry-After` для секунд и HTTP-date, проверку некорректных значений, clamping и отмену ожидания контекстом; добавить unit tests.

## 2. Budget и output cap

- [ ] 2.1 Определить конфигурационный источник bounded input token estimate и per-model context bound; задокументировать tokenizer/version либо fail-before-call при отсутствии подтверждённой границы.
- [ ] 2.2 В OpenAI chat completion передавать `max_tokens`; проверить сериализованное тело и отказ до сети при неизвестной оценке/превышении model context.
- [ ] 2.3 Ввести per-operation reserve/acquire/reconcile API; резервировать отдельно исходный вызов, каждую допустимую retry и Assess JSON repair; удерживать reserve при unknown outcome.
- [ ] 2.4 Сохранять подтверждённую provider usage отдельно от reserve и budget; не выдавать локальный бюджет за общий account billing cap.
- [ ] 2.5 Расширить опубликованный `contracts/integration-platform/llm` contract обязательными caller-provided `OperationScope`/`OperationID` и budget-policy context для Plan/Suggest/Converse/ConverseStream/Interpret/Assess; валидировать до сети и не генерировать scope в адаптере.
- [ ] 2.6 Привязать reservations к scope и уникальному attempt ID каждого физического send/retry/repair; сохранять scope при replay той же бизнес-операции/Temporal activity. Проверить отсутствие scope до сети, replay, разные attempts и отсутствие коллизий.
- [ ] 2.7 В потребляющих контекстах отображать реальные стабильные технические идентификаторы на scope (включая RunSearch JobID и requestID+attempt/generation, где применимо); для остальных callsites определить conversation/session или отдельный idempotency scope. Не использовать PII; callsite без scope остаётся заблокированным.

## 3. Retry и resilience wiring адаптеров

- [ ] 3.1 Подключить общие RetryPolicy/Breaker/Bulkhead к OpenAI JSON-вызовам с явным allowlist безопасных outcomes/operations и дополнительным reserve до каждой платной отправки.
- [ ] 3.2 Подключить общие retry/breaker/bulkhead к прямому Firecrawl adapter, сохраняя существующий credit guard и не освобождая неизвестный расход.
- [ ] 3.3 Разделить breaker/bulkhead registry scopes по provider и model/endpoint; исключить permanent 4xx и отмену из retry и корректно классифицировать для breaker.
- [ ] 3.4 Для streaming разрешить повтор только до первого event/content; после доставки контента возвращать ошибку без перезапуска; проверить однократный release bulkhead.

## 4. Composition root и проверка

- [ ] 4.0 Зафиксировать `provider-token-budget` только за `integration-platform` как primary context; описать `lead-management` и прочих потребителей опубликованного LLM contract без импорта foreign domain model. Обновить cross-context impact/contract и затронутые callsites; не добавлять бизнес-агрегат/event или supporting contexts в catalog schema. Синхронизировать каталог, capability matrix и bounded-context diagram только если меняются владение/capability.
- [ ] 4.1 Единоличный владелец `backend/cmd/lead-gen/runners.go` в рамках change — `provider-resilience-and-budgets`; последовательно подключить бюджет и resilience providers к прямым OpenAI и Firecrawl adapters. Не принимать параллельные edits этого файла от `service-per-context`/иных changes; согласованные изменения переносить после сериализации.
- [ ] 4.2 Добавить тесты composition root, подтверждающие фактическое внедрение budget, retry, breaker и bulkhead в оба adapter пути и отдельные breaker scopes.
- [ ] 4.3 Добавить adapter regression matrix: retry/reserve/unknown timeout, `Retry-After` обеих форм, permanent error/cancel, streaming до/после первого события, usage и repair budget; все тесты без реального provider call.
- [ ] 4.4 Перед deployment подтвердить документацией контекст и pricing для каждой включённой модели; провести реальную acceptance с реальными ключами и зафиксировать её отдельно. Пока ключи отсутствуют, acceptance и задача остаются открытыми, production fake не вводить.
- [ ] 4.5 Выполнить `make check`, `go -C tools/archlint run .` и `openspec validate provider-resilience-and-budgets --strict`; не отмечать задачу/change завершёнными при невыполненной реальной deployment acceptance или наличии заглушки на пути.
- [ ] 4.6 Проверить scope collision prevention: разные логические операции не разделяют reservations/attempts; process-local registry и pinned unknown reservation не описываются как durable/global billing total.
