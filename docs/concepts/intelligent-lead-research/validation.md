# Проверка планировочной поставки

Дата: 15 октября 2026 года. Исходная версия: `897743865ec19997f3007586d532a5e4d0c0e0e5`. Ветка: `concept/intelligent-lead-research`.

## Выполнено

- `openspec validate intelligent-lead-research --strict` — valid.
- `openspec status --change intelligent-lead-research --json` — все planning artifacts готовы. Это статус планирования CLI, не выполнения реализации.
- Проверены относительные Markdown links/anchors, закрытие code fences и JSON журнала источников.
- Все 66 source receipts сверены по размеру/SHA-256 с реально прочитанными материалами; это не live integration benchmark.
- Семь новых capability имеют по одному primary owner в каталоге и явно помечены как план в доменной модели/диаграмме.
- Все 87 implementation tasks остаются открытыми; первичная поставка `72342aa` содержала 78 задач, уточнение `bd1b6b3` — 83.
- `git diff --check` / staged whitespace check — без ошибок.

## Архитектурный гейт

`go -C tools/archlint run .` выполнялся установленным Go toolchain с writable cache. Результат: одна ошибка — `catalog: capability "content-production" отсутствует в OpenSpec`.

Для сравнения исходный `HEAD` до этой поставки экспортирован во временный каталог через `git archive`; тот же archlint с `-root` на эту копию сообщил ровно ту же ошибку. Новых архитектурных ошибок после регистрации primary owners нет. Несогласованность `content-production` не исправлялась в концептуальном change.

## Границы проверки

Runtime/Compose/данные demo не менялись. `make check` и полные runtime suites не запускались для этой поставки документов/назначений владельцев. Реальные crawler/OCR/proxy/registry/mailbox integrations и отправка не выполнялись. PlantUML/Mermaid здесь проверялись по структуре и ссылкам, без отдельного rendering benchmark.

Реализация не объявляется завершённой; full project gates и реальные integrations нужны при apply согласно `AGENTS.md`. Тарифы, поддержка стран, лицензии конкретных release/model weights и победитель crawler benchmark должны проверяться перед соответствующим включением.

## Продолжение проработки

После `72342aa` уточнены критерии принятия/target unit, receipt-backed credit и пересчёт, lifecycle/приоритеты миссии, UI счётчиков и R1–R4. Для inbound добавлены conflicting correlation refs, pending-reply hold, new-body intents, исходы reply/send race и различие сообщений/уникальных лидов.

Изменены только существующие документы и planning artifacts. Владение capability/контекстами осталось прежним. Новых внешних источников/платных проверок в этом уточнении нет; журнал 66 прочитанных материалов сохраняется. Strict validation, 46 scoped links/anchors, whitespace, source receipts и task IDs проверены повторно; 83 задачи открыты. Archlint повторно сообщил только известную ошибку `content-production`. Полный runtime gate относится к будущему apply.

## Полный обязательный объём

Добавлен единый реестр REQ-01…REQ-36 и USE-01…USE-14; он связывает пожелания, capability, задачи, критерии и все 66 исходных source IDs. Видимые ссылки добавлены в корневой README, BRD, дорожную карту, журнал и концепцию. Полный объём подтверждён пользователем как обязательный; начальные прототипы не закрывают оставшиеся требования. Машиночитаемое покрытие хранится в `implementation-coverage.json`; задачи приёмки остаются открытыми.

Проверка полноты этого уточнения: 36/36 required требований имеют действующие task/capability/document refs; 66/66 материалов связаны с 14 решениями применения; все 86 открытых задач имеют связь с реестром. Ссылки и strict OpenSpec проверены. Archlint по-прежнему сообщает только исходную несогласованность `content-production`; runtime/full-program acceptance не выполнялась.

## Сверка исходных уточнений пользователя

В schema v2 implementation-coverage.json добавлены 14 записей CL-00…CL-13 с цитатами и 6 записей D-01…D-06 с происхождением/статусом проектных предложений. Поле `requirement` заменяет неоднозначное `user_requirement`; provenance явно отделяет user-explicit, user-derived и proposed-design. Продуктовые возможности обязательны, выбранные пользователем библиотеки/пилотные настройки не выдумываются. Режим чужих форм остаётся открытым продуктовым решением; добавлена задача 9.7, всего 87 открытых задач.

Дополнены нормативные требования собственного поиска и corpus/index. В старом self-hosted-crawler proposal сохранена история выбора Crawl4AI с явной ссылкой на последующее разрешение сравнения. Рабочая реализация/Compose не менялись; сверка и исправление документов не выдаются за выполненный crawler/OCR/mailbox путь.

Проверка сверки: оба change (`intelligent-lead-research`, `self-hosted-crawler`) прошли strict validation; JSON/provenance/task refs, 14 исходных уточнений, 6 проектных решений и ссылки проверены. Архитектурный гейт имеет только ранее зафиксированную ошибку `content-production`. Ни режим отправки форм, ни конкретный альтернативный crawler не объявлены новым решением пользователя.
