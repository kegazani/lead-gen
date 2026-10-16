# Дополнительный поисковый источник SearXNG

Это экспериментальный backend нашего metasearch gateway, выбранный для проверки MS-H03. Firecrawl остаётся доступным источником и инструментом чтения страниц. SearXNG не является независимым индексом интернета: доступность и качество зависят от включённых upstream engines.

Из корня репозитория:

```sh
python3 deploy/research/searxng/generate-settings.py
docker compose --project-directory . -f deploy/research/searxng/compose.yaml --profile searxng up -d
curl -fsS -X POST http://127.0.0.1:18888/search \
  --data-urlencode 'q=строительные компании' --data 'format=json&language=ru-RU'
```

Генератор включает только `duckduckgo,brave`, JSON и ограниченные timeouts; список меняется через `RESEARCH_SEARXNG_ENGINES`. Секрет создаётся один раз в `.runtime/settings.yml`. Настройки и proxy credentials не коммитятся. Для запросов к upstream используется уже существующий `SEARXNG_OUTGOING_PROXY`, иначе `HTTPS_PROXY` / `HTTP_PROXY`; денежный gate для него отсутствует.

Для proxy с собственным CA до генерации установите `SEARXNG_CA_BUNDLE=/run/lead-research/ca.pem`; в запуск Compose добавьте `-f deploy/research/searxng/compose.proxy-ca.yaml`, передав `SEARXNG_PROXY_CA_FILE` с путём существующего host CA bundle. Проверка TLS остаётся включённой.

HTTP200 не означает успех всех engines: `unresponsive_engines` сохраняется в receipt. При отказе всех engines gateway возвращает `unavailable`, полезные результаты оставшихся engines — `partial`. Поддержка языка означает применённый параметр SearXNG; страна выдачи, egress и поддержка операторов каждого engine требуют отдельной проверки. В первой версии cursor не поддержан; технический предел числа попыток задаёт deployment, количество лидов этим не определяется.

API приложения: `integrationplatform.NewSearXNGResearch` использует клиентский PostgreSQL с `migrations/red/0002_proxy_research_acquisition.sql`, endpoint (обычно `http://searxng:8080` внутри общей Compose network), явный список engines и технические limits. Учёт содержит source slots, scoped attempts, receipts, cooldown и fencing; Firecrawl account/grant таблицы не используются. Snapshot/replay не делает новый сетевой вызов. `Fetch` не заявлен — чтение страниц выполняет разрешённый fetch-source миссии.

Live принятие требует результатов именно нашего экземпляра, измерения полезных новых источников и наблюдения отказов engines. Доступность контейнера и JSON сама по себе не завершает задачу 6.3.

Проверенный image manifest (2026-10-15): `ghcr.io/searxng/searxng@sha256:ad273460b12390ab1ad9c6ea630dafed39045ab0b9096a85ca31f5246517c670`, platforms amd64/arm64/armv7. Digest закреплён в Compose; это факт identity образа, а не утверждение успешности внешнего поиска.

## Фактический запуск 2026-10-15

Официальный контейнер запущен с закреплённым digest, исходящим proxy и существующим CA bundle. `/healthz` вернул `OK`; реальный `/search` и вызов нового Go-адаптера вернули HTTP200 с нулём результатов и `proxy error` для обоих engines. Gateway сохранил scoped receipt со статусом `unavailable`, `no_engine_results`; отсутствующие результаты не представлены как успешный пустой поиск. На host те же домены `html.duckduckgo.com` и `search.brave.com` получили `CONNECT 403 Forbidden` от штатного proxy — внешний поиск блокирует egress policy среды. Политика не обходилась.

[Машиночитаемый trial](trial-2026-10-15.json) фиксирует identity образа, исходы и остающиеся проверки. До повторного успешного web-search в разрешённой deployment-сети SearXNG остаётся экспериментальным дополнительным источником; задача 6.3 не считается принятой.

Явный live test через реальный PostgreSQL и наш экземпляр (обычные тесты не обращаются в интернет):

```sh
LEADGEN_SEARXNG_LIVE_URL=http://127.0.0.1:18888 \
  go test ./backend/contexts/integration-platform -run TestSearXNGResearchLive -v -count=1
```

Для этой команды также задайте `LEADGEN_POSTGRES_TEST_DSN` клиентской тестовой базы. Неуспех upstream отражается провалом live acceptance, а не заменяется fixture.
