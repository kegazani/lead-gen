## Why

Семантический recall/RAG (`vector-recall`, `semantic-lead-search`) до сих пор
не работал живьём: OpenAI-совместимый провайдер эмбеддингов не выбран, а чат
идёт в DeepSeek, у которого эмбеддингов нет (задача 2.2 `embedding-config-separation`
открыта именно по этой причине). Нужен **локальный self-hosted** провайдер
эмбеддингов без внешнего ключа, чтобы recall был проверяем и работоспособен в
контуре по умолчанию.

## What Changes

- В `compose.yaml` добавляется сервис локальных эмбеддингов `embeddings`
  (self-hosted **Ollama**, OpenAI-совместимый `POST /v1/embeddings`), модель
  **`bge-m3`** (многоязычная, поддержка русского). Сервис поднимается вместе с
  контуром и сам подтягивает модель при старте; внешний ключ не требуется.
- Проводка конфигурации: `EMBEDDING_BASE_URL` (адрес `embeddings` внутри сети
  compose), `EMBEDDING_MODEL` (`bge-m3`), `EMBEDDING_KEY` (фиктивное значение
  `local` — сервер без авторизации, а адаптер `NewEmbedder` требует непустой
  ключ). Локальный провайдер становится значением по умолчанию контура;
  внешний OpenAI-совместимый провайдер остаётся опцией через те же переменные.
- `.env.example` и `deploy/README.md` описывают локальный провайдер по
  умолчанию и напоминают, что `EMBEDDING_BASE_URL` — базовый адрес **без**
  суффикса `/v1` (клиент сам склеивает путь `/v1/embeddings`).
- Правок кода на Go **не требуется**: адаптер эмбеддера и `vectorRecallPort`
  уже готовы (`embedding-config-separation`); изменение — инфраструктура и
  конфигурация.

- **Не входит:** смена контракта `vector`, каталога, доменной модели; новые
  capability; детерминированная подмена эмбеддинга (запрещена, `AGENTS.md` §3).

## Capabilities

### New Capabilities

Нет.

### Modified Capabilities

Нет. Требования `vector-recall` и `semantic-lead-search` не меняются: меняется
лишь источник провайдера эмбеддингов (self-hosted вместо внешнего). Дельта
спеков не нужна — `skip_specs: true` (инфраструктура/проводка, как у
`embedding-config-separation`).

## Impact

**Домен:** не затронута. Границы `bounded context`, агрегаты, владение и связи
не меняются, поэтому `architecture/domain-catalog.yaml` и `docs/domain-model.md`
не правятся. Затронутые контексты — потребители и владелец векторного порта:
`integration-platform` (адаптер эмбеддера/векторный store), `lead-management`
(`/semantic-recall`, `/cards/{id}/embed`), `request-fulfillment` (эмбеддинг
оффера на шаге одобрения заявки).

**Инфраструктура:** `compose.yaml` (новый сервис `embeddings`), `.env.example`,
`deploy/README.md`.

**Внешние зависимости:** self-hosted Ollama + модель `bge-m3`; внешний ключ не
требуется. Образ и модель скачиваются один раз (Docker Hub / реестр Ollama).
