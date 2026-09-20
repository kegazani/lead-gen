## 1. Перенос каталогов и путей модулей

- [x] 1.1 Переместить служебный код под `backend/`: `cmd/` → `backend/cmd/`, `contexts/` → `backend/contexts/`, `platform/` → `backend/platform`. Проверка: `ls backend` показывает `cmd`, `contexts`, `platform`; старые пути `cmd/`, `contexts/`, `platform/` отсутствуют. (`git mv` оказался неприменим: служебный код не был закоммичен — `git ls-files cmd contexts platform` пуст, — поэтому перенос выполнен обычным `mv`.)
- [x] 1.2 Переименовать пути модулей в `go.mod`: `lead-gen/backend/cmd/lead-gen`, `lead-gen/backend/contexts/<ctx>`, `lead-gen/backend/platform`; обновить секцию `use` в `go.work`. Проверка: `go build ./...` зелёный в каждом перенесённом модуле.
- [x] 1.3 Переписать импорты `lead-gen/cmd/...`, `lead-gen/contexts/...`, `lead-gen/platform/...` на `lead-gen/backend/...`. Проверка: `grep -rn '"lead-gen/\(cmd\|contexts\|platform\)/' --include=*.go .` не находит вхождений, кроме фикстур `tools/importlint/testdata`; `make build` зелёный.
- [x] 1.4 Обновить `MODULES` и цели `Makefile` под новые каталоги. Проверка: `make build test vet` зелёные.
- [x] 1.5 Обновить копирование в корневом `Dockerfile` и в `frontend/public/Dockerfile` (пути `backend/...`, минимальный `go.work` в образе edge). Проверка: `docker build -f Dockerfile .` и `docker build -f frontend/public/Dockerfile .` собираются.

## 2. Разложение `backend/platform` по назначению

- [x] 2.1 Создать подпакеты и перенести файлы: `composition/`, `contracts/` (envelope, routing, version), `messaging/` (outbox, inbox, idempotency), `observability/` (observ, prometheus), `reliability/` (resilience), `transport/` (http, amqp), `migrate/`. Проверка: `go -C backend/platform build ./...` зелёный, `ls backend/platform` соответствует Решению 3 `design.md`.
- [x] 2.2 Обновить импорты потребителей `backend/platform/...` в контексте, `backend/cmd/lead-gen` и `frontend/...`. Проверка: `make build test vet` зелёные.

## 3. Сведение знания о деплое в `backend/platform/deployment`

- [x] 3.1 Перенести строгий парсер `deploy/services.yaml` из `backend/cmd/lead-gen/deploy.go` в `backend/platform/deployment`. Проверка: тесты разбора (`deploy_test.go`) перенесены и зелёные.
- [x] 3.2 Перенести загрузчик `infrastructure.yaml` и топологии из бывшего `platform/topology` в `backend/platform/deployment`. Проверка: тесты загрузки зелёные.
- [x] 3.3 Перевести `backend/cmd/lead-gen` на пакет `deployment` как потребителя. Проверка: `go -C backend/cmd/lead-gen test ./...` зелёный; `deploy/` содержит только `services.yaml`, `infrastructure.yaml`, `tofu/`, `README.md`.

## 4. `tools/importlint` под новую грамматику путей

- [x] 4.1 Обновить `parseRole`: распознавать `backend/contexts/<ctx>/internal/<layer>`, `backend/contracts`, `backend/platform`, `backend/cmd`, `frontend/...`. Проверка: unit-тесты `parseRole` покрывают старые и новые ветки и зелёные.
- [x] 4.2 Обновить фикстуры `tools/importlint/testdata/**` под новые пути (негативные случаи: импорт чужого `internal`, чужого хранилища, edge→context — сохраняются). Проверка: `go -C tools/importlint test ./...` зелёный.
- [x] 4.3 Убедиться, что `contracts/` остаётся разделяемым (импорт разрешён из `backend/...` и `frontend/...`). Проверка: `go -C tools/importlint run .` сообщает 0 запрещённых рёбер.

## 5. Компонентный фронтенд

- [x] 5.1 Создать модуль `lead-gen/frontend/components` и добавить его в `go.work`. Проверка: `go -C frontend/components build ./...` зелёный.
- [x] 5.2 Вынести общий слой серверных компонентов на `html/template`: layout, переиспользуемые фрагменты, формы, разбор ошибок в представление. Проверка: пакеты компонентов собираются, в них нет доменных терминов и импортов `backend/...`.
- [x] 5.3 Перевести `frontend/public` на `frontend/components`; разложить монолитные шаблоны `internal/web/templates/*` на компоненты. Проверка: маршруты и ответы edge не изменились — существующие тесты `frontend/public` (`web`, `assembly`, `adapters/stub`) зелёные.

## 6. Документация

- [x] 6.1 Обновить карту репозитория в `README.md` под раскладку `backend/`, `contracts/`, `frontend/{public,components}`, `deploy/`, `architecture/`, `docs/`, `tools/`. Проверка: пути в таблице существуют на диске.
- [x] 6.2 Переписать раздел «Реализация на Go» в `docs/domain-model.md`: `backend/contexts/<context-id>/{domain,app,ports,adapters}`, `backend/platform`, `backend/cmd/lead-gen`. Проверка: упоминания старых путей `internal/*` в `docs/domain-model.md` отсутствуют.
- [x] 6.3 Обновить ссылки на пути в `docs/architecture.md` §2 и `deploy/README.md`. Проверка: `grep -rn 'internal/`\\(contexts\|platform\|composition\\)' docs deploy` не находит вхождений.
- [x] 6.4 Синхронизировать ссылки на пути и место слоя компонентов в незакрытых change'ах (`web-frontend-architecture`, `service-per-context`, `migrate-platform-to-go`, `service-topology-console`): заменить устаревшие пути `internal/*` на актуальные (`backend/*`, `frontend/components`), не меняя продуктовые требования. Проверка: `grep -rn 'internal/`\\(contexts\|platform\|composition\\)' openspec/changes --include=*.md` не находит вхождений в незакрытых change'ах.

## 7. Финальная проверка

- [x] 7.1 Запустить полный контур проверки. Проверка: `make check` (сборка, тесты, `vet`, `importlint`, `archlint`, `openspec`) зелёный.
- [x] 7.2 Проверить, что доменная модель и каталог не изменились. Проверка: `go -C tools/archlint run .` зелёный (14 контекстов, 22 агрегата). Примечание: `architecture/domain-catalog.yaml` и `docs/domain-model.md` имели незакоммиченные правки ещё до этого change'а; сам change `domain-catalog.yaml` не трогал, а в `docs/domain-model.md` изменён только раздел «Реализация на Go».
- [x] 7.3 Проверить, что раскладка читается из корня. Проверка: на верхнем уровне присутствуют `backend/`, `frontend/`, `deploy/`, `architecture/`, `docs/`, `openspec/`, `tools/`, `contracts/`, и корневых `cmd/`, `contexts/`, `platform/` нет.
