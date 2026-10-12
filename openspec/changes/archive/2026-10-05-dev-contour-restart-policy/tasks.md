## 1. Политика перезапуска в `compose.yaml`

- [x] 1.1 Добавить `restart: unless-stopped` в YAML-якорь `x-app` (`compose.yaml`) — это покрывает шесть долгоживущих групп процессов: `core`, `delivery`, `demand`, `intake`, `public`, `platform`. Проверка: `docker compose config` валиден; у всех шести сервисов в выводе присутствует `restart: unless-stopped`.
- [x] 1.2 Добавить `restart: unless-stopped` в сервисы `postgres-green`, `postgres-red`, `rabbitmq`, `mailpit`, `postgres-temporal`, `temporal`, `temporal-ui`. Проверка: `docker compose config` валиден; у всех семи инфраструктурных сервисов в выводе присутствует `restart: unless-stopped`.
- [x] 1.3 Не менять ничего другого по смыслу: `command`, `environment`, `ports`, `volumes`, `healthcheck`, `depends_on` и тома остаются прежними. Проверка: диф правки `compose.yaml` содержит только вставки `restart: unless-stopped`.

## 2. Верификация

- [x] 2.1 Проверить валидность конфигурации. Проверка: `cd /Users/kgz/Desktop/p/lead-gen && POSTGRES_PASSWORD=x FIRECRAWL_API_KEY=x docker compose config -q && echo OK` печатает `OK`.
- [x] 2.2 Проверить, что политика перезапуска не введена заглушкой и не подменяет продуктовую функцию (`AGENTS.md` §3). Проверка: изменение — конфигурация окружения, заглушек не вводит.

## 3. Гейт

- [x] 3.1 Прогнать `openspec validate dev-contour-restart-policy --strict`. Проверка: `valid`.
- [x] 3.2 Прогнать `make check` (build/test/vet/importlint/archlint/openspec). Проверка: зелёный.
