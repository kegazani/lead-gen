# Локальное демо

Из корня репозитория с существующим `.env`:

```bash
docker compose -p lead-gen-demo -f compose.demo.yaml up -d --build --wait --wait-timeout 240
```

Нужны `POSTGRES_PASSWORD`, `FIRECRAWL_API_KEY`, `LLM_API_KEY`.
Для DeepSeek используются `LLM_BASE_URL` и `LLM_MODEL`; пароль и логин оператора
берутся из `CONSOLE_PASSWORD`/`CONSOLE_USER` (по умолчанию `admin`/`admin`).
Пароль записывает одноразовый helper в отдельный том с правами runtime-пользователя;
приложения читают файл. `.env` в Git не добавляется.

- Клиент: http://localhost:18000/login — зарегистрируйте свою компанию.
- Оператор: http://localhost:18000/admin/console — одобрите регистрацию и профиль.
- Заявки: http://localhost:18000/intake/validation — откройте и одобрите заявку,
  при желании задав цель 25. После запуска здесь видны стадия и счётчики.
- Лиды: http://localhost:18000/lead-console/cards.

Порт меняется через `LEADGEN_DEMO_HTTP_PORT` в `.env`. Если операторские списки
пусты, выберите tenant зарегистрированного клиента в операторской консоли.

Пересборка выполняется той же командой запуска. Проверка и логи:

```bash
docker compose -p lead-gen-demo -f compose.demo.yaml ps
docker compose -p lead-gen-demo -f compose.demo.yaml logs --tail=100 app demand
```

Остановка с сохранением данных:

```bash
docker compose -p lead-gen-demo -f compose.demo.yaml stop
```

Это отдельный dev-контур существующего демо-пути: свои БД, RabbitMQ, Temporal и
постоянные тома. Он не использует production `POSTGRES_GREEN_DATA_VOLUME`,
`LEADGEN_RELEASE_STATE_VOLUME`, signing keys или пути `/Users/...`.
Ранее созданные клиенты и заявки штатного Compose остаются в его БД;
в демо регистрируется новый клиент. SMTP-доставка не запускается.
Реальный поиск зависит от доступности провайдеров и сайтов; недобор отображается.
