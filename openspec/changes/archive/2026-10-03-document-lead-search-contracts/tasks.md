# Задачи: догоняющая документация контрактов `lead-search` и `email-generation`

Код уже реализован; изменение носит документационный характер. Каждый пункт выполнен и содержит способ проверки. Ни одна задача не меняет код и тесты.

## 1. Сверка реализованного поведения

- [x] 1.1 Сверить контракт `lead-search`: `contracts/lead-management/leadsearch/leadsearch.go` (Query/Result/LeadRef, `OriginPool`/`OriginCrawl`, `ErrRejected`/`ErrUnavailable`), `internal/app/search_leads.go` (пул → дозабор, лимит/дедлайн/топ-N, токен-матчинг и порог, дедуп по домену, деградация без выдумки) и `internal/adapters/http/leadsearch.go` (wire-форма, 400/503/500); проверить чтением этих файлов и тестов `leadsearch_test.go`, `search_leads_test.go`
- [x] 1.2 Сверить контракт `email-generation`: `contracts/lead-management/emailgeneration/emailgeneration.go` (Request/Email, `ErrRejected`/`ErrUnavailable`), `internal/app/draft_email.go` (чтение карточки и контакта, обезличенный промпт, разбор ответа, доменный отказ вместо выдумки) и `internal/adapters/http/emailgeneration.go` (wire-форма, 400/503/500); проверить чтением этих файлов и тестов `emailgeneration_test.go`, `draft_email_test.go`
- [x] 1.3 Сверить политику транспорта: `internal/adapters/http/server.go` объявляет `POST /lead-search` и `POST /email-generation`, а `authMiddleware` перечисляет их в `consoleauth.Policy.Public`; поведение `isPublic` в `backend/platform/consoleauth/consoleauth.go` пропускает путь без сессии и CSRF; проверить чтением этих файлов
- [x] 1.4 Проверить отсутствие дублей: capability `semantic-lead-search` (векторный recall), `company-quota-search` (поведение задания), `lead-email` (отправка из консоли) не покрывают межконтекстные контракты; проверить чтением `openspec/specs/<capability>/spec.md`

## 2. Планировочные артефакты

- [x] 2.1 Написать `proposal.md` (Why/What/Capabilities/Impact, affected bounded contexts `lead-management`, `request-fulfillment`, `integration-platform`); проверить `openspec validate document-lead-search-contracts --strict`
- [x] 2.2 Написать дельты `specs/lead-search/spec.md` и `specs/email-generation/spec.md` (`## Purpose` + `## ADDED Requirements`, сценарии WHEN/THEN в формате `#### Scenario:`); проверить `openspec validate document-lead-search-contracts --strict`
- [x] 2.3 Написать `design.md` (контекст, цели/non-goals, решения по границе доверия и матчингу, компромиссы, план миграции); проверить `openspec validate document-lead-search-contracts --strict`
- [x] 2.4 Написать `tasks.md` с выполненными пунктами и способом проверки каждого; проверить `openspec validate document-lead-search-contracts --strict`

## 3. Каталог доменной архитектуры

- [x] 3.1 Добавить `lead-search` и `email-generation` владельцу `lead-management` в `architecture/domain-catalog.yaml` — в `capabilities` и `contracts`; проверить `go -C tools/archlint run .` (каждая capability OpenSpec имеет ровно один primary context, каждый контракт объявлен у владельца)
- [x] 3.2 Убедиться, что каталог не вводит новых сущностей, агрегатов, событий, policy и relationships; проверить `go -C tools/archlint run .` без ошибок и неизменность остальных секций
- [x] 3.3 Прогнать `go -C tools/archlint run .`; проверить код возврата `0` и сводку без нарушений

## 4. Проверка и архивация

- [x] 4.1 Прогнать `openspec validate --all --strict`; проверить, что все change и spec валидны, включая новые `lead-search` и `email-generation`
- [x] 4.2 Синхронизировать дельты в `openspec/specs/lead-search/spec.md` и `openspec/specs/email-generation/spec.md` (перенос `## Purpose` и `## ADDED Requirements`) и проверить отсутствие плейсхолдеров `TBD`
- [x] 4.3 Архивировать change в `openspec/changes/archive/<дата>-document-lead-search-contracts/`; проверить наличие каталога и отсутствие активного change
- [x] 4.4 Повторно прогнать `openspec validate --all --strict` и `go -C tools/archlint run .`; проверить оба зелёными
