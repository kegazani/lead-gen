## 1. Разведение конфигурации

- [x] 1.1 Флаги `-embedding-base-url`/`-embedding-model`/`-embedding-key` с ENV-фолбэком `EMBEDDING_*` в `backend/cmd/lead-gen`.
- [x] 1.2 `vectorRecallPort` собирается по `-embedding-key` (не `LLM_API_KEY`); базовый адрес/модель — из `EMBEDDING_*`.
- [x] 1.3 `compose.yaml` и `.env.example`: необязательный блок `EMBEDDING_*`.

## 2. Проверка

- [x] 2.1 `make check` — зелёный (57 passed).
- [x] 2.2 Живой прогон: одобрение заявки проходит без `EMBEDDING_KEY` (workflow стартует) — проверено живьём. Семантический recall подтверждён живьём с локальным self-hosted провайдером (change `local-embedding-provider`, 2026-10-06): провайдер — self-hosted Ollama `0.35.1`, OpenAI-совместимый `/v1/embeddings`, модель `bge-m3` (1024 измерения). `POST http://localhost:11435/v1/embeddings` для русского текста вернул ненулевой вектор; `POST :8082/cards/norbit.ru/embed` и `/cards/rechka.ai/embed` → 200 (`dimensions=1024`); в `"integration-platform".vector_document` две строки с непустым `embedding`; `POST :8082/semantic-recall` → 200 с кандидатами и косинусными оценками. Внешний ключ не потребовался; детерминированная подмена не ставилась (`AGENTS.md` §3).

## 3. Побочная находка (не в объёме change, но блокировала образ)

- [x] 3.1 Dockerfile: встроенный `go.work` не содержал `./contracts/platform-governance` (модуль добавлен M4) — образ не собирался с M4, контур работал на устаревшем образе. Модуль добавлен; образ собирается.
