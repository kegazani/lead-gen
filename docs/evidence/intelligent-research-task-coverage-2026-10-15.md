# Независимый read-only аудит покрытия intelligent-lead-research

Срез: 2026-10-15T17:00:55+00:00. Проверены **94/94 task IDs** и их blueprint inputs/outputs/dependencies/acceptance. Ветку/задачи/root docs аудит не изменяет. Применены openspec-apply-change и openspec-verify-change; CLI сообщает `ready`, 0 complete / 94 remaining. Полный `make check` здесь не запускался: его выполняет root. Последний root run прошёл build/test/vet/importlint/archlint; общий OpenSpec `--all --strict` отказал на 30 existing specs/changes из-за warnings >500 символов. `intelligent-lead-research --strict` valid. Сравнение с HEAD `364c858` подтвердило те же 30 отказов на исходных спецификациях; общий гейт остаётся открытым. Код продолжает изменяться параллельно; новые результаты после этой отметки требуют точечного пересмотра.

## Вывод

**Полностью удовлетворённых задач по полному blueprint и требуемой приёмке: 0/94.** Это не утверждение, что работа отсутствует: есть настоящий search-first runtime, реальные Firecrawl/DeepSeek/PG проверки и значительное покрытие устойчивости/изоляции. Но ни работающий узкий маршрут, ни зелёные линтеры не заменяют отсутствующие критерии каждого целого task.

| Статус | Количество |
|---|---:|
| Полностью принято | 0 |
| Частично реализовано/проверено | 53 |
| Только проектирование/зависимость | 7 |
| Нет новой реализации task | 34 |

«Нет новой реализации» относится к полной research-программе; существующие legacy функции не объявлены отсутствующими. Каждая строка ниже указывает конкретный недостающий результат. Отдельные функции и regression checks успешно приняты в рамках среза, но чекбоксы всего task остаются открытыми.

## Что непосредственно подтверждено

- Human request selection и mutually-exclusive execution mode; durable mission collection; Firecrawl account/grant/operation ledger; memory с tenant/purpose и provenance; human identity decisions/candidates; query/HTTP/JS прогресс и dossier.
- После последних исправлений criteria credit использует **целые immutable assessment sets этой миссии** с точными observation/criteria/locator refs. Нельзя получить зачёт из чужой миссии с той же числовой criteria version; поздняя contact-only unknown оценка не стирает действующее положительное основание.
- Lifecycle request остаётся у request-fulfillment: Reconciler читает mission до owner Tx, проверяет tenant/purpose/selection version/unit/quantity, затем вызывает собственные StartProcessing/Fulfill/Fail/Cancel. Повтор/конкурентный проход проверяется PG test и не создаёт лишние outbox events.
- `0003_research_provider_admission.sql` сохраняет `provider_admitted` до платного POST. Pending balance probes не вычитают друг друга; уже admitted/unknown операции и расход после probe учитываются под account row lock. Миграция консервативно помечает старые running/unknown операции admitted. Это исправляет гонку последних credits, не превращаясь в monetary gate для SearXNG/LLM.
- Settlement obligation сохраняется независимо от useful receipt и terminal outcome. Actual FC adapter/ledger + mission + memory PostgreSQL regression отклоняет owner finish через SQL trigger, пересоздаёт pool и после 11 минут подтверждает held/unknown без повторного POST и изменения terminal members. Пять дополнительных mission PostgreSQL сценариев проверяют потерю ACK, crash/restart lease, mismatched receipt и запрет следующего attempt при неопределённой фиксации. Это реальные БД/production adapters с тестовым HTTP provider, не повтор платного внешнего вызова.
- Новый transport validator проверяет только sync command/query, явный deployment/co-location, public owner interface/constructor и действительную сборку потребителя. Event/policy остаются async/amqp. Три consumer suites получили точные 11 research edges, а не wildcard для всех in-process.

## Критические пробелы перед приёмкой программы

1. Нет independent corpus/holdout/quality evaluator; поэтому нельзя объявить contact-link/false-merge качество на основании нескольких regression fixtures или успешного company-unit run.
2. Нет полного международного MarketStudy, country profiles/quotas, собственного acquisition/browser/OCR/index и почтового/формового цикла. Разделы7–10 в основном остаются реализационной работой.
3. API negative memory и OpenNeed существуют, но исполнитель не использует reusable attempts/need assignment как полноценный planner input. SourceFollowups ограничен доказанными same-origin contact/company Markdown ссылками, а не общей интеллектуальной цепочкой registry→company→domain→third-party contact.
4. Identity graph пересобирается в tenant/purpose scope. Это сознательно наблюдаемая первая реализация, но blueprint1.2 прямо требует не запирать весь tenant graph одним агрегатом; нет bounded affected-group/reverse-dependency processing.
5. Durable PostgreSQL execution не является новым Temporal workflow: нет registered planner block, full intent/outbox/ReserveForAction saga, versioned history replay/ContinueAsNew и всего completion settlement acceptance. После durable settled-unknown terminal mission снимается с очереди; поздний отдельный ledger.Reconcile пока не обновляет её budget projection автоматически.
6. SearXNG поднят и health/API доступны, но реальная выдача upstream engines блокируется CONNECT403 текущего proxy; task6.3 не пройден. Firecrawl run нашёл компании, а не достаточный набор реальных контактов/emails.
7. Legacy lead list/cards/email и research dossier пока раздельны; нет bridge, new material handoff, exact recipient approval и реального ответа. Нельзя говорить пользователю, что вся новая интеллектуальная программа уже доставляет письма.

## Проверка staged-fixture extraction

Изменение прочитано независимо по `staged-fixture-review.diff` и текущим `db_jobs.go`/`db_jobs_test.go`:

- production `verifyStagedSecretBundle` по-прежнему отклоняет любой `volumeDir != /run/secrets`, затем передаёт неизменённый `readSecretFile`;
- общий verifier сохраняет bundle registry/generation checks, lstat directory/no-symlink/0500/UID-GID65532, точный generation marker, allowlist/readability и rejection лишних файлов;
- private seam добавляет только выбираемый reader; production путь не получает возможности выбрать его снаружи;
- fixture открывает реальные staged bytes `O_NOFOLLOW|O_CLOEXEC|O_NONBLOCK`, проверяет regular/0400/65532:65532/nlink1/size, читает тот же descriptor. Это не reader с фиксированным success;
- assertion alternate production path теперь отдельный; stale generation и extra-file assertions сохранены; существующий TestSecretFileOwnerMustMatchRuntimeUID проверяет euid;
- пять targeted root tests реально PASS без skips (`staged-fixture-tests.log`); записи host `/run/secrets` и ослабления production UID policy нет.

Наблюдаемого изменения production семантики в этом extraction не найдено. Это кодовое исправление тестируемости по отдельному разрешению, не обход onboarding policy.

## Доказательства и точки кода

### E00. Архитектурные границы

- [architecture/domain-catalog.yaml](../../architecture/domain-catalog.yaml#L429)
- [deploy/research-client.json](../../deploy/research-client.json#L22)
- [tools/archlint/inprocess.go](../../tools/archlint/inprocess.go#L39)
- [tools/archlint/inprocess_test.go](../../tools/archlint/inprocess_test.go#L40)
- [backend/platform/topology/topology_test.go](../../backend/platform/topology/topology_test.go#L382)

### E01. Human request и режим исполнения

- [backend/contexts/request-fulfillment/internal/domain/research_execution.go](../../backend/contexts/request-fulfillment/internal/domain/research_execution.go#L19)
- [backend/contexts/request-fulfillment/internal/app/researchbridge/service.go](../../backend/contexts/request-fulfillment/internal/app/researchbridge/service.go#L231)
- [backend/contexts/request-fulfillment/migrations/green/0011_research_execution_mode.sql](../../backend/contexts/request-fulfillment/migrations/green/0011_research_execution_mode.sql#L1)

### E02. Planner и grounded analyzer

- [contracts/agent-orchestration/researchplan/plan.go](../../contracts/agent-orchestration/researchplan/plan.go#L53)
- [contracts/agent-orchestration/researchplan/countries.go](../../contracts/agent-orchestration/researchplan/countries.go#L7)
- [backend/contexts/agent-orchestration/internal/app/researchplanner/planner.go](../../backend/contexts/agent-orchestration/internal/app/researchplanner/planner.go#L42)
- [backend/contexts/lead-management/internal/app/researchanalysis/analyzer.go](../../backend/contexts/lead-management/internal/app/researchanalysis/analyzer.go#L30)
- [contracts/integration-platform/llm/research_extract.go](../../contracts/integration-platform/llm/research_extract.go#L19)

### E03. Память, контакты, need/attempt

- [backend/contexts/lead-management/internal/domain/researchmemory/observation.go](../../backend/contexts/lead-management/internal/domain/researchmemory/observation.go#L178)
- [backend/contexts/lead-management/internal/adapters/persistence/postgres/research_memory.go](../../backend/contexts/lead-management/internal/adapters/persistence/postgres/research_memory.go#L56)
- [backend/contexts/lead-management/internal/adapters/persistence/postgres/research_memory.go](../../backend/contexts/lead-management/internal/adapters/persistence/postgres/research_memory.go#L572)
- [backend/contexts/lead-management/internal/adapters/persistence/postgres/research_identity.go](../../backend/contexts/lead-management/internal/adapters/persistence/postgres/research_identity.go#L100)
- [backend/contexts/lead-management/internal/adapters/persistence/postgres/research_identity_test.go](../../backend/contexts/lead-management/internal/adapters/persistence/postgres/research_identity_test.go#L72)

### E04. Миссия, identity, immutable состав

- [backend/contexts/agent-orchestration/internal/domain/research_mission.go](../../backend/contexts/agent-orchestration/internal/domain/research_mission.go#L18)
- [backend/contexts/agent-orchestration/internal/app/research/execution.go](../../backend/contexts/agent-orchestration/internal/app/research/execution.go#L19)
- [backend/contexts/agent-orchestration/internal/app/research/identity.go](../../backend/contexts/agent-orchestration/internal/app/research/identity.go#L19)
- [backend/contexts/agent-orchestration/internal/app/research/followups.go](../../backend/contexts/agent-orchestration/internal/app/research/followups.go#L15)
- [backend/contexts/agent-orchestration/research_integration_test.go](../../backend/contexts/agent-orchestration/research_integration_test.go#L250)

- [backend/contexts/agent-orchestration/internal/app/research/settlement.go](../../backend/contexts/agent-orchestration/internal/app/research/settlement.go#L25)
- [backend/cmd/lead-gen/research_settlement_test.go](../../backend/cmd/lead-gen/research_settlement_test.go#L38)
- [backend/contexts/agent-orchestration/research_integration_test.go](../../backend/contexts/agent-orchestration/research_integration_test.go#L1064)

### E05. Request lifecycle

- [backend/contexts/request-fulfillment/internal/app/researchbridge/reconcile.go](../../backend/contexts/request-fulfillment/internal/app/researchbridge/reconcile.go#L65)
- [backend/contexts/request-fulfillment/internal/adapters/persistence/postgres/research_reconcile_test.go](../../backend/contexts/request-fulfillment/internal/adapters/persistence/postgres/research_reconcile_test.go#L32)
- [backend/contexts/request-fulfillment/internal/adapters/persistence/inmemory/research_reconcile_test.go](../../backend/contexts/request-fulfillment/internal/adapters/persistence/inmemory/research_reconcile_test.go#L35)

### E06. Firecrawl reservation/admission

- [backend/contexts/integration-platform/migrations/red/0003_research_provider_admission.sql](../../backend/contexts/integration-platform/migrations/red/0003_research_provider_admission.sql#L1)
- [backend/contexts/integration-platform/internal/adapters/researchacquisition/store.go](../../backend/contexts/integration-platform/internal/adapters/researchacquisition/store.go#L212)
- [backend/contexts/integration-platform/internal/adapters/researchacquisition/client.go](../../backend/contexts/integration-platform/internal/adapters/researchacquisition/client.go#L110)
- [backend/contexts/integration-platform/internal/adapters/researchacquisition/client_test.go](../../backend/contexts/integration-platform/internal/adapters/researchacquisition/client_test.go#L451)

### E07. SearXNG технический источник

- [backend/contexts/integration-platform/internal/adapters/researchsearxng/store.go](../../backend/contexts/integration-platform/internal/adapters/researchsearxng/store.go#L29)
- [backend/contexts/integration-platform/internal/adapters/researchsearxng/client.go](../../backend/contexts/integration-platform/internal/adapters/researchsearxng/client.go#L33)
- [deploy/research/searxng/compose.yaml](../../deploy/research/searxng/compose.yaml#L1)
- [deploy/research/searxng/trial-2026-10-15.json](../../deploy/research/searxng/trial-2026-10-15.json#L1)

### E08. HTTP/JS/progress/dossier

- [backend/contexts/request-fulfillment/internal/adapters/http/templates/research.html](../../backend/contexts/request-fulfillment/internal/adapters/http/templates/research.html#L36)
- [backend/contexts/request-fulfillment/internal/adapters/http/templates/research_dossier.html](../../backend/contexts/request-fulfillment/internal/adapters/http/templates/research_dossier.html#L9)
- [backend/contexts/request-fulfillment/internal/adapters/http/research_progress_test.go](../../backend/contexts/request-fulfillment/internal/adapters/http/research_progress_test.go#L9)
- [backend/contexts/request-fulfillment/internal/adapters/http/testdata/research-progress.test.cjs](../../backend/contexts/request-fulfillment/internal/adapters/http/testdata/research-progress.test.cjs#L1)

### E09. Точные основания зачёта

- [backend/contexts/agent-orchestration/internal/app/research/criteria_support.go](../../backend/contexts/agent-orchestration/internal/app/research/criteria_support.go#L32)
- [backend/contexts/agent-orchestration/internal/app/research/assessment_sets.go](../../backend/contexts/agent-orchestration/internal/app/research/assessment_sets.go#L21)
- [backend/contexts/agent-orchestration/internal/app/research/criteria_support_test.go](../../backend/contexts/agent-orchestration/internal/app/research/criteria_support_test.go#L55)
- [backend/cmd/lead-gen/research_criteria_test.go](../../backend/cmd/lead-gen/research_criteria_test.go#L21)

### E10. Живые проверки и ограничения

- [docs/evidence/intelligent-research-runtime-2026-10-15.md](../../docs/evidence/intelligent-research-runtime-2026-10-15.md#L1)
- [backend/cmd/lead-gen/research_runtime_test.go](../../backend/cmd/lead-gen/research_runtime_test.go#L34)
- [backend/cmd/lead-gen/research_ui_acceptance_test.go](../../backend/cmd/lead-gen/research_ui_acceptance_test.go#L32)
- [deploy/research/README.md](../../deploy/research/README.md#L1)

### E11. Secure-file testability

- [backend/cmd/lead-gen/db_jobs.go](../../backend/cmd/lead-gen/db_jobs.go#L732)
- [backend/cmd/lead-gen/db_jobs_test.go](../../backend/cmd/lead-gen/db_jobs_test.go#L433)

### E15. План и полный scope

- [openspec/changes/intelligent-lead-research/design.md](../../openspec/changes/intelligent-lead-research/design.md#L1)
- [docs/concepts/intelligent-lead-research/task-blueprints.json](../../docs/concepts/intelligent-lead-research/task-blueprints.json#L1)
- [docs/concepts/intelligent-lead-research/implementation-coverage.json](../../docs/concepts/intelligent-lead-research/implementation-coverage.json#L1)
- [docs/concepts/intelligent-lead-research/09-browser-ocr-and-tool-selection.md](../../docs/concepts/intelligent-lead-research/09-browser-ocr-and-tool-selection.md#L1)

Локальные дополнительные evidence: `acquisition-final-review.md`, `mission-implementation.md`, `planner-result.md`, `analysis-result.md`, `criteria-support-result.md`, `staged-fixture-tests.log`, `topology-catalog-tests.log`, `live-root-fixed.log` в `/tmp/lead-research-implementation/`. Эти отчёты сверялись с кодом; результаты владельцев не переименованы в полный acceptance. Сетевые/provider fixtures, реальные PostgreSQL tests и реальные paid provider calls различаются.

## Полная матрица 94 задач

Ссылки на task являются точками критериев; `E..` — конкретные code/test paths выше. В строках описаны пределы текущего среза, а не предложение вычеркнуть оставшуюся часть программы.

### Раздел 1

| Task | Статус | Подтверждено | Недостающее для целого task | Evidence |
|---|---|---|---|---|
| [1.1](../../openspec/changes/intelligent-lead-research/tasks.md#L11) | Частично | Human quantity/unit/offer/ICP/version сохраняются; общей цели нет. | Нет полного quota/min-max snapshot и доступного email-ready approval пути; source-of-field/unknown policy не представлены для всех критериев. | E01,E02,E05 |
| [1.2](../../openspec/changes/intelligent-lead-research/tasks.md#L12) | Частично | Фактические первые aggregates/VO/ports/red tables и 62 связи зарегистрированы; archlint/importlint проходят. | Полная design-модель dependencies/events и разделение green metadata/red payload ещё не поставлены; identity map блокирует tenant/purpose целиком, вопреки указанию blueprint не запирать весь tenant graph. | E00,E03,E04,E06 |
| [1.3](../../openspec/changes/intelligent-lead-research/tasks.md#L13) | Только проектирование | В design описана зависимость source/robots policy и сохранение demo. | Нет принятого effective policy для будущего self-hosted маршрута, согласованного с обоими existing/pending changes и проверенного в runtime. | E15 |
| [1.4](../../openspec/changes/intelligent-lead-research/tasks.md#L14) | Только проектирование | Один model gateway и grounded analyzer реализованы для Firecrawl Markdown. | Нет окончательного ADR/сравнения с Crawl4AI LLMExtractionStrategy на выбранном собственном runtime; end-to-end lineage будущего crawler не проверен. | E02,E15 |
| [1.5](../../openspec/changes/intelligent-lead-research/tasks.md#L15) | Частично | Версионированные search/fetch/analysis/receipt контракты различают capabilities и geography. | Нет поставленных versioned registry/browser/document/OCR/local-index adapters/manifests со всей требуемой семантикой. | E02,E06,E07 |
### Раздел 2

| Task | Статус | Подтверждено | Недостающее для целого task | Evidence |
|---|---|---|---|---|
| [2.1](../../openspec/changes/intelligent-lead-research/tasks.md#L19) | Нет новой реализации | Есть отдельные regression fixtures в тестах. | Нет разрешённого размеченного corpus manifest и независимого holdout с контролем source-family leakage. | E15 |
| [2.2](../../openspec/changes/intelligent-lead-research/tasks.md#L20) | Нет новой реализации | Есть концепция сравнения инструментов. | Нет 120 primary objects с labels/scopes/locators и отдельного странового holdout. | E15 |
| [2.3](../../openspec/changes/intelligent-lead-research/tasks.md#L21) | Нет новой реализации | Live logs содержат отдельные latency/credits/counters. | Нет воспроизводимого evaluator field/link precision/recall, false merge и denominator-aware quality report. | E15 |
| [2.4](../../openspec/changes/intelligent-lead-research/tasks.md#L22) | Только проектирование | Критерии и риски cutover описаны концептуально. | Нет принятых cohort policy versions, sample sizes/failed strata и отчёта нулевых critical errors на acceptance. | E15 |
| [2.5](../../openspec/changes/intelligent-lead-research/tasks.md#L23) | Частично | Сохранены 36REQ/14USE/source receipts/94blueprints; добавлен factual runtime evidence. | Implementation-coverage ещё в основном pending и не связывает каждый USE/источник с конкретным новым code/test/проверенным решением. | E15,E10 |
### Раздел 3

| Task | Статус | Подтверждено | Недостающее для целого task | Evidence |
|---|---|---|---|---|
| [3.1](../../openspec/changes/intelligent-lead-research/tasks.md#L27) | Частично | Scoped entity IDs/composite keys и isolated memory реализованы. | Нет scoped bridge старого CompanyID в новые entity; legacy leads/cards/email остаются другим хранилищем/контрактом. | E03 |
| [3.2](../../openspec/changes/intelligent-lead-research/tasks.md#L28) | Частично | RU INN checksum, GB prefix/leading zeros, FR SIREN/SIRET и unknown формат реализованы. | RU KPP и DE register/court предметно не реализованы; нет полного набора обязательных jurisdiction cases. | E03 |
| [3.3](../../openspec/changes/intelligent-lead-research/tasks.md#L29) | Частично | Сохраняются source revision/hash/text bytes/subject locator; wrong-subject и footer проверяются. | Реализован Markdown/text capture, но нет полного artifact-ready/ref lifecycle и всех crash/upload/subject-role acceptance на независимом corpus. | E03,E02 |
| [3.4](../../openspec/changes/intelligent-lead-research/tasks.md#L30) | Частично | Knowledge/freshness/access/validity/history/source-family поля существуют; revoke влияет на чтение. | Нет полной политики независимости source families, conflict adjudication и versioned support graph с измеренной приёмкой. | E03 |
| [3.5](../../openspec/changes/intelligent-lead-research/tasks.md#L31) | Частично | Manual link/reject/revoke, CAS, cluster contradiction veto, candidates и reversible projections реализованы. | Нет labelled false-merge acceptance/sample uncertainty; candidate fanout и адресный пересчёт ограниченных групп не измерены; scope-wide rebuild. | E03,E04 |
| [3.6](../../openspec/changes/intelligent-lead-research/tasks.md#L32) | Частично | Email format и консервативный international +phone, evidence/identity не смешаны с reachability. | Phone принимает только + и 8–15 digits; нет country-aware национальной нормализации/metadata. Все independent channel/action dimensions не реализованы. | E03,E02 |
| [3.7](../../openspec/changes/intelligent-lead-research/tasks.md#L33) | Частично | Есть реальные PG contract suites и dossier/identity UI. | Живой прогон не нашёл контактов; нет принятого RU/GB real seed с объяснимо связанным контактом и полного country acceptance. | E03,E04,E08,E10 |
| [3.8](../../openspec/changes/intelligent-lead-research/tasks.md#L35) | Частично | Dossier показывает knowledge/freshness/access/support, method/version/date/quote и candidates. | Нет полного UI независимых identity/link/format/reachability/action-eligibility состояний и ручной field confirmation с audit во всех случаях. | E08,E03 |
### Раздел 4

| Task | Статус | Подтверждено | Недостающее для целого task | Evidence |
|---|---|---|---|---|
| [4.1](../../openspec/changes/intelligent-lead-research/tasks.md#L39) | Частично | Mission начинается из human intent без company domain, сохраняет plural refs; старый/новый execution mode взаимоисключены. | Нет полного approved legacy CompanyID bridge/последующего company-agent mapping и повторной совместной legacy smoke acceptance; зависимости1.1/3.1 неполны. | E04,E05,E10 |
| [4.2](../../openspec/changes/intelligent-lead-research/tasks.md#L40) | Частично | Bounded structured portfolio и runtime schema/scope/source validation есть. | Planner подключён прямым constructor, не registered research-planner block; полный набор structured tools/authority и injection corpus не принят. | E02,E04 |
| [4.3](../../openspec/changes/intelligent-lead-research/tasks.md#L41) | Частично | PG intent/lease/CAS, receipt→owner ingest→membership и crash regressions реализованы; сеть вне Tx. | Нет полной intent/outbox/activity saga и публичного ReserveForAction/Settle handshake из blueprint; текущий settlement через durable obligation и Lookup проверен PG/-race, но не заменяет всю saga. | E04,E06 |
| [4.4](../../openspec/changes/intelligent-lead-research/tasks.md#L42) | Частично | Firecrawl account/grant/reserve/unknown и provider_admitted; proxy имеет только technical caps. | Нет full Search/Crawl/Extract/refine/MarketStudy acceptance и operational resume/top-up/reconciliation admin; внешние потребители того же FC ключа вне общего ledger. | E06,E07 |
| [4.5](../../openspec/changes/intelligent-lead-research/tasks.md#L43) | Частично | SaveAttempt/RecallAttempts с TTL/scope существуют; mission actions устойчивы. | Mission/planner не вызывает reusable SaveAttempt/RecallAttempts: нет фактического предотвращения повторной semantic negative попытки между run. | E03,E04 |
| [4.6](../../openspec/changes/intelligent-lead-research/tasks.md#L44) | Частично | Scoped PostgreSQL exact/FTS retrieval и revoked фильтры есть. | Нет typed-graph retrieval, измеренного semantic расширения/pgvector index и включённого retrieval-before-prompt пути для всех summaries. | E03 |
| [4.7](../../openspec/changes/intelligent-lead-research/tasks.md#L45) | Частично | Execution state/phase/reason, pause/resume/cancel/deadline и request-owned reconciliation реализованы. | Не пройдена полная source-backoff/alternative timer и изменённого human-scope linked generation acceptance; позднее ручное ledger reconciliation не доставляется terminal projection автоматически. | E04,E05 |
| [4.8](../../openspec/changes/intelligent-lead-research/tasks.md#L46) | Частично | Plan/prompt/model/schema versions и PG restart/reuse receipts сохраняются. | Нет зарегистрированного Temporal research workflow, GetVersion/replay histories/ContinueAsNew или принятой эквивалентной pinned worker strategy из blueprint. | E02,E04 |
| [4.9](../../openspec/changes/intelligent-lead-research/tasks.md#L48) | Частично | Receipt-backed members, canonical regroup/revoke, immutable own assessment sets и exact evidence recheck реализованы. | Нет owner-event watermarks, deterministic country quotas и полного concurrent final-slot/last-mile invalidation corpus; live UI после latest fixes ещё нужен. | E04,E09 |
| [4.10](../../openspec/changes/intelligent-lead-research/tasks.md#L49) | Частично | Frozen terminal members, drain deadline/late updates и durable settlement obligation проверены PG/-race; failed owner finish не теряет компанию/резерв и не повторяет POST после restart. | Нет автоматической terminal projection после позднего ручного ledger.Reconcile, полного owner event job/Temporal drain replay и bounded-drain/late-artifact UI acceptance для всех исходов. | E04,E06 |
| [4.11](../../openspec/changes/intelligent-lead-research/tasks.md#L50) | Частично | Bounded discovery/enrichment очередь, source-link followups и FC→proxy fallback есть. | Нет measured-yield ranking, weighted-deficit fairness, semantic attempt normalization и no-progress parking policy/TTL на representative corpus. | E04 |
| [4.12](../../openspec/changes/intelligent-lead-research/tasks.md#L51) | Частично | Revision snapshots/polling, stale/out-of-order handling, saved/accepted/contact/email filters и waiting diagnostics есть. | Не все blueprint счётчики (documents/hits/identity/criteria/owned-contact/credited) раскрываются отдельными owner resultsets; полный reconnect/live acceptance после изменений не завершён. | E08,E04 |
| [4.13](../../openspec/changes/intelligent-lead-research/tasks.md#L53) | Частично | Incomplete entities и persistent OpenNeed с independent knowledge/execution состояния переживают restart. | Нет полной criteria/constraints-keyed need semantics, автоматического присоединения/отсоединения mission execution и plan-from-need; SetNeedExecution не подключён к worker. | E03,E08 |
| [4.14](../../openspec/changes/intelligent-lead-research/tasks.md#L54) | Частично | Есть identity changefeed, revoke-aware projection и exact criteria support recheck активной миссии. | Нет общего versioned reverse dependency index, AND/OR proof sets/inbox/outbox addressable jobs, expiry fanout, retry/DLQ/lag observability. | E03,E04,E09 |
| [4.15](../../openspec/changes/intelligent-lead-research/tasks.md#L55) | Частично | PG tests покрывают late contact, scoped IDs, revoke/alternative identity support и terminal freeze частями. | Нет единого T0–T4 live DB+worker+UI сценария с историей отправки, deletion race и всеми source-scope cases. | E03,E04,E09 |
| [4.16](../../openspec/changes/intelligent-lead-research/tasks.md#L57) | Частично | Offer/ICP разделены, immutable criteria/version/source-backed query families и grounded analysis реализованы. | Полные audience-owned country/industry profiles, источники всех inferred critical fields и международный comparative acceptance ещё нужны. | E02,E09 |
| [4.17](../../openspec/changes/intelligent-lead-research/tasks.md#L58) | Частично | Discovery portfolio без seed, incomplete candidates, наблюдаемые contact/company followups и canonical credit есть. | Нет полноценной двойной frontier по persistent OpenNeed, measured-yield/parking/semantic memory и cross-source registry query chaining. | E04,E02 |
| [4.18](../../openspec/changes/intelligent-lead-research/tasks.md#L59) | Нет новой реализации | Есть только заданные технические caps и план эксперимента. | Нет fixed-vs-adaptive holdout trial, quality/unique outcomes/uncertainty и основания принятия policy/caps. | E15 |
### Раздел 5

| Task | Статус | Подтверждено | Недостающее для целого task | Evidence |
|---|---|---|---|---|
| [5.1](../../openspec/changes/intelligent-lead-research/tasks.md#L63) | Частично | 249 ISO codes, часть локализованных labels, UK alias, RU/ru defaults новых заявок и multiselect сохраняются. | Нет pinned ISO/CLDR provenance и полного EU/GCC frozen group revision/legacy_unknown backfill/availability profile acceptance. | E01,E02,E08 |
| [5.2](../../openspec/changes/intelligent-lead-research/tasks.md#L64) | Нет новой реализации | Ledger допускает тип study intent, но это инфраструктурная возможность. | Нет audience MarketStudy aggregate/UI button/service/results или живого country study; тип grant не реализация исследования рынка. | E15 |
| [5.3](../../openspec/changes/intelligent-lead-research/tasks.md#L65) | Частично | Семь language recipes, явные рынки/языки и original ICP fallback. | Нет полного CountryProfile/local synonym evidence и раздельных registration/operating/served markets, adversarial developer cases. | E02 |
| [5.4](../../openspec/changes/intelligent-lead-research/tasks.md#L66) | Частично | Есть выбранные markets и общий distinct result goal. | Нет persisted quota/min/max allocation/support-level policy и доказанного запрета закрывать DE deficit RU результатом. | E01,E04 |
| [5.5](../../openspec/changes/intelligent-lead-research/tasks.md#L67) | Частично | Реальный RU discovery выполнен через Firecrawl/DeepSeek. | Нет per-country RU/GB registry/contact acceptance, воспроизводимого manifest качества/coverage/стоимости. | E10 |
### Раздел 6

| Task | Статус | Подтверждено | Недостающее для целого task | Evidence |
|---|---|---|---|---|
| [6.1](../../openspec/changes/intelligent-lead-research/tasks.md#L71) | Частично | SearchReceipt/capability/geography requested/applied/observed, durable replay/errors присутствуют. | Нет полного regional API paging/cache/circuit/country engine support; реальная региональная функция должна пройти6.2/6.3. | E06,E07 |
| [6.2](../../openspec/changes/intelligent-lead-research/tasks.md#L72) | Частично | Firecrawl external search реально работает и не подтверждает identity сам по себе. | Отдельный accepted regional API с RU/GB capability/тарифным trial не подключён; Firecrawl v1 не обещает применённую региональность. | E06,E10 |
| [6.3](../../openspec/changes/intelligent-lead-research/tasks.md#L73) | Частично | Собственный pinned multiarch SearXNG container, JSON endpoint/health и real adapter. | Оба live engine blocked CONNECT403: достаточная реальная поисковая функция не принята; public instance не использовался. | E07 |
| [6.4](../../openspec/changes/intelligent-lead-research/tasks.md#L74) | Частично | Scoped exact/FTS по накопленным assertions существует, revoked refs не выдаются как доступные. | Нет vector ranking/измеренного recall baseline, полноценного corpus search UI/snippets и source-wide deletion/load acceptance. | E03 |
| [6.5](../../openspec/changes/intelligent-lead-research/tasks.md#L75) | Нет новой реализации | Устойчивые mission действия не равны вертикальному индексу. | Нет самостоятельного source/recrawl manifest, freshness scheduling/index projections и corpus index UI. | E15 |
| [6.6](../../openspec/changes/intelligent-lead-research/tasks.md#L76) | Нет новой реализации | PostgreSQL используется как хранилище. | Нет preregistered SLO/load report и ADR keepPG vs OpenSearch на измеренном corpus; отсутствие OpenSearch само по себе не ошибка. | E15 |
### Раздел 7

| Task | Статус | Подтверждено | Недостающее для целого task | Evidence |
|---|---|---|---|---|
| [7.1](../../openspec/changes/intelligent-lead-research/tasks.md#L80) | Нет новой реализации | Исследовательские рекомендации Crawlee/Crawl4AI сохранены. | Нет fair pinned-version benchmark одинакового corpus/лимитов с correct contact links/locators. | E15 |
| [7.2](../../openspec/changes/intelligent-lead-research/tasks.md#L81) | Нет новой реализации | Есть только hosted Firecrawl и search-only SearXNG adapters. | Нет собственного HTTP/browser acquisition service/артефактов/интеграции; они не подменяются thin hosted wrapper. | E06,E15 |
| [7.3](../../openspec/changes/intelligent-lead-research/tasks.md#L82) | Частично | Owner-owned operation/attempt/lease ledger для FC и SearXNG реализован. | Нет собственного Python bounded runtime/frontier dispatch/discovered-links протокола и доказательства единственного scheduler в нём. | E06,E07 |
| [7.4](../../openspec/changes/intelligent-lead-research/tasks.md#L83) | Частично | FC account concurrency/interval и SearXNG source slots/attempt/byte/time limits есть. | Нет host-wide redirects/subrequests/session rotations/browser resource cancellation caps собственного acquisition; vendor account cap не host rate limiter. | E06,E07 |
| [7.5](../../openspec/changes/intelligent-lead-research/tasks.md#L84) | Частично | Requested/applied/claimed/observed поля раздельны; unsupported required geography отклоняется. | Нет живых geo/session/sticky profiles с подтверждённым egress, wrong-country fault matrix и accepted markets. | E06 |
| [7.6](../../openspec/changes/intelligent-lead-research/tasks.md#L85) | Частично | Initial URL/private IP precheck и явное ограничение hosted crawler trace. | Нет network enforcement фактических redirects/subresources/service workers, private canary tests и tenant cookie/session isolation. | E06,E07 |
| [7.7](../../openspec/changes/intelligent-lead-research/tasks.md#L86) | Только проектирование | Есть SearXNG deployment/readme/pin и research opt-in runbook. | Нет licensing/resource/versioned rollout/rollback собственного acquisition cohort и real compatibility acceptance. | E07,E15 |
### Раздел 8

| Task | Статус | Подтверждено | Недостающее для целого task | Evidence |
|---|---|---|---|---|
| [8.1](../../openspec/changes/intelligent-lead-research/tasks.md#L90) | Нет новой реализации | Markdown contact-link followups не являются browser recipe. | Нет Playwright recipe execution/постусловий/trace/DOM locators для JS/reveal/pagination/public search. | E15 |
| [8.2](../../openspec/changes/intelligent-lead-research/tasks.md#L91) | Только проектирование | Activation policy описана как зависимость. | Нет принятой MODIFIED-дельты действующего browser запрета и активируемой версии автономного режима. | E15 |
| [8.3](../../openspec/changes/intelligent-lead-research/tasks.md#L92) | Нет новой реализации | Есть сравнение подходов в концепции. | Нет Browser Use vs Playwright trial с caps/side-effect checks и grounded incremental value. | E15 |
| [8.4](../../openspec/changes/intelligent-lead-research/tasks.md#L93) | Частично | Text structured LLM имеет token/time/size/schema bounds. | Нет принятых text/tool/vision capability profiles и реальных vision/tool fixtures; screenshot путь не реализован. | E02 |
| [8.5](../../openspec/changes/intelligent-lead-research/tasks.md#L94) | Частично | Grounded extraction из Markdown/text с subject quotes и no-contact candidate. | Нет DOM/JSON-LD/structured blocks/text-PDF layout pipeline и multi-column ownership acceptance. | E02,E03 |
| [8.6](../../openspec/changes/intelligent-lead-research/tasks.md#L95) | Нет новой реализации | Docling/PaddleOCR/Tesseract исследованы концептуально. | Нет corpus trial/pinned weights/licenses/resource report и принятой комбинации backend. | E15 |
| [8.7](../../openspec/changes/intelligent-lead-research/tasks.md#L96) | Нет новой реализации | Текстовый locator не объявляется OCR. | Нет document/OCR adapter page/bbox/crop transform/raw-model receipt и bounded malformed-file integration. | E15 |
| [8.8](../../openspec/changes/intelligent-lead-research/tasks.md#L97) | Нет новой реализации | Текстовые форматы отбрасывают часть мусора. | Нет OCR ambiguous-symbol alternatives/subject review и независимого OCR-confidence/ownership состояния. | E15 |
| [8.9](../../openspec/changes/intelligent-lead-research/tasks.md#L98) | Нет новой реализации | Текстовый dossier показывает quote/span. | Нет image/page/crop overlay, OCR regression corpus и upgrade acceptance/runbook. | E08,E15 |
### Раздел 9

| Task | Статус | Подтверждено | Недостающее для целого task | Evidence |
|---|---|---|---|---|
| [9.1](../../openspec/changes/intelligent-lead-research/tasks.md#L102) | Нет новой реализации | Legacy material contracts существуют отдельно. | Нет versioned research-handoff из нового dossier с immutable recipient/evidence/language snapshot. | E15,E10 |
| [9.2](../../openspec/changes/intelligent-lead-research/tasks.md#L103) | Нет новой реализации | Legacy генерация писем не перенесена на research. | Нет claim/span→personalization lineage и material version, потребляющей новую evidence memory. | E15,E10 |
| [9.3](../../openspec/changes/intelligent-lead-research/tasks.md#L104) | Нет новой реализации | Existing exact content approval не доказательство нового пути. | Нет new research recipient snapshot/epoch/fingerprint bridge; текущий legacy first-email resolver остаётся вне нового acceptance. | E15 |
| [9.4](../../openspec/changes/intelligent-lead-research/tasks.md#L105) | Нет новой реализации | Некоторые delivery safeguards есть в legacy. | Новый research outreach не выполняет freshness/suppression/pending-reply recheck и unknown-send reconciliation. | E15 |
| [9.5](../../openspec/changes/intelligent-lead-research/tasks.md#L106) | Нет новой реализации | Есть только концепция чужих форм. | Нет sender profile/prepared form task/needs-data/review UI с реальными реквизитами. | E15 |
| [9.6](../../openspec/changes/intelligent-lead-research/tasks.md#L107) | Нет новой реализации | Research UI показывает candidates, а legacy drafts — другой путь. | Нет нового research material/recipient/channel timeline и real permitted delivery evidence. | E10,E15 |
| [9.7](../../openspec/changes/intelligent-lead-research/tasks.md#L108) | Только проектирование | Сохранено, что прежний manual-only план не является окончательным выбором пользователя. | Нет принятого продуктового режима foreign form effect и согласованного executable acceptance. | E15 |
### Раздел 10

| Task | Статус | Подтверждено | Недостающее для целого task | Evidence |
|---|---|---|---|---|
| [10.1](../../openspec/changes/intelligent-lead-research/tasks.md#L112) | Только проектирование | Новые требования inbound и distinction relay/outbound описаны. | Прежнее исключение mail sync не снято согласованной действующей дельтой/каталогом/реализацией. | E15 |
| [10.2](../../openspec/changes/intelligent-lead-research/tasks.md#L113) | Нет новой реализации | Capability owner объявлен как план. | Нет mailbox/form bindings и inquiry/conversation aggregates/contracts/migrations новой модели. | E15 |
| [10.3](../../openspec/changes/intelligent-lead-research/tasks.md#L114) | Нет новой реализации | Existing client form relay — предварительная инфраструктура. | Нет durable red Inquiry/attribution/policy/idempotent lead effect нового пути и real sink acceptance. | E15 |
| [10.4](../../openspec/changes/intelligent-lead-research/tasks.md#L115) | Нет новой реализации | Подключённого mailbox в этом change нет. | Нужен реальный API/webhook/IMAP transport ingest durable-before-parse и UI письма. | E15 |
| [10.5](../../openspec/changes/intelligent-lead-research/tasks.md#L116) | Нет новой реализации | Нет mailbox transport state. | Не реализованы cursor/ack/backfill/UIDVALIDITY/dedup с missing Message-ID fault matrix. | E15 |
| [10.6](../../openspec/changes/intelligent-lead-research/tasks.md#L117) | Нет новой реализации | Новый research не хранит MIME. | Нет MIME/charset/attachments red parser/lineage/sanitized viewer/limits/reparse. | E15 |
| [10.7](../../openspec/changes/intelligent-lead-research/tasks.md#L118) | Нет новой реализации | Новая receiving/correlation модель отсутствует. | Нет проверенного Reply-To/alias/token и exact wire Message-ID/In-Reply-To/References persistence. | E15 |
| [10.8](../../openspec/changes/intelligent-lead-research/tasks.md#L119) | Нет новой реализации | Identity decision компаний не является email correlation. | Нет scoped headers/thread correlation/ambiguity review/audited correction. | E15 |
| [10.9](../../openspec/changes/intelligent-lead-research/tasks.md#L120) | Нет новой реализации | Обычный SaveObservation не принимает mailbox inquiry. | Нет inbound inquiry disposition/lead update и distinct spam/OOO/DSN outcome. | E15 |
| [10.10](../../openspec/changes/intelligent-lead-research/tasks.md#L121) | Нет новой реализации | Research subject extractor не классифицирует переписку. | Нет new-body quoted intent/type model, operator correction и safe stop/OOO semantics. | E15 |
| [10.11](../../openspec/changes/intelligent-lead-research/tasks.md#L122) | Нет новой реализации | Новый outreach/conversation bridge отсутствует. | Нет qualified reply/отказ → pause/stop/suppression с idempotent scoped effects. | E15 |
| [10.12](../../openspec/changes/intelligent-lead-research/tasks.md#L123) | Нет новой реализации | Dossier UI не является inbox. | Нет общего inbox/conversation timeline, ответственного и auditable correlation correction. | E15 |
| [10.13](../../openspec/changes/intelligent-lead-research/tasks.md#L124) | Нет новой реализации | Research startup runbook касается search. | Нет connected mailbox/form-sink runbook/replay/retention/rotation и real acceptance. | E15 |
| [10.14](../../openspec/changes/intelligent-lead-research/tasks.md#L126) | Нет новой реализации | Conversation-signal остаётся планом. | Нет owner contract/relationship и pending-reply hold с timeout/correlation conflict/outcome transitions. | E15 |
### Раздел 11

| Task | Статус | Подтверждено | Недостающее для целого task | Evidence |
|---|---|---|---|---|
| [11.1](../../openspec/changes/intelligent-lead-research/tasks.md#L130) | Частично | Real company-unit run с двумя зачётами, receipts/credits; request owner ставит terminal search outcome. | Нет полного quota/contact/material объёма и итогового повторного live UI после duplicate/criteria/settlement fixes; source shortfall измерен лишь узким срезом. | E04,E05,E10 |
| [11.2](../../openspec/changes/intelligent-lead-research/tasks.md#L131) | Частично | Состояния/diagnostics/counters/reasons доступны в query/UI. | Нет полного Prometheus metrics/alerts для stuck/no-progress/source health/dependency lag/inbound lag с privacy acceptance. | E04,E06,E08 |
| [11.3](../../openspec/changes/intelligent-lead-research/tasks.md#L132) | Частично | Source revoke убирает доступное содержимое и снимает текущие основания; criteria recheck fail-closed. | Нет полного purpose/retention deletion pipeline для artifacts/crops/vector/summary/conversation/cached prompt projections. | E03,E09 |
| [11.4](../../openspec/changes/intelligent-lead-research/tasks.md#L133) | Частично | Identity corrections и criteria ownership изолированы; терминальные snapshots не переписываются. | Нет интеграции bounce/address/company/campaign feedback и проверки отсутствия повторной доставки в новом пути. | E03,E04 |
| [11.5](../../openspec/changes/intelligent-lead-research/tasks.md#L134) | Частично | Opt-in mode и legacy FC gate ограничивают новый маршрут. | Нет измеренного source/country/cohort cutover и rollback rehearsal с сохранением incoming/unknown effects. | E01,E06 |
### Раздел 12

| Task | Статус | Подтверждено | Недостающее для целого task | Evidence |
|---|---|---|---|---|
| [12.1](../../openspec/changes/intelligent-lead-research/tasks.md#L138) | Частично | Проверен request→research→dossier фрагмент с настоящими провайдерами/PG. | Нет material→exact approval→delivery→real reply→conversation/update полной цепочки. | E10,E01 |
| [12.2](../../openspec/changes/intelligent-lead-research/tasks.md#L139) | Частично | Есть negative unit/PG tests footer/mixed identity/scope/injection/provider failures. | Нет OCR/SMTP/full cross-tenant corpus и независимой приёмки всех отрицательных strata с опубликованными пределами. | E03,E04,E06,E08 |
| [12.3](../../openspec/changes/intelligent-lead-research/tasks.md#L140) | Частично | Targeted PG/-race и active strict PASS; root broad build/test/vet/importlint/archlint PASS после exact catalog fixture fix. | Root global OpenSpec --all --strict FAIL на30 existing specs/changes (>500-char warnings); сравнение HEAD baseline выполняется. Также отсутствуют полные corpus/measurements/required integrations; task остаётся открытым. | E00,E10,E11 |
| [12.4](../../openspec/changes/intelligent-lead-research/tasks.md#L142) | Частично | Реальный request/dossier UI и tenant403 проверялись; HTTP/JS role/CSRF/stale tests есть. | Полный UI request→lead→email→conversation и все console roles/transitions не принят; после latest fixes нужен повтор human-browser path. | E08,E10 |
| [12.5](../../openspec/changes/intelligent-lead-research/tasks.md#L143) | Частично | Есть сохраняемая программа REQ/USE, существенный search-first implementation slice. | MarketStudy/свойcrawler/index/browser/OCR/forms/inbound и остальные обязательные acceptance отсутствуют; полный change не готов к archive. | E15,E10 |

## Приоритет следующей реализации

1. Повторить живой request→dossier UI после duplicate/criteria/settlement исправлений; показать truthful partial там, где контакт не найден. Отдельно закрыть terminal budget projection после позднего ручного ledger.Reconcile; уже выполненные PG/-race settlement tests не заменяют этот сценарий.
2. Подключить reusable negative memory/OpenNeed к planner, scoped legacy bridge и ограниченные cross-source registry/contact действия с сохранением доказательств.
3. Создать corpus/holdout/evaluator до заявлений о качестве и выбора crawler/browser/OCR; принимать международные источники по странам.
4. Завершить authoritative dependency jobs и ограниченный identity affected-group пересчёт; затем contact-ready → immutable research handoff → exact approval/outreach.
5. Продолжить все обязательные MarketStudy, own search/index/acquisition, browser/OCR, формы/inbound задачи по сохранённым зависимостям. Они не заменяются успешным Firecrawl demo.

## Ограничение этого аудита

Это read-only task/blueprint coverage review текущего рабочего дерева, не archive verification полной программы. Все94 IDs сопоставлены, но не выполнен новый независимый corpus/per-country/channel acceptance и не повторялись paid провайдеры. Отсутствующие продуктовые контуры определены по фактическим новым contracts/wiring/migrations и поиску реализации; legacy фундамент отмечен отдельно. Новые изменения параллельных агентов после времени среза нужно сверить перед финальной отметкой задач.
