## 1. Инфраструктура и конфигурация

- [x] 1.1 `compose.yaml`: сервис `embeddings` (self-hosted Ollama 0.35.1, OpenAI-совместимый `/v1/embeddings`), healthcheck, том `ollama-models`, авто-загрузка модели `bge-m3` при старте.
  - Критерий: `docker compose config` валиден; сервис объявлен в сети контура и доступен по имени `embeddings`.
- [x] 1.2 `compose.yaml`: дефолты `EMBEDDING_BASE_URL=http://embeddings:11434`, `EMBEDDING_MODEL=bge-m3`, `EMBEDDING_KEY=local` для групп, собирающих векторный порт (`demand`, `intake`).
  - Критерий: без переменных в `.env` флаги `-embedding-*` контейнера указывают на локальный сервис.
- [x] 1.3 `.env.example` и `deploy/README.md`: локальный провайдер по умолчанию, внешний OpenAI-совместимый — опция; оговорка, что `EMBEDDING_BASE_URL` — без суффикса `/v1`.
  - Критерий: документация воспроизводит рабочую конфигурацию контура.

## 2. Проверка

- [x] 2.1 Гейт: `make check` зелёный (62 passed, 0 failed), `go -C tools/archlint run .` — код 0 (12/23/40/60/51/8), `openspec validate local-embedding-provider --strict` — valid.
- [x] 2.2 Живой прогон: сервис `embeddings` здоров; `POST http://localhost:11435/v1/embeddings` вернул вектор (1024, ненулевой) для русского текста; `POST :8082/cards/norbit.ru/embed` и `/cards/rechka.ai/embed` → 200, `dimensions=1024`; в `"integration-platform".vector_document` две строки с непустым `embedding`; `POST :8082/semantic-recall` → 200 с кандидатами и оценками. Заглушки нет (`AGENTS.md` §3).
- [x] 2.3 Отмечена `embedding-config-separation` 2.2 (`- [x]` с доказательствами), обновлён `docs/progress-log.md`.
