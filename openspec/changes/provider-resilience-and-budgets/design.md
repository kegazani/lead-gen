## Контекст

`backend/contexts/integration-platform/internal/adapters/openai` использует прямой `http.Client`; `doJSON` и `doStream` сводят ошибки, chat completion не задаёт `max_tokens`, а Assess выполняет одну semantic JSON repair. Firecrawl также имеет прямой HTTP adapter. Общие primitives находятся в `backend/platform/reliability/resilience` и `backend/platform/invocation`, но не подключены к обоим прямым адаптерам. Существующая Firecrawl credit guard отдельна; этот change не подменяет её и не объявляет локальные token reserves глобальным billing cap.

## Цели и нецели

- До сети знать ограниченную верхнюю границу расхода для каждой платной операции и выделить отдельный резерв каждой отправке.
- Отличать HTTP 429/5xx, permanent HTTP и transport/unknown outcome; не повторять неизвестный исход.
- Подключить имеющиеся retry/breaker/bulkhead, не смешивая независимые provider/model scopes.
- Не менять доменную модель. Изменить опубликованный `contracts/integration-platform/llm`; прежняя нецель о неизменности межконтекстных контрактов отменяется.
- Не выполнять реальные provider calls при adapter tests и не поставлять fake production adapter.

Нецель — обещать account-wide, durable или точную денежную гарантию тарификации. Usage — только подтверждённая фактическая usage из ответа; reserve и локальная accounting не равны счёту провайдера.

## Архитектурное решение

### Владение capability и contract

`provider-token-budget` имеет ровно один primary context — `integration-platform`, владельца внешнего LLM adapter и опубликованного budget contract. `lead-management` потребляет этот контракт через границу адаптера и не импортирует чужие агрегаты или внутренние domain-типы. Capability техническая: для неё не вводятся бизнес-агрегат и domain event. Supporting contexts не добавляются в catalog schema.

### Идентичность операции и бюджет

Каждая опубликованная операция LLM (Plan, Suggest, Converse, ConverseStream, Interpret, Assess) принимает caller-provided стабильный технический `OperationScope`/`OperationID` и budget-policy context. Значение обязательно и валидируется до любой сетевой активности; адаптер не создаёт случайный или иной scope самостоятельно. Идентификатор — opaque technical value, не PII.

Повтор той же логической бизнес-операции, включая Temporal/activity replay, использует тот же scope. Отдельный физический send, retry или Assess repair получает уникальный attempt ID внутри этого scope. Резервирование связывается со scope и attempt. Идентичность не обещает дедупликацию на стороне провайдера.

Владельцы формируют scope из имеющихся стабильных идентификаторов (например, RunSearch JobID, requestID вместе с attempt/generation, conversation/session ID). Если подходящего ID нет, callsite обязан предоставить отдельный idempotency scope и не может подменять его PII.

### Входная оценка

Портовый/адаптерный механизм бюджета определяет operation budget перед первой отправкой. Источник — явная конфигурация на модель: выбранный tokenizer с закреплённой версией и верхней оценкой сериализованного prompt плюс protocol overhead, либо документированный конечный model-context bound. При отсутствии проверяемой верхней границы — fail-before-call. Сумма верхней оценки input и output cap не может превышать per-model context bound. Output cap передаётся в Chat Completion как `max_tokens`; официальная документация DeepSeek (`https://api-docs.deepseek.com/api/create-chat-completion`) подтверждает, что это максимальное число генерируемых токенов, а суммарный input+output ограничен model context. Она не подтверждает конкретные context limits и цену выбранной модели. Матрица поддерживаемых моделей должна хранить ссылки/дату проверки context и pricing.

Для каждого физического send, retry и Assess repair reserve приобретается до записи запроса и привязывается к attempt под общим OperationScope. Явный отказ, документированно безопасный для повтора, может освободить предыдущий reservation только если есть evidence отсутствия списания; иначе резерв остаётся pinned. Transport timeout после отправки считается unknown, без retry и без release. Не добавлять idempotency header без endpoint-specific документации. Usage хранить отдельно и reconciliate лишь подтверждённый ответ; не экстраполировать total billing. Registry резервов остаётся process-local; неизвестный reservation остаётся pinned. Это не глобальный durable total и не утверждение о сумме биллинга аккаунта.

### Ошибки и устойчивость

Ввести типизированную ошибку provider response с status, retry-after и безопасной категорией; транспортная ошибка несёт признак unknown outcome. `Retry-After` принимать как integer seconds либо HTTP-date, отрицательные/некорректные значения игнорировать согласно определённой policy; верхний предел и дедлайн контекста ограничивают ожидание. 429, 5xx и transport разделяются, но retry допускается только сочетанием безопасного outcome и явно retryable operation. Постоянный 4xx не retry. Retry policy конечная; каждый новый платный POST требует нового резерва.

Использовать существующие `resilience.RetryPolicy`, `Breaker` и `Bulkhead` либо расширять только минимально необходимыми интерфейсами. Обвязки принадлежат provider adapters; breaker registry создаётся в composition root с ключом `(provider, model/endpoint)`, bulkhead scope аналогичен. Не заводить один общий breaker для OpenAI chat и embeddings либо Firecrawl endpointов. 4xx permanent, cancellation и неизвестный timeout не учитывать как обычный downstream retry failure без обоснованной классификации.

Streaming: до первого event/content допустимы только явно безопасные ограниченные повторы с новым резервом. После первого отданного фрагмента ошибка возвращается без рестарта. Close/cancel потока освобождает bulkhead ровно один раз.

### Runner/wiring и владение файлами

Один владелец изменяет `backend/cmd/lead-gen/runners.go` в этом change: владелец `provider-resilience-and-budgets`. Изменения его composition root/wiring сериализуются; параллельные changes не редактируют файл, а интегрируются последовательно после согласования. Другие изменения контекста могут менять adapter-owned файлы, но не этот runner в течение данного change. Тесты composition root проверяют явное разделение scopes и реальное внедрение в оба пути. Не изменяются сервисные role boundaries или `deploy/services.yaml` без отдельного доказательства необходимости.

## Тестирование

Только `httptest`/RoundTripper и test doubles: JSON body содержит правильный `max_tokens`; prompt budget/context overflow отказывает до сети; reserve отдельно учитывает first call/retry/repair; unknown timeout оставляет reserve и не повторяется; 429 и 5xx классифицируются; `Retry-After` seconds/date с clamping; permanent 4xx и cancellation без retry; breaker isolation OpenAI model/embeddings и Firecrawl endpoint; bulkhead concurrency/cancel; streaming до первого события и после передачи контента; usage хранится отдельно; runner wiring инстанцирует adapter с реальными resilience/budget компонентами. Проверка provider doc фиксирует URL и дату, context/pricing подтверждается по каждой модели отдельно. Реальный provider acceptance не является требованием adapter unit tests; он обязателен перед deployment этой интеграции. Пока ключи не предоставлены, live acceptance остаётся открытым и никакой fake не подменяет её.

## Риски

- Tokenizer и protocol overhead могут не давать жёсткой гарантии: тогда безопасный исход — отказ до отправки, а не фиктивная оценка.
- Локальные reserves не являются транзакцией с биллингом провайдера и не переживают процессный сбой, если отдельно не будет спроектировано durable-хранилище.
- Повтор HTTP POST по 429/5xx не всегда безопасен: перед освобождением резерва и повтором требуются endpoint-specific доказательства отсутствия исполнения/списания.
- Provider docs и тарифы меняются; привязать проверку context/pricing к модели и версии документации.
