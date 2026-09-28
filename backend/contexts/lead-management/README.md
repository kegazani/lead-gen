# Сервис lead-management — MVP-срез поиска лидов

Вертикальный срез capability `osint-enrichment`: задание поиска → поиск → обход →
экстракция → карточка компании в базе → витрина в консоли.

## Движки

- **Обход и поиск** — Firecrawl (hosted API): `/search`, `/map`, `/scrape`, `/crawl`,
  `extract`. Ротация IP и JS-рендер на стороне провайдера; robots.txt соблюдается.
- **Планирование поиска** — DeepSeek (OpenAI-совместимый `base_url` + `model`),
  только операция `Plan`. Без ключа планирование отключено, срез работает
  (слои экстракции 1–2).

## Локальный запуск

### Docker Compose (быстрый старт)

Поднимает весь стек: PostgreSQL зелёной и красной зон + сервис `lead-management`
(роли `api` и `worker`). Образ собирается из корневого `Dockerfile`.

```sh
cp .env.example .env     # заполнить FIRECRAWL_API_KEY
vim .env                 # по желанию: LLM_BASE_URL/LLM_MODEL/LLM_API_KEY
docker compose up --build
```

Консоль: <http://localhost:8080/> (порт настраивается `LEAD_HTTP_PORT` в `.env`).
Остановить: `docker compose down` (данные сохраняются в томах; сбросить —
`docker compose down -v`).

### Вручную (без Docker Compose)

1. **PostgreSQL.** Поднимите контур `deploy/tofu` env `local` (зелёная зона —
   схема `lead-management`, красная — отдельная БД) либо любой инстанс
   PostgreSQL. Миграции применяются автоматически при сборке сервиса из
   `migrations/green/` и `migrations/red/`.
2. **Ключ Firecrawl.** Обязателен — через флаг `-firecrawl-key` (или env-переменную).
3. **Ключ LLM (необязательно).** Флаги `-llm-base-url`, `-llm-model`, `-llm-key`;
   без `-llm-key` планирование отключено.

Запуск через единую точку входа образа:

```sh
go run ./backend/cmd/lead-gen \
  -service lead-management \
  -lead-green-dsn 'postgres://.../green?sslmode=disable' \
  -lead-red-dsn  'postgres://.../red?sslmode=disable' \
  -firecrawl-key  'fc-...' \
  -llm-key        'sk-...'   # необязательно
```

Без `-lead-green-dsn` сервис поднимается на in-memory-хранилище (для быстрого
dev-прогона консоли без базы). Роли: `api` (HTTP-консоль на `-addr`, по умолчанию
`:8080`) и `worker` (раннер заданий поиска).

Консоль: `GET /` (запуск поиска + журнал заданий), `GET /cards` (таблица),
`GET /cards/{id}` (карточка с контактами), `POST /cards/{id}/hide|delete`.
