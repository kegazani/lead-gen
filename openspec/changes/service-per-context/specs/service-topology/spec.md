# service-topology Specification

## Purpose

Capability задаёт сервисную топологию платформы: bounded context как единицу сервиса и развёртывания, соответствие «один контекст — один сервис», допустимый транспорт межконтекстных контрактов и правила надёжной синхронной границы.

## ADDED Requirements

### Requirement: Сервис как единица развёртывания

Единицей сервиса и развёртывания SHALL быть bounded context. Границы отказа, релиза и масштабирования сервиса SHALL совпадать с границей owning context. Контекст SHALL поставляться как один сервис; совмещённый первый деплой нескольких сервисов в одном образе и конфигурации MAY быть допустим, но SHALL NOT менять границу сервиса.

#### Scenario: Контекст разворачивается одним сервисом
- **WHEN** контекст поставляется как сервис
- **THEN** границы его отказа, релиза и масштабирования совпадают с границей контекста

#### Scenario: Совмещённый первый деплой
- **WHEN** несколько сервисов поставляются в одном образе и конфигурации
- **THEN** каждый из них сохраняет границу своего контекста, а последующее выделение отдельного сервиса не требует пересмотра модели

### Requirement: Соответствие «один контекст — один сервис»

Каждый bounded context SHALL соответствовать ровно одному сервису, а каждый сервис SHALL обслуживать ровно один bounded context. Процессы `api`, `ingest`, `sender`, `worker`, `chains`, `schedule` SHALL быть ролями (режимами) запуска внутри сервиса и SHALL NOT быть единицей размещения.

#### Scenario: Контекст и сервис соответствуют один к одному
- **WHEN** контекст объявлен с сервисной принадлежностью
- **THEN** ровно один сервис обслуживает этот контекст и ни один сервис не обслуживает несколько контекстов

#### Scenario: Процесс используется как единица размещения
- **WHEN** развёртывание, релиз или конфигурация объявляют `api`, `ingest`, `sender`, `worker`, `chains` или `schedule` единицей размещения
- **THEN** проверка сервисной границы завершается ошибкой

### Requirement: Допустимый транспорт контрактов

Вид связи SHALL быть обязательным транспортным атрибутом каждой межконтекстной связи в каталоге. Синхронные виды `command` и `query` SHALL выполняться сетевыми вызовами между сервисами; при совмещённом деплое, когда сервисы связи исполняются в одном рантайме, вызов MAY выполняться in-process адаптером того же контракта. Выбор транспорта SHALL задаваться топологией, а не кодом потребителя, а сетевое разделение сервисов SHALL оставаться целевым состоянием. In-process адаптер SHALL сохранять семантику контракта — дедлайн, отмену и ключ идемпотентности — так, чтобы переход к сетевому адаптеру не менял наблюдаемое поведение. Асинхронные виды `event` и `policy` SHALL идти через брокер. Связь без объявленного вида либо с видом, несовместимым с типом взаимодействия, запрещена.

#### Scenario: Синхронный вид связи
- **WHEN** связь имеет вид `command` или `query`
- **THEN** вызов идёт по объявленному синхронному транспорту — сетевым вызовом между раздельными сервисами или in-process адаптером при совмещённом деплое — с таймаутом и обработкой частичного отказа

#### Scenario: Совмещённый деплой
- **WHEN** сервисы связи исполняются в одном рантайме
- **THEN** синхронный вызов MAY выполняться in-process адаптером, сохраняющим семантику контракта, а переход к сетевому адаптеру при разделении SHALL не менять код потребителя

#### Scenario: Асинхронный вид связи
- **WHEN** связь имеет вид `event` или `policy`
- **THEN** сообщение доставляется через брокер и не требует синхронного ожидания ответа

#### Scenario: Связь без объявленного вида
- **WHEN** связь объявлена без вида связи или её вид несовместим с типом взаимодействия
- **THEN** проверка сервисной границы завершается ошибкой с идентификатором связи

### Requirement: Надёжность синхронной границы

Каждый синхронный вызов между сервисами SHALL иметь явный таймаут, ограниченное число ретраев и согласованный с владельцем контракта ключ идемпотентности. Частичный отказ downstream-сервиса SHALL NOT приводить к потере или двойному применению команды: инициатор SHALL обрабатывать недоступность, таймаут и повтор, а повторная доставка SHALL быть идемпотентной.

#### Scenario: Таймаут и повтор
- **WHEN** downstream-сервис не отвечает в пределах таймаута
- **THEN** инициатор повторяет вызов в пределах ретраев и затем завершает операцию с наблюдаемым исходом отказа

#### Scenario: Идемпотентный повтор
- **WHEN** одна и та же команда доставляется повторно
- **THEN** владелец применяет её ровно один раз и распознаёт повтор по ключу идемпотентности

#### Scenario: Частичный отказ
- **WHEN** часть синхронных вызовов завершается ошибкой
- **THEN** инициатор сохраняет согласованное состояние и не теряет результаты успешных вызовов

### Requirement: Запрет прямого доступа к чужому хранилищу

Сервис SHALL NOT обращаться напрямую к хранилищу, схеме данных или модели другого сервиса. Доступ к состоянию чужого контекста SHALL происходить только через объявленный контракт владельца по объявленному виду связи. Агрегат, собираемый из хранилищ нескольких сервисов, запрещён.

#### Scenario: Прямой доступ к чужому хранилищу
- **WHEN** сервис читает или пишет в хранилище или схему данных чужого контекста
- **THEN** проверка сервисной границы завершается ошибкой

#### Scenario: Чужое состояние через контракт
- **WHEN** сервису нужны данные чужого контекста
- **THEN** он получает их через объявленный контракт владельца, а не прямым запросом к чужому хранилищу

### Requirement: Разделение PostgreSQL identities и операций

Provisioning SHALL использовать отдельную bootstrap/admin identity только для идемпотентного создания и настройки схем и ролей; эта identity SHALL NOT передаваться приложению. Для каждой схемы SHALL существовать отдельная `NOLOGIN` owner role, migration login role с правом миграции только этой схемы и runtime login role, не являющаяся владельцем и не способная принять owner/migration identity. Runtime grants SHALL ограничиваться своей схемой и необходимыми таблицами/sequence; межсхемное чтение и запись, DDL и запрещённые для append-only данных `UPDATE`, `DELETE`, `TRUNCATE` SHALL быть невозможны.

Перед `ApplyAs` система SHALL доказать через PostgreSQL catalogs/effective privileges, что active owner — owner только managed schema и не обладает ownership либо прямыми schema/table grants вне неё. Для migration login SHALL существовать ровно одно допустимое membership `migrator → owner` с `admin_option=false`, `SET=true`, `INHERIT=false`; отсутствие membership, дополнительные memberships или drift любого атрибута SHALL приводить к отказу до ledger/SQL mutation. Global и per-schema `pg_default_acl` owner-а SHALL аудироваться; stale ACL SHALL нормализоваться к intended runtime grants до создания объектов либо migration SHALL завершиться fail-closed. Это применяется на lifecycle `provision → ApplyAs → GrantRuntimeSchema`. Глобальный ACL `PUBLIC` SHALL NOT изменяться этим provisioning. Если `PUBLIC` даёт `CREATE` в схеме вне target, migration SHALL fail-closed только для этого target. Во всех проверках SECURITY DEFINER, ownership и external ACL системные схемы SHALL исключаться по точному имени либо literal prefix comparisons (`pg_temp_`, `pg_toast`); SQL `LIKE` и wildcard pattern matching SHALL NOT использоваться. Схема `pgXtempY` SHALL считаться обычной внешней схемой.

#### Scenario: Runtime использует только runtime identity
- **WHEN** runtime startup проверяет credentials и версию своей схемы
- **THEN** проверка выполняется read-only runtime identity, без bootstrap/migration credentials и без `Apply`

### Requirement: Совместимость G1.5 и граница включения protocol v2

Исходный G1, чей `Verify` требует exact migration set `0005–0009`, SHALL NOT объявляться совместимым с этой схемой. До включения request protocol v2 SHALL быть подготовлен и развернут immutable G1.5, сохраняющий v1 approve/rerun/direct-start и worker/activity payload, с runtime-only identities и зафиксированными image digest. `Verify` SHALL оставаться strict exact-set. Release compatibility manifest SHALL продолжать откладывать `0005–0009` до доказательства G1.5-совместимости; применение compatibility migration к пользовательской БД и production deployment не входят в подготовительную работу.

Lifecycle SHALL различать `G1_CURRENT`, `G1_5_BRIDGE_PREPARED`, `G1_5_DEPLOYED_V1_ONLY` и `V2_ADMISSION_ENABLED`. G2 v2 admission SHALL NOT включаться до состояния `G1_5_DEPLOYED_V1_ONLY` и прохождения release gate. До первой durable v2 admission/intent rollback допустим только на G1.5; после первой такой записи rollback к v1 SHALL NOT заявляться, допускаются только G2 roll-forward/recovery. Feature flag сам по себе не доказывает совместимость или отсутствие durable v2 writes. Исходный G1 privileged credentials SHALL сохраняться до отдельного retirement и явного завершения rollback window.

#### Scenario: Исходный G1 не принимает schema set 0005–0009
- **WHEN** G1 `Verify` получает schema с migration set `0005–0009`
- **THEN** exact-set проверка отвергает её; verifier не ослабляется и G1 не называется rollback target

#### Scenario: Включение v2 заблокировано без развернутого G1.5
- **WHEN** v2 admission запрошен до deployment G1.5 и release gate
- **THEN** v2 admission остаётся выключенным, исходный G1 остаётся текущим

#### Scenario: Rollback boundary после durable v2 intent
- **WHEN** уже записана первая durable v2 admission/intent
- **THEN** v1/G1.5 rollback запрещён; восстановление выполняется только G2 roll-forward/recovery

`ApplyAs` SHALL ограничивать статический и динамический DDL собственной схемой. SQL migration files SHALL полностью запрещать операторы `GRANT` и `REVOKE` (включая назначение прав `PUBLIC` и ролям); структурный whitelist не применяется. Такие migration SHALL отклоняться до любых изменений ACL, ledger или исполнения SQL. Regex scanner SHALL NOT считаться доказательством области SQL. Неизвестные, неоднозначные и неанализируемые constructs SHALL отклоняться до исполнения; effective privileges и ownership migration role также должны исключать внешние изменения. Анализ SHALL включать тела SQL/PLpgSQL `FUNCTION` и `PROCEDURE`, в том числе прямые операторы и динамический SQL через `EXECUTE`. Динамический DDL и `SECURITY DEFINER`-пути, способные выйти за target schema или повысить привилегии, SHALL отклоняться, если безопасность не доказана. При невозможности доказать безопасность система SHALL fail-closed до подключения к БД, изменения migration ledger и ACL. Доказуемо безопасные существующие функции с динамическим DDL в target schema не должны блокироваться только за определение функции или динамический SQL.

Миграции SHALL выполняться отдельным одноразовым job под migration identity. Runtime startup SHALL использовать только runtime credentials, выполнять read-only проверку применённой версии схемы и SHALL NOT применять миграции. Compose, Tofu local и Tofu bare-metal SHALL доставлять credentials через секреты конкретным process groups; конфигурация SHALL NOT передавать root/migration credential runtime-группам. Формат запуска provisioner SHALL поддерживать идемпотентный upgrade существующего persistent PostgreSQL volume. Проверка интеграции SHALL проводиться на изолированном реальном PostgreSQL с реальными ограниченными credentials; отсутствие интеграции не может заменяться тестовой заглушкой.

Для `intake` фактический app runner SHALL при каждом обычном startup (не только в CLI verify mode) вызывать `migrate.VerifyRuntimeRole` и read-only `migrate.Verify` для всех PostgreSQL dependencies. Runtime-only конфигурация SHALL отвергать bootstrap/admin и legacy/shared DSN. Intake dependency inventory: `request-fulfillment` (`request`, `email_draft`, `outbox`, `execution_attempt`, `execution_resolution`, `execution_resolution_revision`, `schema_migrations`); `consoleauth` (`console_sessions`, `schema_migrations`); vector storage `integration-platform` (`vector_document`, `schema_migrations`). Фактическая migration задаёт `vector_document.id` как `text PRIMARY KEY`, sequence отсутствует. `integration_invocation` и `integration_invocation_seq_seq` относятся к отдельной identity `integration-platform`, не vector-only intake identity. Отсутствующая/неверная роль, DSN или schema version любой зависимости SHALL блокировать startup. Отдельный текущий CLI database job mode сохраняется; runtime SHALL NOT вызывать `Apply`/`ApplyAs`.

Runtime secret reader SHALL открывать файл атомарно с `O_NOFOLLOW`; тип, owner и mode SHALL проверяться через `fstat` открытого descriptor, а чтение SHALL выполняться из того же descriptor. Для production `readSecretFile` допустимы только regular file, ожидаемый runtime owner UID и mode ровно `0400`; symlink, неверный owner, любой иной mode и небезопасный родительский путь SHALL отклоняться fail-closed. Проверка родителя не заменяет проверку descriptor. Ошибки SHALL NOT содержать secret или DSN. Эти требования SHALL NOT ослабляться ради совместимости с bind mounts.

Для Compose и Tofu общим способом доставки host secret в runtime SHALL быть B: изолированный одноразовый root staging helper копирует внешние owner-only host files в отдельные по потребителю Docker named volumes и атомарно записывает их с owner `65532:65532` и mode `0400`. Runtime SHALL монтировать только требуемые versioned volumes read-only; application process SHALL оставаться nonroot. Helper SHALL иметь только allowlisted source mounts read-only и ровно один candidate target volume read-write, SHALL NOT иметь network, Docker socket или privileged mode, SHALL использовать `CAP_DROP ALL` и только минимальные необходимые capabilities, а target allowlist SHALL быть фиксированным. Secret contents SHALL NOT передаваться через argv/ENV и SHALL NOT появляться в logs/errors/artifacts; secret values и их hash/digest SHALL NOT попадать в Terraform/OpenTofu state или attestation. Helper SHALL fail closed; incomplete output SHALL NOT активироваться и SHALL NOT изменять active volume частично. App SHALL проверять generation marker и завершать запуск fail-closed при несоответствии.

Runtime SHALL блокироваться при missing, incomplete или stale generation и SHALL NOT молча использовать старую версию или прямой bind fallback. Compose local source path SHALL оставаться configuration/service stage и SHALL NOT сам по себе считаться гарантией availability. Для Tofu SHALL применяться две фазы: первый apply создаёт candidate generation-scoped volumes и выдаёт только несекретные metadata `environment`, `deployment_id`, target, source/volume/path mapping и expected generation; runtime остаётся на `active_secret_generation`, и Tofu SHALL NOT объявлять staging успешным. Граф app module SHALL NOT содержать root staging или DB-job containers. Release job SHALL быть единственным потребителем Ed25519 release private key, доступного ему через secure file; Tofu и external verifier SHALL получать только public key/trust configuration. Подписываемый payload SHALL содержать `schemaVersion`, `purpose`, environment, deployment, target, точную Docker daemon identity, уникальные generation и release nonce, bundle file names, image digest, `issuedAt`, `expiresAt`, `keyId`; credential values и secret hashes/digests запрещены. External verifier SHALL canonicalize payload (JCS или детерминированный поддерживаемый формат), проверить подпись, текущий trust, expiry и точное совпадение всех ожидаемых полей до activation. Release job SHALL запускать на exact daemon one-shot `docker run --rm` helper, ждать реальный exit code 0 и проверять bundle identity/files/marker как UID 65532. Provision, затем migrate SHALL выполняться только после реального exit code 0 предыдущего шага. Replay ledger SHALL находиться на remote shared Docker volume и атомарно фиксировать used nonce/generation и current active sequence; локальные XDG/flock или Tofu state сами по себе SHALL NOT считаться replay protection. Remote shared-volume lock SHALL покрывать полный release+apply; local lock MAY быть только дополнительным. Crash SHALL оставлять явный in-progress, требующий reconciliation; automatic stale unlock SHALL NOT выполняться.

Вторая фаза SHALL принимать только валидную подписанную attestation и выполнять activation под активным remote lock. Attestation SHALL включать точные target/environment/deployment, daemon identity, unique generation/release nonce, bundle file names, image digest, timestamps и keyId; credential values, secret contents и secret hashes/digests запрещены. Raw generation input без проверки подписи, key, срока, полей и ledger SHALL блокировать runtime replacement. Remote ledger SHALL атомарно отклонять повторный nonce/generation и несоответствие active sequence. Неизвестный role target и более одного active candidate SHALL отклоняться. Generation в именах volumes SHALL быть namespaced по environment/deployment ID и согласована с Compose project naming. Неудачный staging SHALL оставлять active generation неизменной; previous active volume/generation SHALL сохраняться для rollback, automatic cleanup SHALL NOT выполняться, retirement — отдельная операция. Tofu partial apply recovery/rollback SHALL быть явным; система SHALL NOT заявлять атомарность partial apply или автоматически снимать stale lock. Local и bare-metal source paths SHALL разрешаться на точном Docker host. Прямой bind (A) MAY использоваться только как явно опциональный managed-Linux путь после отдельной проверки UID/GID/mode mapping. Закрытие 3.10.4.h/i требует независимых реальных cross-runner/replay failure tests, mixed-daemon/source identity tests и двухфазной activation acceptance; конфигурационная документация SHALL NOT утверждать, что live deployment или внешнее provisioning уже выполнены.

Deployment-конфигурация intake SHALL передавать `EMBEDDING_MODEL` как аргумент процесса без интерполяции в shell script или `sh -c`. Запуск профиля DB jobs SHALL NOT запускать migrations в неопределённом порядке: либо один job выбирается на invocation, либо migration job явно зависит от успешного завершения своего provision job через `service_completed_successfully`. Intake SHALL NOT наследовать зависимость от `postgres-red`, если она не нужна этому сервису. Tofu `process_extra_args` для intake SHALL ограничиваться allowlist-ом флагов/значений либо валидатором, отвергающим DSN/credential flags и значения секретов; blacklist по строковым шаблонам недостаточен. DB job args SHALL NOT содержать secret DSN values. Console password SHALL поступать только из обязательного внешнего secret file; default `admin` и raw password argv SHALL NOT использоваться. Внутри каждого Compose/Tofu контейнера target paths фиксированного config mount и secret mounts SHALL быть попарно уникальны; конфликт SHALL приводить к ошибке валидации.

Успешный аудит конфигурации SHALL описываться как code/config verification, а не live deployment, пока Compose jobs/services не запускались и Tofu не был применён.

#### Scenario: Runtime credential ограничен схемой и операциями
- **WHEN** runtime identity обращается к своей схеме и к другой схеме, либо выполняет чтение, разрешённую запись, запрещённую запись или DDL
- **THEN** доступ разрешён только в своей схеме и только для выданных операций; чужое чтение/запись, DDL и запрещённые операции append-only завершаются отказом PostgreSQL

Runtime ACL audit SHALL перечислять проверенные классы объектов и SHALL NOT заявлять аудит «всех объектов», если class coverage не полон. Для классов, не включённых в точный inventory, SHALL быть указаны гарантированные границы доступа либо fail-closed результат.

Runtime DB identity SHALL быть generation-scoped: role name детерминированно выводится из доверенного canonical scope `environment/deployment_id/target/generation` с collision-resistant SHA-256 suffix и укладывается в лимит PostgreSQL 63-byte identifier. Raw scope SHALL храниться только в candidate registry/attestation, не в идентификаторе PostgreSQL. Manifest связывает каждую DB job, staged bundle/static registry, runtime role и target-generation; caller SHALL NOT задавать arbitrary role names. Registry и signed attestation SHALL содержать согласованный target/generation/role binding, а runtime DSN username SHALL точно ему соответствовать. Runtime role SHALL быть без membership в owner/migrator/другие runtime роли; owner/migrator identities остаются управляемыми.

Provision/prepare SHALL NOT выполнять `ALTER ROLE ... PASSWORD` для существующей LOGIN identity. При повторе существующая роль и её credentials остаются неизменны; несовпадение ожидаемого пароля должно приводить к fail-closed и отказу соответствующего DSN, а не к ротации. Failed release SHALL сохранять созданную candidate role до отдельного явного retirement. Пока rollback window открыт, G1 runtime обязан продолжать успешное подключение и запросы до и после G2 provision; migration policy остаётся additive-only.

#### Scenario: Candidate runtime role определяется target и generation
- **WHEN** DB job provision-ит target/generation из проверенного manifest
- **THEN** role name детерминированно выведен из полного canonical scope, не превышает 63 байта, registry, bundle и signed attestation связывают один и тот же target/generation/role, а произвольное имя caller или несовпадение DSN username отвергается

#### Scenario: Provision retry не меняет пароль существующей роли
- **WHEN** G2 provisioning повторяется для уже созданной runtime role с другим password
- **THEN** существующий пароль не меняется, G1 credentials и запросы остаются рабочими, а DSN с новым неверным password получает отказ PostgreSQL и не проходит `VerifyRuntimeRole`

#### Scenario: Неудачный candidate сохраняет отдельную identity
- **WHEN** release G2 завершается ошибкой после provision role
- **THEN** candidate role не удаляется автоматически и G1 доступ не меняется; удаление допустимо только отдельной явной retirement-операцией

### Requirement: Подписанный candidate manifest и pre-mutation DB gate

До первой DB mutation доверенный Release runner SHALL сформировать и подписать candidate manifest, отдельный от финальной success attestation. Manifest SHALL связывать target, environment, deployment ID, daemon/ledger instance, generation/nonce, bounded registry разрешённых role names, image digest, точные hashes migration manifest и G1 compatibility policy. DB provision/migrate jobs SHALL проверить подпись доверенным public key, ledger binding и актуальность/неиспользованность generation/nonce до мутации; CLI selectors SHALL точно совпадать с подписанными значениями. Произвольные role names из CLI запрещены: роли выводятся из подписанного scope и bounded registry.

Runtime bundle/static registry SHALL содержать те же role usernames. Runtime startup SHALL верифицировать manifest против подписи и ledger, сверять selectors и требовать точного равенства DSN username подписанному role registry. Runtime RoleSet SHALL строиться для выбранной generation из verified manifest, а не из статического `RequestIntakeRoleSets`. До G2 действует `ExecutionProtocol=v1` bridge. Existing active role passwords SHALL оставаться неизменными; provision SHALL создавать только новые generation identities, сохраняя G1 credentials.

До выпуска финальной success attestation Release runner SHALL аутентифицироваться каждым candidate runtime DSN и выполнить `VerifyRuntimeRole` и read-only `Verify`. Любая ошибка SHALL блокировать финальную подпись и activation.

#### Scenario: Candidate manifest tamper или scope mismatch отклоняется до DB mutation
- **WHEN** candidate manifest изменён после подписи, подписан недоверенным ключом, имеет неверный ledger binding либо CLI target/environment/deployment/generation не совпадает с ним
- **THEN** DB job завершается отказом до любой DB mutation

#### Scenario: Role registry ограничивает идентичность и scope
- **WHEN** CLI передаёт произвольное имя роли либо runtime DSN username не совпадает с подписанным registry для выбранной generation
- **THEN** provision job или runtime startup отклоняет конфигурацию; статический role set не подменяет signed generation-specific RoleSet

#### Scenario: Неудачная аутентификация candidate DSN блокирует финальную attestation
- **WHEN** любой candidate runtime DSN не проходит PostgreSQL authentication, `VerifyRuntimeRole` или read-only `Verify`
- **THEN** Release runner не выпускает финальную success attestation и не активирует candidate; active roles/passwords, включая G1, остаются неизменными

#### Scenario: Фактический intake runner проверяет все runtime dependencies
- **WHEN** обычный app runner запускает intake с runtime-only credentials
- **THEN** до готовности он вызывает `migrate.VerifyRuntimeRole` и read-only schema `migrate.Verify` для request-fulfillment, consoleauth и vector storage dependencies; CLI verify mode не является условием этих проверок, а migration job остаётся отдельным режимом

#### Scenario: Runtime intake отвергает admin и legacy DSN
- **WHEN** runtime-only intake получает bootstrap/admin DSN, migration DSN или прежний общий/legacy DSN
- **THEN** startup завершается отказом до запуска роли и не вызывает `Apply`/`ApplyAs`; безопасное сообщение не раскрывает DSN

#### Scenario: Intake startup блокируется при неверной dependency schema
- **WHEN** отсутствует runtime DSN, `VerifyRuntimeRole` отвергает роль либо schema `Verify` находит отсутствующую/неверную версию хотя бы одной intake dependency
- **THEN** фактический app runner не становится ready

### Requirement: Независимый trust anchor и атомарный multi-target release intake

Ключ доверия для candidate manifest SHALL быть закреплён вне candidate/release state и всех файлов, создаваемых release runner. Trust anchor SHALL быть установлен оператором/deployment administrator до запуска release и смонтирован read-only в release DB jobs и runtime; candidate не SHALL иметь возможности установить, заменить или расширить trust anchor. Source files и trust files SHALL поступать из внешнего операторского/deployment lifecycle; их содержимое не включается в release state.

Runtime startup SHALL принимать только подписанный manifest, на который ссылается active durable ledger. Runtime SHALL проверять подпись доверенным внешним trust anchor, scope ключа, связь ledger/generation, role registry и полный набор версий схемы через все три candidate runtime DSN (`request`, `consoleauth`, vector). Active release-state volume SHALL монтироваться runtime только read-only. Ни manifest, лежащий сам по себе в volume, ни ссылка, не подтверждённая active ledger, не разрешают startup.

Intake release SHALL быть одним атомарным multi-target bundle: один подписанный candidate manifest связывает request, consoleauth и vector DSN/roles, migration set и единую generation. Provision/migrate DB jobs могут выполнять только свои подписанные этапы; ни один target job не может отдельно завершить release или активировать generation. После успешных миграций и проверки всех трёх candidate runtime DSN, но до активации, runner SHALL сформировать одну final attestation для всего bundle. Любая ошибка оставляет прежнюю active generation неизменной.

Перед первым candidate release SHALL быть выполнена enrollment-запись текущего G1/G1.5: фактические container/image/volume/DSN identity и явное разрешение этой исходной release state. Enrollment SHALL использовать наблюдённые значения, не догадки; state содержит только несекретную identity/ссылки, не DSN credential values или секреты. Если фактический G1 snapshot отсутствует, release SHALL остановиться до candidate stage/DB mutation.

#### Scenario: Candidate не может подменить trust anchor
- **WHEN** candidate state или release job пытается установить/заменить ключ проверки
- **THEN** проверка использует только предварительно provisioned внешний RO trust file, а подмена блокируется до DB mutation

#### Scenario: Runtime доверяет только ledger-active manifest
- **WHEN** runtime стартует с неподписанным, неподтверждённым active ledger, неправильного scope/generation, role registry либо версии схемы хотя бы одного из трёх DSN
- **THEN** startup завершается fail-closed; runtime не принимает manifest из одного лишь release volume и не может писать в active release state

#### Scenario: DB jobs не активируют target отдельно
- **WHEN** отдельные request, consoleauth или vector provision/migrate jobs завершились успешно, но общий release bundle не имеет final attestation
- **THEN** ledger не переводит generation в active и runtime остаётся на прежней active generation

#### Scenario: Общая attestation выпускается после всех проверок
- **WHEN** миграции завершены и аутентификация, `VerifyRuntimeRole` и read-only `Verify` пройдены для каждого из трёх candidate runtime DSN
- **THEN** runner выпускает одну final attestation, связывающую manifest, все три targets/roles/migration sets и одну generation; любое несоответствие блокирует её

#### Scenario: Enrollment требует фактический G1/G1.5 snapshot
- **WHEN** release начинается без явно разрешённой enrollment-записи с проверенными container/image/volume/DSN identities текущего G1/G1.5
- **THEN** release останавливается до stage/DB mutation; state не заполняется предположительными identities или секретными значениями

#### Scenario: Secret file проверяется на открытом descriptor
- **WHEN** runtime читает secret file, включая конкурентную замену пути или target
- **THEN** open использует `O_NOFOLLOW`, `fstat` проверяет тот же descriptor, чтение идёт из него; symlink, не-regular file и небезопасный mode отвергаются, ошибки не раскрывают секрет

#### Scenario: Secret staging создаёт только проверенное поколение
- **WHEN** одноразовый helper копирует owner-only host secret в allowlisted named volume
- **THEN** helper не имеет network/Docker socket/privileged mode и иных mounts; runtime получает только нужный versioned volume read-only; данные публикуются атомарно как `65532:65532`/`0400`, без secret contents в argv/ENV/logs/errors; ошибка оставляет active volume неизменным

#### Scenario: Невалидный или устаревший secret generation блокирует runtime
- **WHEN** нужный staged volume отсутствует, неполон, stale либо source имеет wrong owner/mode или является symlink
- **THEN** staging/runtime завершается fail-closed без silent fallback на старый generation или прямой bind

State volume SHALL быть уникален для environment+deployment+фактической Docker daemon identity; все runner/Tofu операции SHALL обращаться через exact same remote daemon. Единственный authorized release runner SHALL захватывать атомарный remote lock (Linux `flock`/эквивалент) до начала candidate и удерживать до завершения activation; local flock SHALL быть только supplementary. Ledger SHALL долговечно отражать `new → staging → verified → activation_in_progress → activated` либо `failed/recovery_required`, использованные generation/nonce и previous active generation. После crash состояние SHALL сохраняться; новый runner SHALL fail-closed, а resume допустим только с тем же nonce после проверки фактического Docker runtime. Никакого автоматического stale unlock, пока операции неопределённы. Generation и nonce SHALL быть globally unique для deployment и не переиспользоваться после abort/activation; precheck и atomic CAS SHALL предотвращать parallel run/replay. Tofu SHALL проверять remote ledger status/nonce и active lock до activation; прямой apply без matching active lock SHALL завершаться отказом.

Ed25519 attestation SHALL подписывать канонический детерминированный JSON с `keyId`, schema/purpose, deployment/environment, daemon identity, target, generation+nonce, bundle file names, image digest, issue/expiry. Credential values, secret contents и hashes/digests секретов SHALL NOT включаться. Private key SHALL быть доступен только release job через secure file; public key передаётся в Tofu; rotation/revocation SHALL быть явно заданы. Tofu/verifier SHALL проверять signature, разрешённый актуальный key, все поля, срок и ledger перед activation. Tofu state или unsigned JSON сами по себе SHALL NOT считаться anti-replay.

#### Scenario: Tofu использует внешний двухфазный Release lifecycle
- **WHEN** Tofu описывает candidate generation и версионированный target volume
- **THEN** первый apply создаёт candidate volumes и только несекретный release plan, сохраняя runtime на `active_secret_generation`; release job подписывает canonical payload Ed25519, а внешний verifier проверяет подпись, trust, expiry и все ожидаемые поля; второй apply активирует candidate только при валидной attestation и операции под активным remote lock, иначе runtime containers не заменяются

#### Scenario: Подписанная attestation не содержит credentials
- **WHEN** release job формирует attestation для candidate
- **THEN** payload содержит schemaVersion/purpose, environment/deployment/target, точную daemon identity, уникальные generation/release nonce, bundle file names, image digest, issuedAt/expiresAt и keyId; private key доступен только release job через secure file, а attestation/state не содержат credential values или secret hashes/digests

#### Scenario: Конкурентные runner-ы сериализуются общим удалённым lock
- **WHEN** два authorized release runner-а начинают candidate для одной environment/deployment/daemon одновременно
- **THEN** один атомарно получает lock на общем remote volume, второй fail-closed; lock удерживается до окончания activation, локальный flock не считается достаточным

#### Scenario: Durable ledger после crash и stale lock
- **WHEN** runner падает на stage/activation либо lock heartbeat истекает при неопределённых активных операциях
- **THEN** ledger сохраняет состояние, включая `failed/recovery_required`; автоматической разблокировки нет, resume разрешён только с тем же nonce после проверки фактического Docker runtime

Remote release ledger SHALL иметь единственного writer — lock-holder Unix socket server. UDS SHALL находиться в отдельном parent directory; symlink parent или socket SHALL отклоняться. При создании directory/socket и при каждом подключении клиента SHALL проверяться точные ожидаемые owner, GID и mode; любое несовпадение SHALL блокировать IPC. State, candidate и active Docker volumes SHALL иметь driver ровно `local`. Active volume SHALL иметь те же обязательные deployment/environment/target labels, что и candidate, и фактические labels SHALL совпадать с release plan. Runner ID SHALL подтверждаться фактическим Docker inspect созданного runner container и точно совпадать с ожидаемым ID; значение только из плана или аргументов недостаточно.

#### Scenario: Небезопасный ledger socket отвергается
- **WHEN** UDS parent/socket является symlink либо owner, GID или mode не совпадает при создании или client connect
- **THEN** подключение и ledger mutation отвергаются; клиент не получает путь прямой записи ledger

#### Scenario: Несовпадающие Docker volume/runner metadata отвергаются
- **WHEN** state/candidate/active volume использует driver не `local`, active labels не совпадают с candidate/plan либо inspected runner ID отличается от ожидаемого
- **THEN** release fail-closed до активации

#### Scenario: Подпись одноразова и generation не повторяется
- **WHEN** activation proof уже использован либо generation/nonce ранее aborted/activated
- **THEN** precheck/CAS удалённого ledger отклоняет replay/reuse, и Tofu не меняет runtime

#### Scenario: Fail stage не меняет active generation
- **WHEN** stage нового candidate завершается ошибкой
- **THEN** remote ledger фиксирует failure/recovery state, active generation и её volume остаются неизменными

#### Scenario: Replay и конкурирующий runner блокируются удалённым ledger/lock
- **WHEN** два runner-а работают параллельно либо повторно предъявлена использованная attestation
- **THEN** remote shared-volume lock сериализует полный release+apply, атомарный ledger отклоняет повторный nonce/generation и неверную active sequence; local lock и Tofu state не считаются достаточными

#### Scenario: Crash оставляет in-progress для явной сверки
- **WHEN** release/apply аварийно завершается при удержании remote lock
- **THEN** состояние остаётся явно in-progress и требует reconciliation; автоматического stale unlock или скрытого возобновления нет

#### Scenario: Несовпадающая daemon/source identity блокирует активацию
- **WHEN** source и candidate volume относятся к разным Docker daemon либо daemon identity не совпадает с payload/ожиданиями
- **THEN** verifier/release отказывает до activation

#### Scenario: Неудачный staging и partial apply требуют безопасного восстановления
- **WHEN** staging G2 завершается ошибкой либо Tofu apply частично применён
- **THEN** неудачный stage оставляет active G1 нетронутым; partial apply требует явного recovery/rollback и не считается атомарным

#### Scenario: Release jobs gated реальным результатом
- **WHEN** stager или отдельный DB provision/migrate Release step завершается ненулевым кодом
- **THEN** следующий шаг и activation не запускаются; фактический exit code, а не существование ресурса, определяет успех

#### Scenario: Source доступен на точном Docker host
- **WHEN** Tofu local либо bare-metal Release job stage-ит bundle
- **THEN** source path разрешается на том же Docker host, где находится target volume; runner-local path не принимается за remote daemon path без подтверждённого mapping

#### Scenario: Tofu staging не сохраняет значения секретов
- **WHEN** Tofu описывает удалённый host staging path и versioned volume lifecycle
- **THEN** state, argv, logs и artifacts не содержат secret bytes или hash; nonsecret attestation ограничена точными generation/target/environment

#### Scenario: Ротация активирует только проверенное поколение
- **WHEN** подготовлено новое поколение staged secrets
- **THEN** после успешной точной attestation второй apply активирует только matching candidate; stale или parallel generation отклоняется lock/lease, а app отвергает неверный marker

#### Scenario: Предыдущее поколение сохраняется для отката
- **WHEN** runtime активирован на новом поколении
- **THEN** прежний active volume сохраняется на rollback window; автоматическая очистка не выполняется, явный cleanup после acceptance не удаляет active volume

#### Scenario: Ограниченное окно отката G1
- **WHEN** G2 развёрнут, но оператор ещё не завершил rollback window
- **THEN** прежний привилегированный G1 DSN/user и защищённый G1 secret volume сохраняются без ротации пароля и отзыва пользователя; G1 volume доступен только legacy G1 runtime и отсутствует в Tofu state и G2; G2 использует отдельные ограниченные роли

#### Scenario: Изменение схемы во время гарантии отката
- **WHEN** rollback на G1 заявлен
- **THEN** разрешены только additive backward-compatible migrations; несовместимая миграция требует явно объявленного maintenance window и немедленно инвалидирует гарантию отката на G1

#### Scenario: Завершение rollback window и retirement G1
- **WHEN** G2 acceptance завершена и оператор явно подтверждает выход из rollback window
- **THEN** отдельная retirement-процедура удаляет G1 runtime mount/volume и отзывает старые DSN/user/secret; expiry не задаётся вымышленной календарной датой, а до завершения процедуры G1 credentials и volume остаются защищёнными и сохранёнными

#### Scenario: Прямой bind допустим только в проверенном managed Linux
- **WHEN** оператор выбирает optional bind path A
- **THEN** для конкретного managed Linux host/container проверены UID/GID/mode mapping; этот путь не применяется как общий Compose/Darwin Docker Desktop вариант

#### Scenario: Embeddings model не интерполируется в shell
- **WHEN** Compose задаёт модель embeddings для intake
- **THEN** значение передаётся приложению аргументом без shell script/`sh -c` интерполяции

#### Scenario: DB jobs выполняются в определённом порядке
- **WHEN** запускается DB lifecycle через Compose profile
- **THEN** выполняется только выбранный job за invocation либо migration job ожидает `service_completed_successfully` для соответствующего provision job; migrations не стартуют в неустановленном порядке

#### Scenario: Intake не зависит от postgres-red
- **WHEN** Compose разрешает зависимости intake
- **THEN** `postgres-red` отсутствует среди зависимостей intake

#### Scenario: Tofu аргументы и DB job args не принимают credentials
- **WHEN** Tofu получает неразрешённый extra flag, DSN, credential flag или значение секрета/DSN в DB job args
- **THEN** allowlist/валидация отклоняет конфигурацию; DB job получает секрет только через secret file mount

#### Scenario: Console password поступает только из внешнего secret file
- **WHEN** пароль консоли не задан внешним secret file либо ищется default/raw argv
- **THEN** конфигурация/startup завершается отказом; пароль `admin` не используется по умолчанию и пароль не передаётся в argv

#### Scenario: Mount targets уникальны
- **WHEN** Compose или Tofu конфигурирует фиксированный config target и secret targets одного контейнера
- **THEN** все targets уникальны, а конфликтованный target отклоняется до запуска контейнера

#### Scenario: Конфигурационный аудит не выдаётся за live deployment
- **WHEN** прошли статическая проверка Compose/Tofu и тесты, но сервисы/jobs не запускались и Tofu не применялся
- **THEN** результат фиксируется как code/config verification, не как live deployment

#### Scenario: Миграция отдельно от runtime
- **WHEN** одноразовый migration job использует migration identity, а сервис запускается runtime identity
- **THEN** migration job может применить собственные миграции, runtime может проверить версию read-only и не вызывает `Apply`; runtime identity не может выполнять миграции

#### Scenario: Bootstrap повторно на существующем volume
- **WHEN** provisioner повторно запускается на уже инициализированном PostgreSQL volume
- **THEN** роли и grants приводятся к требуемому состоянию без сброса данных и без необходимости initdb-only скрипта

#### Scenario: Owner не имеет полномочий вне managed schema
- **WHEN** `ApplyAs` начинает миграцию
- **THEN** проверяется owner role `nspowner` target и отсутствие внешнего ownership/direct schema-table grants/default ACL; обнаружение внешней authority блокирует миграцию

#### Scenario: Membership migrator-owner имеет точные атрибуты
- **WHEN** membership `migrator → owner` отсутствует, имеет `admin_option=true`, `SET=false` или `INHERIT=true`, либо у migrator есть дополнительное membership
- **THEN** `ApplyAs` отклоняет миграцию до изменения ledger и исполнения SQL

#### Scenario: PUBLIC сохраняется, внешний CREATE блокирует target
- **WHEN** `PUBLIC` имеет `CREATE` в сторонней схеме
- **THEN** глобальный ACL `PUBLIC` остаётся неизменным, а затронутая миграционная цель завершается fail-closed без глобального отзыва

#### Scenario: Stale default ACL не протекает при provisioning
- **WHEN** owner имеет stale global или per-schema `pg_default_acl` в lifecycle `provision → ApplyAs → GrantRuntimeSchema`
- **THEN** ACL аудируется и нормализуется только к intended runtime grants либо процесс fail-closed; новые объекты не получают неожиданных grants

#### Scenario: Глобальный default EXECUTE отозван только для managed owner
- **WHEN** provisioner готовит managed owner к созданию routine и затем новая routine создаётся до runtime grants
- **THEN** для owner выполняется `ALTER DEFAULT PRIVILEGES FOR ROLE <managed owner> REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC` без `IN SCHEMA`; новая routine недоступна `PUBLIC`, default privileges иных ролей и database-level ACL не меняются

#### Scenario: SQL миграции не выходит за схему
- **WHEN** SQL содержит `CREATE TABLE public.x`, динамический `CREATE` через `DO`, неподдерживаемый construct, `GRANT` на `PUBLIC`/роль либо `REVOKE`
- **THEN** миграция отклоняется до ledger/SQL/ACL mutation; реальный PostgreSQL подтверждает, что внешнее состояние не изменилось

#### Scenario: GRANT в теле функции отклоняется до изменений PostgreSQL
- **WHEN** migration SQL определяет SQL/PLpgSQL функцию или процедуру, тело которой прямо либо через динамический `EXECUTE` выполняет `GRANT`/`REVOKE`, включая `EXECUTE 'GRANT SELECT ... TO PUBLIC'`
- **THEN** миграция отклоняется до подключения к БД, ledger и ACL mutation; regression test на реальном PostgreSQL подтверждает отсутствие изменения ACL

#### Scenario: Безопасные существующие функции сохраняют совместимость
- **WHEN** migration содержит доказуемо безопасную функцию с динамическим DDL, ограниченным target schema, как в текущих request-fulfillment миграциях
- **THEN** миграция не отклоняется только из-за определения функции или динамического SQL; неизвестный или потенциально привилегированный путь остаётся fail-closed

#### Scenario: Имена системных схем проверяются без LIKE wildcard
- **WHEN** проверка SECURITY DEFINER, ownership или external ACL встречает схему `pgXtempY`
- **THEN** схема рассматривается как обычная внешняя схема, а применимый SECURITY DEFINER side effect блокируется; системные префиксы определяются literal prefix comparisons

#### Scenario: Sequence privileges проверяются отдельно
- **WHEN** runtime обращается к allowlisted `bigserial` sequence
- **THEN** только необходимые `USAGE`/`SELECT` privileges позволяют требуемые операции, а доступ к неразрешённой sequence отклоняется

#### Scenario: Первый пилот не заявляет глобальную изоляцию
- **WHEN** `request-fulfillment` переведён на раздельные runtime и migration identities, но другие PostgreSQL-контексты ещё не переведены
- **THEN** пилот принимается только как подэтап; задача полной schema-per-service остаётся открытой

### Requirement: Проверка сервисной границы инструментом

Команда `go -C tools/archlint run .` SHALL проверять сервисную границу: один сервис на контекст, отсутствие импорта чужого домена, объявленный вид связи у каждой связи и отсутствие прямого доступа к чужой схеме данных. Команда SHALL возвращать ненулевой код при любом нарушении сервисной границы.

#### Scenario: Согласованная сервисная граница
- **WHEN** каждый контекст имеет один сервис, каждая связь объявляет вид, а чужой домен и хранилище не используются напрямую
- **THEN** команда завершается кодом 0 и печатает краткую сводку

#### Scenario: Нарушение сервисной границы
- **WHEN** каталог, код или дельта нарушают границу сервиса
- **THEN** команда завершается ненулевым кодом и перечисляет нарушения за один запуск
