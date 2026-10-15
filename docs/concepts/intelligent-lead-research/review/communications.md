# Аудит communications: контакты как результат поиска, письма как потребитель, inbound как поздний источник

**Baseline:** `bbe49f6`. Исследование и план, без runtime изменений. Главный приоритет — discovery/crawl/identity/evidence/OpenNeed; ни mailbox,ни outbound не блокируют первый качественный поисковый срез.

Уточнение пользователя о бюджете:прикладной общий бюджет ограничивает только Firecrawl. Остальные внешние вызовы используют уже существующий proxy;финансовые gates и резервы для LLM/mail/MIME/OCR не вводятся. Технические лимиты размера/частоты/времени и durable reservation исходящего действия сохраняются:reservation означает защиту от повторного эффекта,не денежный резерв.

## Основной вывод

Контакт полезен,когда известны субъект,тип канала,основания принадлежности,актуальность и ограничения применения. Email в тексте страницы или письме остаётся наблюдением до проверки. Компания без email остаётся в долговечной памяти. Поздний контакт из иной страницы,формы или письма проходит тот же assertion/dependency pipeline и дополняет прежнюю компанию. Для генерации требуется evidence snapshot;для отправки — pinned получатель/материал и текущая допустимость.

Рамка existing exact-version approval частично реализована:version черновика есть,но recipient resolver может разрешить CompanyID в первый текущий email. Это главный разрыв данной темы. Исходящая почта уже умеет durable unknown,но recovery и ранний reply hold ещё нужно проектировать.

## Дефекты и уточнения change

### COMM-F01 · critical
**Проверяемое место:** `docs/concepts/intelligent-lead-research/06-outreach-and-feedback.md:83`.

Версия черновика действительно фиксируется, но delivery-target v1 возвращает текущий Address без recipient version; fallback CompanyID выбирает первый контакт. Обещание точного адресата нельзя считать уже обеспеченным existing approval.

**Решение:** В новом пути immutable RecipientSnapshotRef фиксирует endpoint/link version и policy scope; resolver v2 разрешает именно снимок, повторно проверяет текущие ограничения, но никогда не заменяет endpoint. Legacy fallback не используется после нового approval.
**Основания:** `backend/contexts/lead-management/internal/app/resolve_recipient.go:82`, `contracts/lead-management/deliverytarget/deliverytarget.go:50`, `backend/contexts/outreach-orchestration/internal/domain/action_draft.go:229`. **Задачи:** 9.1, 9.3.

### COMM-F02 · major
**Проверяемое место:** `docs/concepts/intelligent-lead-research/10-inbound-forms-and-conversations.md:29`.

SMTP envelope не имеет Reply-To/In-Reply-To/References; compose генерирует Message-ID через lossy safeMessageID и общий @lead-gen. Будущая корреляция не обеспечивается одним alias в документации.

**Решение:** Расширить versioned mail envelope, сохранять точный wire Message-ID до отправки, namespace реальным контролируемым доменом и UUID, валидировать заголовки. Проверить реальный reply roundtrip.
**Основания:** `backend/contexts/integration-platform/internal/adapters/smtp/smtp.go:196`, `contracts/integration-platform/mail/mail.go:30`, `COMM-S06`, `COMM-S12`. **Задачи:** 9.3, 10.7, 10.8.

### COMM-F03 · major
**Проверяемое место:** `openspec/changes/intelligent-lead-research/specs/research-to-outreach/spec.md:53`.

Есть suppression и campaign-cancel fence в reserve, но после commit перед mailer.Send нет проверки версии eligibility, pending reply и evidence freshness. Async conversation event оставляет также окно задержки между intake и его применением.

**Решение:** Ввести versioned DeliveryEligibilityCheck непосредственно перед provider invocation с owner read/epoch; hold durable в владельце conversation, сигнал обновляет outreach. Указать наблюдаемую границу: check не является распределённой атомарной отменой уже принятого SMTP.
**Основания:** `backend/contexts/message-delivery/internal/app/command/deliver_message.go:99`. **Задачи:** 9.4, 10.14.

### COMM-F04 · major
**Проверяемое место:** `openspec/changes/intelligent-lead-research/tasks.md#9.4`.

Unknown уже устойчиво сохраняется провайдерским adapter и исключён из автоматического retry. Нет обнаруженного публичного пути reconciliation; generic error/cancel после reserve может оставить sending. Нужно доработать recovery, а не заново изобретать unknown.

**Решение:** Добавить журнал reconciliation/операторское решение и наблюдаемую классификацию stale sending; provider evidence ведёт к accepted/confirmed-not-accepted/unknown. Отсутствие Sent item само по себе не доказывает неотправку.
**Основания:** `backend/contexts/message-delivery/internal/adapters/mailer/provider/provider.go:80`, `backend/contexts/message-delivery/internal/domain/message.go:194`, `backend/contexts/message-delivery/internal/app/command/deliver_message.go:186`. **Задачи:** 9.4, 9.6.

### COMM-F05 · major
**Проверяемое место:** `docs/concepts/intelligent-lead-research/10-inbound-forms-and-conversations.md:51`.

Form relay существует, но v1 Submission не несёт submission ID и forwarder принимает любой 2xx без durable receipt. Retry после потерянного HTTP ответа сейчас нельзя связать с одним inquiry.

**Решение:** Новый intake контракт с binding-scoped submission_id, payload digest и durable receipt. Тот же ID/тот же payload возвращает тот же receipt; тот же ID/другой payload —409. Legacy sink остаётся явно без новой гарантии до миграции.
**Основания:** `contracts/outreach-orchestration/formsubmission/formsubmission.go:52`, `frontend/public/internal/adapters/forwarder/forwarder.go:129`. **Задачи:** 10.1, 10.3.

### COMM-F06 · major
**Проверяемое место:** `docs/concepts/intelligent-lead-research/10-inbound-forms-and-conversations.md:75`.

Последовательность parse→dedup позволяет тратить parser/OCR ресурсы на транспортные повторы и путает atomic receipt с бизнес-дедупликацией. go-message ограничивает header, но общий decoded size/part count/nesting/HTML sanitization нужно обеспечивать отдельно.

**Решение:** Разделить durable transport dedup до parse и semantic review после parse. Receipt/raw ref/cursor commit предшествуют ack; poison message уходит в quarantine с видимым состоянием, не блокирует весь mailbox навечно.
**Основания:** `contracts/outreach-orchestration/formsubmission/formsubmission.go:52`, `COMM-S04`, `COMM-S05`, `COMM-S11`. **Задачи:** 10.5, 10.6.

### COMM-F07 · major
**Проверяемое место:** `docs/concepts/intelligent-lead-research/10-inbound-forms-and-conversations.md:79`.

provider account+message ID недостаточно без объявления области и стабильности конкретного provider ID: у обычных Microsoft Graph IDs move меняет ID. Gmail historyId не непрерывный счётчик и может истечь за часы.

**Решение:** Adapter manifest задаёт identity_kind, account/folder scope, movement semantics. Graph использует ImmutableId во всех запросах/subscription и per-folder delta; Gmail history404 вызывает scoped full resync; dedup не строится по Message-ID.
**Основания:** `COMM-S06`, `COMM-S07`, `COMM-S14`. **Задачи:** 10.4, 10.5.

### COMM-F08 · major
**Проверяемое место:** `docs/concepts/intelligent-lead-research/10-inbound-forms-and-conversations.md:145`.

Новая цель red-only для подробной переписки не соответствует всей существующей delivery storage: green message snapshot включает Address, Subject, Body. Добавление reply bodies в этот snapshot расширит расхождение.

**Решение:** До подключения содержимого входящей переписки к outbound выбрать red delivery payload store/refs и миграцию с проверкой topology; green operational record хранит refs/status. Не выдавать существующий layout за уже подтверждённую red isolation.
**Основания:** `backend/contexts/message-delivery/internal/adapters/persistence/postgres/message_repository.go:228`, `backend/contexts/message-delivery/migrations/green/0001_message.sql:18`, `architecture/domain-catalog.yaml:153`. **Задачи:** 10.1, 10.2, 9.3.

### COMM-F09 · major
**Проверяемое место:** `docs/concepts/intelligent-lead-research/06-outreach-and-feedback.md:22`.

Existing handoff идентифицирует draft по RecipientRef; новый material для того же recipient в одной request campaign даёт конфликт. Долговечное исследование допускает несколько версий dossier и позднее обогащение, поэтому нельзя повторно публиковать изменённый пакет под прежней идентичностью.

**Решение:** ResearchHandoffID/version фиксирует snapshot; idempotency по tenant/handoff/version; DraftID не равен recipient ref для нового пути. Старый draft не заменяется молча, revision создаётся явной командой и новым approval.
**Основания:** `backend/contexts/outreach-orchestration/internal/app/command/submit_handoff.go:156`. **Задачи:** 9.1, 9.3.

### COMM-F10 · minor
**Проверяемое место:** `openspec/changes/intelligent-lead-research/design.md:180`.

Фраза «ручной form acceptance» в плане миграции вновь предрешает режим, хотя основная спецификация оставляет его открытым. В выбранных требованиях нет пользовательского manual-only.

**Решение:** Заменить на acceptance выбранного режима. Подготовка реальных полей обязательна при обоих; путь отправки и его разрешение фиксируются задачей9.7.
**Основания:** `docs/concepts/intelligent-lead-research/10-inbound-forms-and-conversations.md:21`. **Задачи:** 9.5, 9.7.

### COMM-F11 · major
**Проверяемое место:** `docs/concepts/intelligent-lead-research/10-inbound-forms-and-conversations.md:97`.

Поздний контакт/ИНН из письма должен попадать в тот же evidence/dependency pipeline, что и веб-источник; отдельное прямое обновление contact/company обойдёт subject verification, OpenNeed и пересмотр зависимых выводов.

**Решение:** Ingest создаёт message observation/claim с locator и source_kind; shared assertion evaluator проверяет связь и может закрыть OpenNeed. Принятый reply не переписывает immutable completion snapshot и не создаёт автоматически исследовательскую заявку и не увеличивает установленный лимит Firecrawl.
**Основания:** `backend/contexts/lead-management/internal/app/resolve_recipient.go:82`, `openspec/changes/intelligent-lead-research/specs/inbound-lead-conversations/spec.md:85`. **Задачи:** 10.9, 10.10, 10.11.

## Реальные факты и пределы вывода

- **COMM-C01 [observed-code]** Draft revision повышает d.version и переводит approved draft обратно в pending-approval. Основания: `backend/contexts/outreach-orchestration/internal/domain/action_draft.go:229`. Предел: Не фиксирует сам по себе версию mutable endpoint.
- **COMM-C02 [observed-code]** Approved action identity включает campaign/draft/version. Основания: `backend/contexts/outreach-orchestration/internal/ports/contracts/approved_action.go:84`. Предел: Не доказывает, что endpoint/material evidence ещё актуальны.
- **COMM-C03 [observed-code]** Delivery resolver tenant-scoped; отклоняет неизвестные и чужие refs, но ищет текущий email и допускает CompanyID fallback. Основания: `backend/contexts/lead-management/internal/app/resolve_recipient.go:82`. Предел: Непустой email не равен подтверждённой принадлежности/достижимости.
- **COMM-C04 [observed-code]** SMTP adapter после DATA write/close error сообщает ErrOutcomeUnknown; provider mapper сохраняет OutcomeUnknown, domain не публикует delivery-event и не разрешает retry. Основания: `backend/contexts/integration-platform/internal/adapters/smtp/smtp.go:138`, `backend/contexts/message-delivery/internal/adapters/mailer/provider/provider.go:80`, `backend/contexts/message-delivery/internal/domain/message.go:194`. Предел: Это код и тесты в репо, не доказательство проведённого live SMTP испытания.
- **COMM-C05 [observed-code]** Текущий SMTP compose имеет From/To/Subject и stable-derived Message-ID, без Reply-To и reply reference headers. Основания: `backend/contexts/integration-platform/internal/adapters/smtp/smtp.go:196`. Предел: Не проводился roundtrip реальной почты.
- **COMM-C06 [observed-code]** Form forwarder маршрутизирует FormRef, не хранит payload; любой2xx трактуется успешной передачей. Основания: `frontend/public/internal/adapters/forwarder/forwarder.go:129`. Предел: Устойчивость red sink этим не проверяется; submission ID отсутствует.
- **COMM-C07 [observed-code]** RecordReaction при уверенном reply переводит execution в suspended, stopping reaction —stopped; неуверенная реакция лишь пишет журнал. Основания: `backend/contexts/outreach-orchestration/internal/domain/campaign_sequences.go:174`. Предел: Pending raw reply hold отсутствует в этой модели; факт приема ещё должен быть связан адаптером.
- **COMM-C08 [observed-code]** Доставка в green snapshot сейчас хранит адрес, тему и body. Основания: `backend/contexts/message-delivery/internal/adapters/persistence/postgres/message_repository.go:228`, `backend/contexts/message-delivery/migrations/green/0001_message.sql:18`. Предел: Проверен код serialization/migration, не фактическая deployment topology и не данные БД.
- **COMM-C09 [observed-code]** Генерация demo читает company/contact и вызывает LLM; результат Subject/Body/Language/RecipientRef не содержит claim-evidence binding. Основания: `backend/contexts/lead-management/internal/app/draft_email.go:87`. Предел: Промпт сам по себе не обеспечивает доказанность каждой персонализации.
- **COMM-C10 [documented-external]** Gmail Message.id документирован immutable; RAW возвращает RFC2822 message. Thread insertion требует threadId, корректные References/In-Reply-To и matching Subject. Основания: `COMM-S06`. Предел: Это документированная Gmail семантика, не универсальное свойство mail providers и не tenant identity.
- **COMM-C11 [documented-external]** Gmail startHistoryId может истечь;404 требует full sync. watch имеет expiration и требует renewal. Основания: `COMM-S06`. Предел: Документация не гарантирует отсутствие пропущенных push; нужен reconciliation/backfill.
- **COMM-C12 [documented-external]** Microsoft Graph message delta работает отдельно по folder, token opaque; ImmutableId нужно включать на каждом запросе, scope сохраняется внутри mailbox. Основания: `COMM-S07`, `COMM-S14`. Предел: Archive mailbox move/reimport могут менять даже immutable ID; примеры не означают подключённый Graph.
- **COMM-C13 [documented-external]** go-message mail.Reader потоково возвращает parts; unknown charset может вернуть пригодный Reader вместе с error; body part нужно дочитать перед NextPart. Основания: `COMM-S04`, `COMM-S05`, `COMM-S11`. Предел: Не обеспечивает HTML sanitization, надежное отделение цитат, антивирус, лимиты всего decoded payload и subject attribution.
- **COMM-C14 [documented-external]** go-message Header.MessageID/MsgIDList разбирают RFC-style identifiers; go-imap exposes UIDNext/UIDValidity и ReadOnly EXAMINE. Основания: `COMM-S10`, `COMM-S12`, `COMM-S13`. Предел: Нужно закрепить supported release; чтение master/v2 не доказывает production стабильность или серверную поддержку всех extensions.

## Что перенять из первичных материалов

| Источник | Заимствуемый механизм | Предел |
|---|---|---|
| [COMM-S04](https://raw.githubusercontent.com/emersion/go-message/master/mail/reader.go) | Потоковый MIME parser;раздельные raw/parsing states и bounded resource wrapper. | Не решает quoting/sanitization/атрибуцию компании. |
| [COMM-S05](https://raw.githubusercontent.com/emersion/go-message/master/entity.go) | Лимит header и UTF8 decoding доступны;остальные limits добавляет наша реализация. | Прочитан upstream master source snapshot,release для реализации ещё не выбран. |
| [COMM-S06](https://raw.githubusercontent.com/googleapis/google-api-go-client/main/gmail/v1/gmail-api.json) | Provider-specific transport identity/threading/checkpoint/recovery. | Не live API acceptance;Gmail свойства нельзя переносить на IMAP/Graph. |
| [COMM-S07](https://raw.githubusercontent.com/microsoftgraph/microsoft-graph-docs-contrib/main/concepts/delta-query-messages.md) | Checkpoint на folder;nextLink полностью сохраняется/используется,не разбирается. | Не универсальный push protocol и не guarantee loss-free безbackfill. |
| [COMM-S10](https://raw.githubusercontent.com/emersion/go-imap/v2/imapclient/select.go) | Read-only mailbox sync не должен случайно удалять сообщения через CLOSE. | v2 branch требует закрепления supported release. |
| [COMM-S11](https://raw.githubusercontent.com/emersion/go-message/master/charset/charset.go) | Для международной почты charset support подключается явно. | Unsupported encoding сохраняется unknown,а не выдуманный текст. |
| [COMM-S12](https://raw.githubusercontent.com/emersion/go-message/master/mail/header.go) | Структурный parser для message identifiers и корректная генерация headers. | Парсинг не удостоверяет sender/tenant/business correlation. |
| [COMM-S13](https://raw.githubusercontent.com/emersion/go-imap/v2/select.go) | Доступные adapter primitives для namespace/cursor. | Нужны серверные acceptance cases reset/reconnect,source не заменяет RFC semantics trial. |
| [COMM-S14](https://raw.githubusercontent.com/microsoftgraph/microsoft-graph-docs-contrib/main/concepts/outlook-immutable-id.md) | Явный provider identity mode и migration обычных IDs. | Mailbox scoped immutable ID не глобальная entity identity. |

Прямое чтение RFC заблокировано сетевой политикой (403);это ограничение исследования,не свойство почтовых протоколов. Полные fetch metadata,bytes/SHA256 и snapshot paths находятся в JSON. Gmail и Graph рассмотрены как варианты,не выбранные пользователем интеграции.

## Предлагаемая реализация и границы транзакций

Интеграционный adapter выполняется в разрешённой клиентской области. Полученное письмо сначала получает устойчивый raw artifact и receipt с transport key. Транзакция red owner сохраняет receipt/parser job/checkpoint progress;ack допускается после commit. Извлечение MIME,HTML sanitization,OCR и LLM не держат транзакцию. Повтор notification не повторяет parse или бизнес-эффект. Parser failure сохраняет quarantine и исходный артефакт,синхронизация остальных сообщений продолжается.

Matched correlation создаёт durable pending hold до LLM classification. Сигнал с непрозрачными refs обновляет outreach,но для проверки перед отправкой нужен authoritative gate:одного eventual outbox недостаточно. Нельзя обещать атомарную отмену между mailbox,БД и SMTP;задержка hold propagation и окно внешнего эффекта наблюдаются отдельно.

### RecipientSnapshot
Владелец: lead-management red. Поля: `tenant,purpose,snapshot_id,version,entity_ref,contact_ref,endpoint_snapshot,link_revision,assertion_refs,created_at,retention`.

Ограничения: UNIQUE(tenant,snapshot_id,version);immutable payload hash;expected version lookup;current eligibility separate.

Миграция: Новые refs v2;legacy contact/company refs не повышаются автоматически до pinned assurance.

### Mailbox/FormBinding
Владелец: governance authority + integration adapter + red intake. Поля: `binding_id,tenant,purpose,channel,provider_account_ref,allowed_folder_aliases,credential_ref,policy_version,status`.

Ограничения: trusted binding→tenant;no caller tenant override;credential only permitted secret store.

Миграция: Новый registry и topology wiring;не создавать speculative service.

### TransportReceipt/Message
Владелец: lead-management red при техническом adapter integration-platform. Поля: `receipt_id,binding_id,transport_identity_kind,transport_key,provider_epoch,raw_ref,raw_hash,provider_received_at,observed_at,parse_state,message_ref`.

Ограничения: UNIQUE(binding_id,identity_kind,transport_key);Message-ID nonunique indexed metadata;cursor commit after raw durable;logical effects unique(message_ref,decision_version,effect_kind).

Миграция: Новые red migrations;provider sync starts explicit from chosen historical window;не объявлять архив полный автоматически.

### Inquiry/Conversation/CorrelationDecision
Владелец: lead-management red. Поля: `inquiry_id,initial_origin,first_source,entity_ref optional,conversation_id,participants,decision_revision,candidate_refs,basis,evidence_refs,actor,corrects_ref`.

Ограничения: No mandatory company;scoped FKs/tenant authorization;immutable decision history CAS;unique acquisition decision distinct messages.

Миграция: Legacy leads получают refs only when verified;no mass gmail-domain company creation.

### ConversationSignal
Владелец: lead-management publishes;outreach/engagement consume. Поля: `schema,event_id,tenant,purpose,conversation_ref,message_ref,correlation_revision,execution_ref,signal_kind,pending_hold_ref,occurred_at`.

Ограничения: No MIME/body/address;inbox event dedup;version-aware apply;hold per message,qualification removes exact hold.

Миграция: Catalog relationship+inproc/http transports before activation;old consumers ignore unsupported signal,новый route fail-closed for eligibility.

### OutboundEnvelope/DeliveryEligibility
Владелец: message-delivery red payload;outreach eligibility. Поля: `action_id,material_ref,recipient_snapshot_ref,sender_profile_ref,wire_message_id,date,reply_to,reply_refs,payload_ref,eligibility_epoch,attempt_ref`.

Ограничения: Immutable action payload/fingerprint;last pre-effect owner check;unique action reservation;unknown never blind retry.

Миграция: Green status+refs and red payload;dual-read only explicit legacy;move body/address under retention/audit with cutover rehearsal.

## Состояния

### Message ingest
- `notification-observed→raw-durable→receipt-committed→parse-pending→parsed|parse-partial|quarantined`
- `parsed→correlation-matched|unmatched|conflict`
- `matched→pending-reply-hold→qualified-human|auto|dsn|spam|unknown`

Cursor/ack следуют receipt commit;parse quarantine не теряет raw.
OOO/DSN не human interest;timeout держит pending hold.

### Delivery
- `approved-pinned→queued→reserved→eligibility-checked→dispatch-started→provider-accepted|definitive-failure|unknown`
- `unknown|stale-sending→reconciliation-pending→accepted-evidenced|not-accepted-evidenced|unknown`

Текущее delivered означает accepted provider,не delivery/read.
Нет перехода unknown→retry только по таймеру/отсутствию Sent copy.
После фактического эффекта новая suppression блокирует следующие действия,не переписывает прошлое.

### Late contact enrichment
- `known-entity+OpenNeed→message-observation→candidate link→validated assertion→new dossier version+need fulfilled`
- `support revoked→dependency reevaluation→needs-review|need reopened`

Деловое письмо не создаёт новую человеческую заявку и не увеличивает лимит Firecrawl.
Receipt остаётся в red scope;completed mission snapshot неизменен.

## Гипотезы и решения

### COMM-H01
Двухэтапное правило+LLM выделение новой части и намерений достаточно для русско-/англоязычных цепочек с цитатами.
Эксперимент: Размеченный независимый corpus human/OOO/DSN/forward/quoted positive+new unsubscribe; отдельно languages и HTML/plain; judge не видит ожидаемую метку.
Приёмка: Ноль пропущенных явных stop signals и wrong-subject updates на acceptance; precision/recall с n и CI публикуются, пороги обычных intents задаются до испытания.
Fallback: Сохранять unknown/needs-review, не снимать hold при timeout; quote extraction никогда не уничтожает raw MIME.

### COMM-H02
Согласованные token+reply headers дают достаточно точную автоматическую корреляцию.
Эксперимент: Разрешённый mailbox roundtrip с несколькими campaign/tenant, forwards, token A/References B, shared endpoint и отсутствующими headers.
Приёмка: Ноль cross-tenant/wrong-conversation auto-links на acceptance; покрытие автоматической корреляции измеряется отдельно от precision.
Fallback: Unsorted inquiry и список scoped candidates; оператор связывает версионной командой.

### COMM-H03
Одного mailbox adapter достаточно первому полному сценарию, включая надежное восстановление.
Эксперимент: Выбрать доступный реальный рабочий mailbox; renewal/reconnect/backfill/duplicate notifications/expired cursor; остановка до и после каждого commit.
Приёмка: Каждый provider transport identity отражён ровно одним logical message; все gaps видны, восстановление не удваивает lead/engagement effects.
Fallback: Переход на поддержанный API/IMAP того же провайдера; недоступный provider остаётся открытой зависимостью, без подмены Mailpit.

### COMM-H04
Проверенные поздние контакты из входящих обращений повышают заполненность существующей компании без ложных merge.
Эксперимент: T0 компания без email;T1 receipt с matching ID, conflicting name, common provider domain и referral;T2 verification;T3 merge revoke;T4 повтор.
Приёмка: Email без подтвержденной company link сохраняется candidate; подтверждённый контакт дополняет прежнюю entity и закрывает matching need; snapshot заявки неизменен.
Fallback: Независимый inquiry/contact candidate и review без прямого привязывания компании.

- **COMM-D01 [proposed]** Реализовать outreach/inbound как потребителей единого evidence graph и поздние источники обогащения после первого discovery/crawl среза. Основание: Пользователь выделил главный приоритет поиска; письмо полезно ровно настолько, насколько проверен найденный субъект/контакт.
- **COMM-D02 [proposed]** RecipientSnapshotRef и immutable outbound envelope + mutable eligibility gate. Основание: Состав разрешённого эффекта фиксируется, разрешённость может измениться; смешение этих моделей допускает замену адреса.
- **COMM-D03 [unresolved]** Конкретный mailbox adapter выбрать по реально имеющемуся tenant mailbox; API с backfill предпочтителен при доступных scopes, IMAP поддержанного release —альтернатива. Основание: Ни Gmail,ни Graph,ни IMAP пока пользователь не выбрал; инфраструктура изменяет эксплуатацию, но не inquiry semantics.
- **COMM-D04 [unresolved]** Режим чужих форм фиксировать9.7; sender profile/form discovery/preparation обязательны независимо от manual/automated исполнения. Основание: Пользователь не выбирал manual-only; browser research не имеет send authority.
- **COMM-D05 [proposed]** Не строить собственный SMTP/IMAP сервер для первого приема; не менять email local-part регистр/plus/dots при entity matching без provider-specific evidence. Основание: Транспорт и identity независимы; эвристическое склеивание endpoints может перенести чужой контакт.

## Порядок и зависимости

Порядок этой lane не задаёт приоритет всей программы. Сначала3.x/4.x дают проверенный субъект,контакт и память. Затем9.1→9.2 образуют доказательный материал. Параллельно10.1→10.2 строят inbound storage/contracts;10.3 (форма) и10.4→10.5→10.6 (mailbox) независимы.10.7 связывает outbound headers с реальным receiving channel;10.8 даёт correlation;10.10 классификация параллельна correction/hold.10.14→10.11 дают управляемое применение reply.9.3→9.4→9.6 дают approved delivery.10.9→10.12→10.13 связывают inbound с late enrichment и operator UI.9.7 —явное решение исполнения чужих форм,не скрытое manual-only.

Полный DAG в JSON,проверен на отсутствие внутренних циклов. Зависимости от других lane обозначены исходными task IDs. Runtime цикл «новый источник→новое знание→новая потребность» не является циклом порядка реализации.

## Все 21 assigned задачи: конкретные шаги

### 9.1 · lead-management → outreach-orchestration
Зависит от: 1.2, 1.5, 3.4, 3.6, 4.9. Вход: Entity+dossier/assertion versions, criteria, offer, language, purpose и channel. Выход: Immutable handoff(version) и recipient snapshot либо объяснимый no-recipient/needs-verification.
Code anchors: `contracts/lead-management/deliverytarget/deliverytarget.go:50`, `backend/contexts/outreach-orchestration/internal/app/command/submit_handoff.go:156`.
Предлагаемые места: proposed contracts/lead-management/researchhandoff/; proposed lead-management red recipient_snapshot/research_handoff tables.

1. Определить ResearchHandoffV1{tenant,purpose,request+criteria version,entity,dossier,assessment,recipient_snapshot_ref,offer_version,language,allowed_claim_refs,evidence_snapshot_ref}.
2. Recipient snapshot фиксирует endpoint и subject-link revision; unique(tenant,snapshot_id), unique(tenant,handoff_id,version), payload digest запрещает конфликтующий replay.
3. Создать handoff+outbox в одной owner transaction; потребитель хранит inbox receipt и создаёт свой draft ID независимо от RecipientRef.
4. Legacy handoff оставлять читаемым; новые snapshots не обновляют старый approved draft автоматически.

Отказы: Телефон-only/форма-only; Владелец контакта недоступен; Повтор с тем же ID/другим payload; CompanyID fallback после approval.
Приёмка: Раздельные company/contact/email-ready counters; Recipient и material rejection отличны от unavailable; Snapshot воспроизводит конкретный endpoint и evidence.

### 9.2 · content-production при предоставлении фактов lead-management
Зависит от: 9.1. Вход: Handoff snapshot, offer/style/language и разрешённые claims. Выход: Material version + factual span→claim usage + prompt/model versions.
Code anchors: `backend/contexts/lead-management/internal/app/draft_email.go:87`.
Предлагаемые места: proposed research-backed generation adapter; proposed claim_usage table/material metadata.

1. Генерировать structured draft{subject,body,claim_usages[span,claim_ref]} через единый model gateway.
2. Проверить существование refs, subject scope и доступность; factual spans без основания возвращать на review.
3. Сохранить exact text digest, evidence bundle и lineage; LLM judge служит проверкой, не независимым свидетельством.
4. Для withdrawn evidence создать dependency invalidation, не переписывать отправленные historical материалы.

Отказы: Новость превращена в намерение купить; Неизвестное имя; Ссылка на чужой tenant/другую компанию; Старый source revoked после generation.
Приёмка: Неизвестные сведения не подставлены; UI может раскрыть основание каждой существенной персонализации; Текст и refs версионны.

### 9.3 · outreach-orchestration, lead-management, message-delivery
Зависит от: 9.2, 10.2. Вход: Exact material, recipient snapshot, sender/channel profile. Выход: Approval exact fingerprint и immutable dispatch payload.
Code anchors: `backend/contexts/outreach-orchestration/internal/domain/action_draft.go:229`, `backend/contexts/outreach-orchestration/internal/ports/contracts/approved_action.go:84`, `contracts/lead-management/deliverytarget/deliverytarget.go:50`, `backend/contexts/message-delivery/internal/adapters/persistence/postgres/message_repository.go:228`.
Предлагаемые места: proposed delivery-target v2 resolver; proposed approved-action envelope extension/version; proposed red delivery_payload and green payload_ref.

1. Определить approval fingerprint material_version+recipient_snapshot+sender_profile+channel+purpose; existing campaign/draft/version action identity сохранить.
2. v2 resolver принимает snapshot ref и expected version, возвращает именно pinned address и current eligibility ref; измененный endpoint требует нового draft/approval.
3. Передавать body/address через red payload ref; topology/migration gate из10.2 закрывает существующий green snapshot reuse.
4. Историю legacy approvals оставить с семантикой legacy-unpinned, не объявлять автоматически достаточной новым правилам.

Отказы: Stale decision; Recipient меняется после approve; Ссылка material unavailable; Повтор action одного campaign и совпадение draft ID другого.
Приёмка: Нет подмены адреса; Точный текст/состав совпадают с approval; Новый endpoint требует нового решения.

### 9.4 · message-delivery + outreach-orchestration
Зависит от: 9.3, 10.14, 10.11. Вход: Approved dispatch+current eligibility и receipts входящих. Выход: accepted/failed/unknown/held с объяснением и replay-safe reconciliation.
Code anchors: `backend/contexts/message-delivery/internal/app/command/deliver_message.go:99`, `backend/contexts/message-delivery/internal/adapters/mailer/provider/provider.go:80`, `backend/contexts/message-delivery/internal/domain/message.go:194`.
Предлагаемые места: proposed DeliveryEligibilityCheck contract; proposed delivery_reconciliation journal; proposed dispatch_gate epoch.

1. Разделить pending/reserved/dispatch-started/provider-accepted/unknown/reconciled; дописать staging status при совместимой domain migration.
2. Перед внешним вызовом прочитать owner eligibility revision: suppression, pending reply, evidence validity, approval fingerprint, sender limits. Не держать сетевой SMTP в DB transaction.
3. Сохранить provider attempt+wire message ID до эффекта; reservation replay не шлёт повторно.
4. Unknown/stale sending проверяет provider logs/Sent/API при поддержке; positive evidence связывается с attempt. Отсутствие evidence не равно not-sent. Решение reissue отдельное с оператором и учётом duplicate risk.
5. Outbox публикует статус unknown отдельно от delivered; metrics age/lag/reconciliation backlog.

Отказы: Reply после reserve до gate; Reply после gate/provider accepted; DATA ack lost; Crash до/после external send; Mailbox/Sent eventual consistency.
Приёмка: Не начавшийся и наблюдаемо blocked follow-up не отправлен; Unknown не retryable; Никакого обещания exactly-once SMTP или отмены прошлого эффекта.

### 9.5 · outreach-orchestration с sender profile owner
Зависит от: 9.1, 10.2. Вход: Обнаруженная форма+schema/source snapshot, material, реальные sender contacts. Выход: Prepared task/needs-data/needs-review, без неразрешённого эффекта.
Code anchors: `contracts/integration-platform/mail/mail.go:30`.
Предлагаемые места: proposed sender_profile versioned red storage; proposed outbound_form_task contract.

1. Профиль задаёт подтверждённые реальный receiving channel/email/name/company и optional phone/site; хранить version/binding refs.
2. Field map хранит label/type/required/value origin и форму version/action endpoint; CSRF tokens не долговременная identity.
3. Разделить readable browser исследование и действие заполнения/отправки; перед выбранным execution проверить актуальность формы/обязательных полей.
4. Manual confirmation хранит actor/time/evidence; автоматическое подтверждение,если выбрано9.7,имеет отдельный provenance и uncertain outcome.

Отказы: Обязательный телефон отсутствует; Форма изменилась; Контактный адрес не принимает ответы; Support/job form не подходит выбранному purpose.
Приёмка: Не выдуманы реквизиты; Prepared task виден пользователю; Подтверждение отправки не выдано за прочтение.

### 9.6 · outreach-orchestration UI и request-fulfillment projection
Зависит от: 9.4, 9.5, 9.7. Вход: События material/decision/delivery/form outcome. Выход: Наблюдаемая timeline и channel runbook.
Code anchors: `backend/contexts/outreach-orchestration/internal/ports/contracts/approved_action.go:84`.
Предлагаемые места: proposed existing console materials/recipient/evidence views.

1. Добавить статусы prepared/awaiting-approval/held/accepted/unknown/failed/manual-confirmed; имена delivered существующего domain показывать как «принято провайдером» до более сильного evidence.
2. Отобразить pinned recipient/material versions, source claims, причины ожидания; показывать реальную доступность канала.
3. Проверить разрешённый real-mailbox end-to-end и выбранный form-mode acceptance; capture-only acceptance маркировать отдельно.

Отказы: Зависший unknown не виден; Green projection показывает body; Tenant navigation теряет scope.
Приёмка: Пользователь раскрывает каждый этап; Реальный канал и исход подтверждены; UI не утверждает прочтение по SMTP250.

### 9.7 · outreach-orchestration product/spec owner
Зависит от: 1.3. Вход: Обязательная работа с формами и разрешение/режим конкретного канала. Выход: Явное решение режима,спецификация эффекта,acceptance route.
Code anchors: новый путь.
Предлагаемые места: proposed MODIFIED delta existing/pending outreach-channel-form.

1. Сопоставить pending manual план и новую программу; не объявлять историческое предложение пользовательским выбором.
2. Выбрать manual/assisted/automated mode per channel version с reviewable UX и exact payload authority; подготовка не блокируется отсутствием execution mode.
3. Для automated режима добавить отдельный executor с preflight schema checks, эффекты submit и observability; research browser не получает этот порт.
4. Согласовать переходы при CAPTCHA, duplicate submission, server confirmation и unknown response; записать результат режима,не обещать гарантированную delivery.

Отказы: Пустой mode трактуется manual-only; Research browser нажимает Send; Повтор после неизвестного результата.
Приёмка: Нет скрытой автономной отправки; Полный form scope сохранён; Acceptance соответствует выбранному,а не заранее ручному режиму.

### 10.1 · lead-management/outreach/integration architectural owners
Зависит от: 1.2. Вход: Новые требования и существующая рамка. Выход: Согласованные names/direction/storage/compatibility.
Code anchors: `contracts/outreach-orchestration/formsubmission/formsubmission.go:52`, `backend/contexts/message-delivery/migrations/green/0001_message.sql:18`.
Предлагаемые места: proposed explicit canonical deltas for mailbox and forms.

1. Развести inbound-form-intake от outbound-form-task в published contracts; v1 form-submission остаётся relay adapter.
2. Проверить canonical/pending mail-sync exclusions; отражать уже сделанную BRD ревизию как требования,не runtime.
3. Зафиксировать red mailbox payload boundary и обнаруженное green delivery расхождение; подготовить migration target до включения reply content.

Отказы: Один API одновременно принимает посетителей и отправляет чужую форму; Старые consumers сломаны; UI показывает функцию implemented по одному BRD.
Приёмка: Specs/catalog/domain model согласованы; Старый relay работает по прежнему контракту.

### 10.2 · lead-management red roots;integration-platform transport;governance bindings
Зависит от: 10.1, 3.1, 3.3. Вход: Зарегистрированный tenant/purpose mailbox/form authority. Выход: Проверяемые bindings,owner boundaries и migrations.
Code anchors: `architecture/domain-catalog.yaml:153`, `contracts/outreach-orchestration/formsubmission/formsubmission.go:52`.
Предлагаемые места: proposed red mailbox_binding/form_binding/inquiry/conversation/message_receipt tables; proposed published message-intake/conversation-signal contracts.

1. Mailbox binding: opaque ID,tenant,purpose,provider account ref,allowed folders/aliases,credential ref,scope,retention,status; секрет не в green registration.
2. Red inquiry/conversation roots не требуют CompanyID; связь entity/contact optional. Технический adapter передаёт authority из binding,не из To/body.
3. Создать schema/index/constraints,red object-store namespace; raw payload store transaction uses preuploaded durable immutable blob plus receipt commit и orphan GC.
4. Для существующего green delivery payload предусмотреть versioned ref migration и dual-read cutover до reply integration; не переносить исторические данные без audit.

Отказы: Forged tenant header; Несуществующий alias binding; Сбой blob upload до receipt; Green fallback при недоступном red.
Приёмка: Archlint/importlint; Cross-tenant rejected; Нет green MIME/body/addresses в новых контрактах.

### 10.3 · lead-management client intake;frontend public relay
Зависит от: 10.2. Вход: Binding-scoped SubmissionID,contact/message,form version,attribution. Выход: Durable inquiry receipt либо объяснимая недоступность.
Code anchors: `contracts/outreach-orchestration/formsubmission/formsubmission.go:52`, `frontend/public/internal/adapters/forwarder/forwarder.go:129`.
Предлагаемые места: proposed inboundformintake v2; proposed red form_receipt table; proposed frontend/public submission_id forwarding.

1. Public FormRef определяет binding; caller tenant_id игнорировать/отклонять. Server-signed form instance или эквивалент scoped token задаёт submission ID, сохраняющийся при retry.
2. UNIQUE(binding_id,submission_id),payload hash compare; SAME→same receipt,DIFFERENT→409.
3. В red transaction сохранить receipt+inquiry+outbox;2xx только после durable accept. Front relay не хранит payload; failure не обещает успех.
4. Consent/notice policy версионировать отдельно от универсального bool,старый v1 сохраняет свою обязательность до explicit migration.

Отказы: Retry после commit/ответ потерян; Duplicate button press; Gmail contact без company; Client intake недоступен.
Приёмка: Один inquiry и один последующий effect; Не создана компания gmail.com; Посетитель видит достоверный исход.

### 10.4 · integration-platform adapter исполняется в разрешённой client/red роли
Зависит от: 10.2. Вход: Реальный рабочий mailbox и scope из binding. Выход: Authenticated receipt+raw MIME reference.
Code anchors: новый путь.
Предлагаемые места: proposed mailbox adapter provider-specific package; proposed mailbox checkpoint port.

1. Выбрать один доступный provider/IMAP adapter,зафиксировать supported release/scopes/folders. Subscription notifications считать сигналом чтения источника,не само письмо.
2. API adapter использует raw/metadata endpoints;IMAP ReadOnly EXAMINE+UID fetch,не sequence numbers. Не менять флаги личного mailbox без необходимости.
3. Auth renewal/subscription expiration/status показывать оператору; adapter capability заявляет supported backfill/alias/raw/immutable-id semantics.
4. Real acceptance подключает согласованный test mailbox;этот этап не требуется первому crawl seed.

Отказы: OAuth revoked; Watch expired; Quota429; UIDVALIDITY изменился; Raw MIME unavailable.
Приёмка: Настоящее письмо принято устойчиво; Технические ошибки различимы; Не fixture-only mail acceptance.

### 10.5 · integration-platform checkpoints + lead-management red receipt
Зависит от: 10.4. Вход: Provider notifications/fetch results+checkpoint. Выход: Replay-safe durable receipt и монотонный checkpoint within epoch.
Code anchors: `frontend/public/internal/adapters/forwarder/forwarder.go:129`.
Предлагаемые места: proposed transport_receipt unique indexes; proposed mailbox_sync_state epoch/checkpoint.

1. Transport key: binding+provider immutable ID;IMAP binding+folder+UIDVALIDITY+UID. Дубликат receipt найден до parser. Webhook notification ID и logical message ID хранить раздельно.
2. Сначала durable raw blob,потом transaction receipt/inbox+parser job+checkpoint progress;ack после commit. Crash between blob and commit даёт orphan cleanup,не loss.
3. При parallel batches advance только contiguous completed page/UID range с учётом holes;новая эпоха UIDVALIDITY делает scoped backfill,не resets old business IDs.
4. Gmail404 history→full scoped sync;Graph per-folder opaque delta+ImmutableId on every call;overlap rounds dedup by stable transport ID.
5. Message-ID/hash не глобальный UNIQUE; semantic duplicates связывать review/aliases,два одинаковых намеренных сообщения сохранять.

Отказы: Ack потерян; Notification duplicate; Provider IDs меняются при folder move; Poison parse; Cursor advance раньше blobs commit.
Приёмка: Нет lost messages/effect duplicates в fault matrix; Gaps/recovery visible; Missing Message-ID не теряет письмо.

### 10.6 · lead-management parsing и document/OCR adapter по потребности
Зависит от: 10.5, 3.3. Вход: Durable raw MIME. Выход: Версионный parsed message/parts/text spans либо quarantine с raw lineage.
Code anchors: новый путь.
Предлагаемые места: proposed bounded MIME parser package; proposed red message_part/artifact refs.

1. Закрепить go-message release+charset support после fixture check;прочитать parts streaming до NextPart,сохранять unknown charset/encoding partial result flags.
2. Лимиты raw bytes,decoded bytes,headers,part count,nesting,attachments,archive ratio+CPU timeout;parser worker отдельно от connector ack.
3. Сохранять raw artifact hash,part ordinal/content ID/media type,decoded hash,source range;HTML sanitizer+sandbox,no remote loads.
4. Разделить header/body/new text/quoted text/signature как versioned spans с uncertain segmentation;OCR вложений подключает общий evidence adapter8.7 позднее,ядро приёма не зависит от OCR.

Отказы: Zip bomb; Malformed MIME; Неизвестная кодировка; HTML remote image/XSS; Image-only attachment.
Приёмка: Raw можно повторно разобрать; UI безопасно отображает unknown; Все извлечённые факты имеют message part locator.

### 10.7 · message-delivery envelope + lead-management correlation map
Зависит от: 10.4, 9.3. Вход: Confirmed mailbox/alias + pinned outbound action. Выход: Реально работающий Reply-To и persisted exact wire headers.
Code anchors: `backend/contexts/integration-platform/internal/adapters/smtp/smtp.go:196`, `contracts/integration-platform/mail/mail.go:30`.
Предлагаемые места: proposed mail envelope v2; proposed reply_token/wire_message mapping.

1. Стабильный RFC Message-ID генерировать один раз на message envelope и хранить дословно;реальный tenant-controlled ID domain,случайный уникальный opaque ID. Header injection validation.
2. Добавить Reply-To,In-Reply-To,References,Date;для reply соблюсти provider threading requirements,не приравнивая provider thread к business conversation.
3. Token opaque random,scope tenant/binding/conversation,hash/token-ref mapping с retention;token не предоставляет access.
4. Провести реальную отправку/ответ через фактический alias/plus route;неподдержанный route→обычный mailbox+headers,сниженная автоматическая корреляция.

Отказы: Provider переписывает address; Plus address не принимается; Переадресованный token; ID collision при sanitization; Токен от другого binding.
Приёмка: Пришедший ответ находится по persisted headers/token; Несовпадение scope не открывает чужие данные; Не заявлен неподдержанный alias.

### 10.8 · lead-management conversation owner
Зависит от: 10.6, 10.7. Вход: Parsed headers+binding+local outbound refs+known participants. Выход: Matched/unmatched/conflict decision с scoped candidates.
Code anchors: новый путь.
Предлагаемые места: proposed CorrelateMessage command; proposed correlation_decision revision/history table.

1. Собрать все candidate evidence,не остановиться слепо на первом приоритете; конфликт tokenA/headerB→review. Header/provider thread keys scoped account и tenant.
2. Точный message ID хранить как parsed opaque identifier;не переписывать регистр/пунктуацию произвольно. Дубликаты header refs дадут множество кандидатов.
3. Strong match может связать conversation,но не доказывает sender identity/role/company;shared endpoint держит unknown entity.
4. Manual relink command содержит expected decision version,actor,reason;обновляет projections/idempotent outbox,не переносит tenant и не удаляет receipt.

Отказы: Same subject; Forward/new sender; Shared endpoint у двух компаний; Duplicate Message-ID; Conflicting refs.
Приёмка: Нет wrong merge на acceptance; Виден chain of correlation reasons; Неуверенная связь не генерирует engagement другой кампании.

### 10.9 · lead-management inquiry/entity evidence owner
Зависит от: 10.8, 10.10, 4.13, 4.14. Вход: Parsed+classified business message и correlation decision. Выход: Новый inquiry/contact candidate либо обновление прежней entity с provenance.
Code anchors: новый путь.
Предлагаемые места: proposed ApplyInquiryDisposition command; proposed message-origin/lead-link ledger.

1. Receipt/inquiry существует до LLM;qualified human business message создаёт lead candidate по UNIQUE(tenant,inquiry acquisition identity),а reply добавляет touch существующей истории.
2. Не создавать synthetic CompanyID из consumer mailbox;entity relation optional. ID из подписи/email creates assertion наблюдение,через shared verifier3.4/3.6 проверяется.
3. Late confirmed contact из message дополняет старую entity и closed matching OpenNeed с evidence dependency;referral остается candidate до проверки.
4. Сохранить first_origin отдельно от acquisition/touch history;projection updates idempotent по message+decision revision;terminal mission snapshots immutable.

Отказы: OOO/DSN/spam; Смена correlation после create; Реальный business inquiry без company; Confirmed ID против конфликтующего имени.
Приёмка: Не дублируются уникальные leads; Late email закрыл нужный пробел; History/завершённая заявка не переписаны.

### 10.10 · lead-management semantic parse;engagement-observation qualified outcome
Зависит от: 10.6. Вход: New-body spans,headers,provider-auth metadata в trust boundary. Выход: Type+multi-intents+quotes+operational suggestion с review flag.
Code anchors: новый путь.
Предлагаемые места: proposed ClassifyMessage versioned result contract; proposed red classification/correction tables.

1. Формальные parser rules DSN/auto-submitted/trusted provider signals идут до LLM;header сам по себе не доказывает человека.
2. LLM structured output хранит message version,prompt/model version,тип,intents[],quote spans,confidence отдельно по полям;missing evidence→unknown.
3. Stop signal имеет precedence над positive tone,quote history и signature не смешивать с новым намерением.
4. Operator correction expected-version CAS с audit;новая квалификация пересчитывает downstream effects без повторного lead creation.

Отказы: Old positive quote/new unsubscribe; OOO with return date; Model timeout; Forged Authentication-Results; Номер в подписи.
Приёмка: OOO не interest; Отказ не потерян; Подпись не registry-confirmed; Raw text доступен для исправления.

### 10.11 · engagement-observation → outreach/data-governance
Зависит от: 10.10, 10.14. Вход: Qualified outcome+conversation binding+revision. Выход: Paused/stopped/remains-held/unqualified outcomes и endpoint feedback.
Code anchors: `backend/contexts/outreach-orchestration/internal/domain/campaign_sequences.go:174`, `backend/contexts/engagement-observation/internal/domain/engagement_kind.go:13`.
Предлагаемые места: proposed qualified-conversation outcome contract extension; proposed suppression source linkage.

1. Human reply→suspended/needs-response;unsubscribe→scope-qualified suppression+stopped;rejection→current sequence stop;OOO/DSN отдельные outcomes по policy.
2. Outbox qualified event carries opaque refs/revision/kind;body и quotes остаются red. Idempotency по message/decision version и downstream event ledger.
3. Hard bounce обновляет endpoint validity как observation,не удаляет компанию;referral не разрешает новую отправку.
4. Не снимать hold если другая pending message/супрессия ещё действуют;operator resume requires current eligibility revision.

Отказы: Classifications out of order; Reply исправлен на OOO; Более поздняя отписка после resume; Один bounced адрес у компании.
Приёмка: Разные effects для human/OOO/DSN/stop; Нет повторных событий/новых лидов; Область suppression объяснима.

### 10.12 · lead-management console + scoped views
Зависит от: 10.9, 10.11. Вход: Receipt/inquiry/correlation/classification/action refs. Выход: Inbox и timeline со ссылками на company/contact и evidence.
Code anchors: новый путь.
Предлагаемые места: proposed inbox queries/UI; proposed conversation timeline projection.

1. Фильтры owner/channel/state/unmatched/needs-review;раздельно ingress time/header Date/sent time и source.
2. Timeline показывает raw-derived safe view,new-body,attachments,trust dimensions,correlation reasons,current owner,next action.
3. Mutations relink/correction/reply-draft audit и tenant authorization;PII не в общей telemetry.
4. Late contact visible в старой карточке с датой появления и источником;no-email карточки остаются.

Отказы: 404/login tenant mismatch; Green list leak; Временная ошибка classification скрыта; Многократные messages увеличили count.
Приёмка: Объекты за каждым счётчиком доступны; Оператор исправляет ошибку связи; Последний meaningful state и возраст ожидания видны.

### 10.13 · integration-platform/lead-management operations
Зависит от: 10.3, 10.5, 10.6, 10.7, 10.8, 10.12. Вход: Реальные mailbox/form bindings и instrumented pipeline. Выход: Описанное восстановление,retention,rotation и acceptance report.
Code anchors: новый путь.
Предлагаемые места: proposed mailbox-and-form-runbook.md; proposed fault matrix + real acceptance evidence.

1. Документировать scopes/folders/oauth renewal/subscription expiry/backfill,права operator и red storage topology.
2. Проверить restarts в каждой boundary,expired cursor,duplicate callback,UIDVALIDITY reset,malformed MIME;record reconciled IDs без PII в общих логах.
3. Retention удаляет raw/parts/quotes/summaries/vectors/correlation data по scope;операционный tombstone минимален и не воскрешает revoked content при replay.
4. Метрики ingress lag,unparsed age,unmatched age,pending hold age,sync gaps,dead letters,real receiving probe;не использовать отправки людям как healthcheck.

Отказы: Credential rotation; Provider outage; Забытый quarantine; Retention replay resurrection.
Приёмка: Реальный connected mailbox и red form sink; Recovery воспроизводим; Запрошенный scope ограничен и состояние доступно.

### 10.14 · lead-management → outreach-orchestration,parallel engagement
Зависит от: 10.2, 10.8. Вход: Устойчивый matched message receipt,correlation decision и revision. Выход: Pending-reply hold до classification,затем контролируемый outcome transition.
Code anchors: `backend/contexts/outreach-orchestration/internal/domain/campaign_sequences.go:174`.
Предлагаемые места: proposed contracts/lead-management/conversationsignal/; proposed conversation_hold table and eligibility epoch.

1. В red owner transaction correlated receipt+pending hold+outbox;contract carries tenant,conversation,execution refs,message ref,decision version,event ID,hold reason без body.
2. Outreach inbox idempotent применяет hold и увеличивает eligibility epoch. Для gate9.4 читать authoritative pending state/compatible owner port,не полагаться только на eventual signal.
3. Hold keyed message/correlation version;multiple unresolved messages→multiple hold reasons. Квалификация снимает лишь свой hold после durable outcome,не любой общий boolean.
4. Relink conflict отменяет прежнюю кандидатную причинность через versioned compensation;terminal accepted send не меняется. Timeout не auto-resume;UI age/reason.

Отказы: Classification timeout; Out-of-order matched/relinked/qualified events; Event delivery lag; Другой pending reply при resolved первом.
Приёмка: Follow-up held до устойчивой qualification; Pending не counted human reply; Нет false resume по устаревшему событию.

## Проверка всех нормативных предложений двух capability

| Requirement | Вердикт | Причина | Задачи |
|---|---|---|
| Готовность компании и адресата различаются | retain | Полезный контакт определяется целью и каналом; телефон-only компания живёт в памяти и может закрыть company-with-contact,но не email-ready. | 9.1 |
| Материал опирается на разрешённый снимок фактов | refine | Нужны immutable claim usage spans и lineage,snapshot revocation не переписывает историю. | 9.1, 9.2 |
| Точное утверждение и повторная проверка перед доставкой | correct | Норма верна,но existing draft version не фиксирует current mutable recipient;версионный resolver требуется отдельно. | 9.3, 9.4 |
| Отдельный статус неопределённого исхода отправки | refine | Unknown уже существует;добавить observable recovery/reconciliation и stale sending handling. | 9.4 |
| Обратная связь имеет правильную область | retain | Bounce/отписка относится к endpoint/действию;через единый evidence owner обновляет актуальность и зависимости. | 10.9, 10.11 |
| Ответ учитывается на границе внешнего эффекта | refine | Определить owner pending state/read gate и невозможность атомарного remote cancel;async signal один недостаточен. | 9.4, 10.14 |
| Источник обращения сохраняется | retain | Origin и touches различны;business inquiry без company допустим,формальная заявка автоматически не создаётся. | 10.3, 10.9 |
| Приём привязан к доверенной области | refine | Новые red tables/bindings обязательны;existing delivery green payload нельзя объявить red-ready. | 10.1, 10.2 |
| Повторная доставка не дублирует обращение | refine | Нужно отличить notification/transport/message/business identity и provider-specific ID epoch;receipt dedup до parse. | 10.3, 10.5 |
| Корреляция переписки объяснима | refine | Нужны persisted actual outbound wire IDs/headers и versioned correction;приоритет не заменяет анализ конфликтов. | 10.7, 10.8 |
| Смысл ответа влияет на следующий шаг | experiment | Требование сохраняется;precision quote/new-body classification проверять на независимой multilingual выборке,а стоп-сигналы не доверять одной тональности. | 10.10, 10.11 |
| Наш обратный контакт реален | pending-decision | Принимающий канал обязателен;режим формы остаётся явным решением9.7,убрать оставшийся manual tail design. | 9.5, 9.7, 10.7 |
| Переписка и исходы видны оператору | retain | Inbox/timeline включая late contact нужны;message claim не превращается в registry fact и сохраняет source trust. | 10.9, 10.12 |
| Конфликт признаков корреляции не скрывается | retain | Не выбирать token без учета противоречащих refs;shared endpoint не даёт две компании. | 10.8 |
| Связанный ответ ожидает разбора до продолжения | refine | Hold versioned по message,не один boolean;gate должен видеть его до dispatch независимо от медленной qualification. | 10.14, 9.4 |
| Новое содержание отделяется от истории письма | experiment | Полное автоматическое разделение не гарантирует MIME parser;segmentation uncertainty и original artifact обязательны. | 10.6, 10.10 |
| Уникальные лиды отделяются от числа сообщений | retain | Stable acquisition/inquiry decision ledger,receipt и touches не меняют origin/unique count. | 10.9, 10.12 |

## Ограничения исследования

- Живые provider/mailbox/SMTP испытания не выполнялись;пользовательские ключи не использовались.
- Прямые RFC5321/5322/9051/3464/3834 fetch заблокированы network403;не объявлять их заново прочитанными. Provider official API descriptions и upstream implementation source прочитаны.
- Code inspection не равен acceptance;никакие задачи реализации не закрыты.
