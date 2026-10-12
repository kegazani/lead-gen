## Why

LLM-вызовы OpenAI-совместимого адаптера не имеют явного предела output tokens и теряют различие между HTTP-статусами и транспортными ошибками; имеющиеся общие retry/breaker/bulkhead не подключены к прямым OpenAI и Firecrawl адаптерам. Это оставляет расходы и поведение при сбоях неограниченными или неясными, включая повтор семантического исправления JSON.

## What Changes

- Подключить ограниченные бюджеты на операцию и `max_tokens` к OpenAI chat completion; источник оценки входных токенов и отказ при неизвестной верхней границе определить явно.
- Добавить caller-provided стабильный технический `OperationScope`/`OperationID` и budget-policy context ко всем опубликованным LLM операциям: Plan, Suggest, Converse, ConverseStream, Interpret и Assess. Отсутствующая/невалидная область — отказ до сети; адаптер не генерирует её сам.
- Повтор той же логической бизнес-операции и Temporal/activity replay сохраняют scope; каждая физическая отправка, retry и repair получает уникальный attempt ID внутри scope.
- Классифицировать ответы HTTP и транспортные ошибки; учитывать `Retry-After`, безопасно допускать только явно разрешённые повторы и удерживать резерв при неопределённом исходе.
- Ограничить повтор семантического JSON repair отдельным резервом; сохранять usage провайдера как фактическую телеметрию, не выдавая её за гарантию полного billing.
- Подключить общие retry, breaker и bulkhead к прямым OpenAI и Firecrawl adapter-вызовам, с изоляцией по provider/model и правилами для streaming.
- Зафиксировать сериализацию изменений runner/wiring: единственный владелец `backend/cmd/lead-gen/runners.go` — этот change; параллельные changes не редактируют его до завершения интеграции.

## Capabilities

### New Capabilities
- `provider-token-budget`: верхняя граница токенов/попыток, резервирование, reconciliation и правила отказа для платных provider-вызовов.

### Modified Capabilities
- `integration-call-resilience`: классификация сбоев и безопасные повторы для OpenAI/Firecrawl, breaker/bulkhead, потоковые ограничения и стабильная идентичность LLM операции.

## Impact

- Единственный primary context capability `provider-token-budget` — `integration-platform`: он владеет внешним LLM adapter и опубликованным budget contract. `lead-management` потребляет этот контракт, не импортируя foreign domain model. Это техническая capability без нового бизнес-агрегата или domain event; supporting contexts в каталог/schema не добавляются.
- Affected bounded context: `integration-platform`; orchestration/wiring в `backend/cmd/lead-gen` затрагивает composition root, но не меняет доменное владение.
- Cross-context impact: изменить опубликованный `contracts/integration-platform/llm`; владельцы callsites в `lead-management` и других потребителях передают реальные стабильные идентификаторы (например, RunSearch JobID, requestID+attempt/generation, conversation/session ID). Если подходящего ID нет, callsite должен предоставить idempotency scope либо оставаться заблокированным; PII нельзя использовать как scope.
- Файлы: опубликованный `contracts/integration-platform/llm`, LLM callsites потребителей, `backend/contexts/integration-platform/internal/adapters/openai`, `.../firecrawl`, общие `backend/platform/reliability/resilience` и `backend/platform/invocation`, `backend/cmd/lead-gen/runners.go` и тесты.
- Внешние контракты не обещают total account billing cap. DeepSeek Chat Completion подтверждает поддержку синтаксиса `max_tokens` как верхней границы генерируемого completion; контекст и pricing сверяются отдельно для каждой модели. Тесты адаптеров используют HTTP test doubles; реальные provider calls обязательны только для acceptance перед фактическим deploy. Реальные ключи не предоставлены, production fake не допускается.
