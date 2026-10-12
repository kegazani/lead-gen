# deploy/tofu — инфраструктура как код (OpenTofu)

Развёртывание модульного монолита: один служебный образ (`cmd/lead-gen`) плюс
контейнеры данных и Prometheus. Инструмент — **OpenTofu** (MPL-2.0), совместимый
с Terraform; провайдер — `kreuzwerker/docker` (плюс `hashicorp/local` для рендера
конфигураций и файлов секретов).

## Единый источник истины

Список сервисов и узлов НЕ дублируется в `.tf`. Оба файла читаются в env-слое
через `yamldecode(file(...))` и передаются модулям переменными:

| Файл | Слой | Кто читает |
| --- | --- | --- |
| `deploy/services.yaml` | группы процессов (`processes` → `services` → `roles`) | `envs/*/main.tf` → переменная `processes` |
| `deploy/infrastructure.yaml` | узлы (`nodes` → `id`, `kind`, `zone`, `addresses`) | `envs/*/main.tf` → переменная `nodes` |

Модули не знают про файлы и не содержат списков сервисов или узлов: они выводят
состав из полученных данных. Добавить узел или группу процессов — значит изменить
только YAML.

Соответствие «вид узла → контейнер» задаётся в модуле `data`:

| `kind` в `infrastructure.yaml` | Контейнер | Кто обрабатывает |
| --- | --- | --- |
| `postgres` (зона `green`) | PostgreSQL | `modules/data` |
| `broker` | RabbitMQ | `modules/data` |
| `cache` | Valkey | `modules/data` |
| `workflow` | Temporal (auto-setup) | `modules/data` |
| `object-storage` | MinIO (S3-совместимое) | `modules/data` |
| `metrics` | Prometheus | `modules/observability` |
| `external` | — (внешний провайдер, не контейнер) | не разворачивается |
| `postgres` (зона `red`) | — (контур клиента, внутренний адрес) | не разворачивается |

Адрес узла из `infrastructure.yaml` (`<id>:<порт>`) задаёт и сетевой псевдоним
(он же `<id>`), и внутренний порт контейнера. Портов наружу никто не публикует:
адреса узлов внутренние.

## Модули

```
deploy/tofu/
  modules/
    network/        минимальная сеть: docker-сеть; DNS — встроенный резолвер Docker
    data/           PostgreSQL, Valkey, RabbitMQ, Temporal, MinIO (состав — из YAML)
    app/            контейнеры служебного образа по группам процессов (из YAML)
    observability/  Prometheus (+ пометка про OpenTelemetry)
    secrets/        контракт секретов: значения не в состоянии и не в репозитории
  envs/
    local/          docker host — unix-сокет, состояние локальное
    bare-metal/     docker host — по SSH, состояние во внешнем backend (GitLab HTTP)
```

## Окружения

### `envs/local` — локальная разработка

* Docker host: `unix:///var/run/docker.sock` (`var.docker_host`); release runner
  требует явный `DOCKER_HOST=unix:///var/run/docker.sock` и тот же daemon.
* Состояние: локальный backend, файл `terraform.tfstate` в каталоге окружения
  (в репозиторий не попадает — см. `.gitignore`).
* Образ служебного образа **собирается** из корневого `Dockerfile` (`build_image = true`).
* Файлы секретов готовит SOPS+age в каталоге `deploy/tofu/.secrets/` (gitignored);
  OpenTofu значения не пишет.
* Конфигурация Prometheus рендерится в `deploy/tofu/.generated/` (gitignored).

### `envs/bare-metal` — боевой хост

* Docker host: `var.docker_host`, по умолчанию `ssh://deploy@localhost`; release
  runner требует явный `DOCKER_HOST` с тем же адресом.
* Состояние: backend `http` (GitLab-managed), **вне репозитория**. Адрес и доступ
  не зашиты в конфигурацию — приходят переменными окружения `TF_HTTP_ADDRESS`,
  `TF_HTTP_LOCK_ADDRESS`, `TF_HTTP_UNLOCK_ADDRESS`, `TF_HTTP_USERNAME`,
  `TF_HTTP_PASSWORD` либо через `init -backend-config=<файл вне репозитория>`.
* Образ служебного образа по умолчанию **берётся готовым** из реестра
  (`build_image = false`).
* Файлы секретов и `prometheus.yml` лежат НА docker-хосте и готовятся внешним
  механизмом; OpenTofu не пишет секретные значения. Tofu lifecycle двухфазный:
  phase 1 создаёт candidate volumes и только несекретный plan; phase 2 активирует
  runtime исключительно по attestation release runner-а. Tofu-managed root stage и
  DB-job containers отсутствуют. External runner использует точный `DOCKER_HOST`,
  `docker run --rm`, реальные exit-code gates, проверку nonroot и lock на deployment.
  Secret values/hash не становятся outputs, Tofu state, argv, logs или artifacts.
  Старые volumes остаются для rollback; автоматическая очистка отсутствует.

* `deploy/services.yaml` должен существовать на docker-хосте по пути
  `var.app_config_source` (по умолчанию `/etc/lead-gen/services.yaml`): bind-mount
  указывает на путь удалённого хоста.
* Образ S3-хранилища берётся из `quay.io` (`quay.io/minio/minio`): образ в Docker
  Hub `minio/minio` больше не публикуется.

## Секреты

Значения секретов НЕ хранятся в репозитории и НЕ попадают в состояние; удалённый
staging path и generation selector сами по себе не содержат secret values:

* переменные объявлены с `sensitive = true`, **без значений по умолчанию** и с
  проверкой непустоты — незаданный источник виден на планировании, а не молчит;
* источник — окружение (`TF_VAR_postgres_password`, `TF_VAR_rabbitmq_password`,
  `TF_VAR_minio_root_password`) или **SOPS + age**;
* модуль `secrets` — это контракт, а не хранилище: он НЕ создаёт ресурсов,
  принимающих значение, и отдаёт наружу только пути к файлам. Так сделано после
  проверки: у `local_file.sensitive_content` значение, вопреки описанию,
  сохраняется в состоянии (а `local_sensitive_file` кладёт его туда по
  определению), поэтому любой ресурс с секретом — это секрет в состоянии;
* файлы на docker-хосте готовит SOPS+age, а контейнеры получают не значение,
  а путь к файлу. Доставка по образам подобрана по факту их поведения:

### Staging и ротация intake secret volumes

`active_secret_generation` — поколение runtime; phase 1 задаёт отдельный
`candidate_secret_generation`, не переключая active. `secret_generations` содержит
все поколения, volumes которых надо сохранить, включая прежние после успешной
ротации. Candidate должен быть новым и отсутствовать в этом наборе. Tofu output
`release_plan` содержит только host paths, bundle targets, volume names, generation
и image/network metadata — он не означает успешный stage. Перед candidate release
оператор закрепляет `docker_daemon_id` по `docker info --format '{{.ID}}'` для того же
`DOCKER_HOST`; plan содержит также фактические network ID и candidate volume IDs,
drivers и labels. Runner fail-closed при любом несовпадении.

Во время G1 rollback window generation прежнего привилегированного пользователя
задаётся в `external_secret_generations` и остаётся вне Tofu volume resource graph и
OpenTofu state; runtime продолжает монтировать его по закреплённому volume name.
Эти поколения должны одновременно оставаться в `secret_generations`. Candidate
release требует, чтобы текущий active G1 был external. Если существующий state уже
отслеживает G1 volume, не запускайте apply: нужен отдельный проверенный перенос
state reference без удаления volume. Эта процедура и live credentials вне данного
code/config acceptance. Runner также требует фактические `local` driver и точные G1
labels/volume ID при runtime reconciliation; он не меняет metadata старого volume
автоматически и блокирует release, если G1 не размечен ожидаемой identity.

Внешняя последовательность: `tofu apply` phase 1 → export `tofu output -json
release_plan` в защищённый от изменения plan file → `DOCKER_HOST=... deploy/scripts/
release-db-jobs.sh --plan ... --source-dir ... --helper-image ... --target intake
--attestation ...` → проверить sanitized attestation → отдельный `tofu apply` с теми
же candidate, `verified_secret_generation` и `attestation_path`. `verified_secret_generation`
как произвольное значение без attestation не принимается. Для DB lifecycle runner
запускается с `--db-target <context> --attestation-dir <path>`: stage/verify и
attestation provision bundle → provision с реальным exit check → stage/verify и
attestation migration bundle → migrate только после успешного provision; запуск
jobs через Tofu запрещён.

При `build_image = false` до phase 1 задайте `image_digest` — config digest
заранее загруженного образа, снятый с того же daemon командой
`docker --host "$DOCKER_HOST" image inspect <image-ref> --format '{{.Id}}'`.
Release plan не принимает mutable tag как подтверждение версии helper image; runner
сверяет фактический image ID перед первым запуском.

Runner сериализует работу одного environment/deployment ID удалённым volume
lock-holder/ledger с Unix-socket single-writer CAS и отклоняет повторное generation.
Image должен быть full `sha256:` + 64 lowercase hex digest. Source root в CLI обязан
точно совпасть с release plan; runner предварительно проверяет allowlisted source
metadata без чтения secret bytes. Source path и named volume
должны разрешаться тем же Docker daemon; у remote Docker host путь из runner container
или CI workspace не считается путём хоста автоматически. `DOCKER_HOST` обязателен,
default context не используется. На local/bare-metal оператор сначала проверяет
remote path и daemon; живой apply не запускается без внешних реальных secret files.
Старые и неудачные candidate volumes не чистятся автоматически; retirement — отдельная
операторская операция после rollback window. Не запускайте `docker volume prune`.

`prepare` намеренно оставляет lock-holder работающим. `activate` пока fail-closed:
подпись сама по себе не блокирует `tofu apply`; Tofu pre-apply gate, runtime verification
и ledger activation commit будут отдельным изменением. Stale holder можно обработать
только явным `recover` после проверки, что он остановлен, и сверки фактического active
runtime с plan/ledger; heartbeat expiry не освобождает lock.

Ошибка staging блокирует выбранное поколение; старое не подключается автоматически.
Исходные секретные файлы должны быть regular, принадлежать владельцу source
directory, иметь mode `0400` или `0600`, без group/world permissions и symlink.
Source directory монтируется только в staging container read-only. Provision- и
migration-bundles раздельны и не попадают в runtime containers.

| Узел | Как читает секрет |
| --- | --- |
| PostgreSQL | родная `POSTGRES_PASSWORD_FILE` |
| MinIO (object-storage) | родная `MINIO_ROOT_PASSWORD_FILE` |
| RabbitMQ | `rabbitmq.conf` собирается в контейнере при старте из файла (образы отклоняют `RABBITMQ_DEFAULT_PASS_FILE` как устаревшую переменную) |
| Temporal | обёртка экспортирует `POSTGRES_PWD` из файла перед запуском (`auto-setup` читает переменную, а не файл) |
| Valkey (cache) | без аутентификации |

Пример подготовки файлов (обе среды; вне OpenTofu):

```sh
mkdir -p deploy/tofu/.secrets
sops exec-env deploy/tofu/secrets.enc.yaml '\
  printf %s "$RABBITMQ_PASSWORD" > deploy/tofu/.secrets/rabbitmq-password; \
  ...'
```

Резервный режим `use_file_secrets = false` передаёт значения как `env` — тогда они
попадут в состояние. Это осознанный выбор оператора, а не умолчание; backend
состояния должен быть защищён независимо (изоляция доступа, шифрование,
ограниченный срок жизни токена).

## Явно вне объёма

* **DNS** отдельным сервисом не вводится: в контейнерной сети работает встроенный
  резолвер Docker (127.0.0.11), имена узлов — сетевые псевдонимы.
* **TLS** не настраивается: сертификаты, терминация и ACME — отдельная задача.
* Внешние адреса, балансировщики и публикация портов наружу не рассматриваются.

## Команды

```sh
# Проверка без состояния и без доступа к docker:
tofu -chdir=deploy/tofu/envs/local init -backend=false
tofu -chdir=deploy/tofu/envs/local validate
tofu -chdir=deploy/tofu/envs/bare-metal init -backend=false
tofu -chdir=deploy/tofu/envs/bare-metal validate

# Форматирование:
tofu fmt -check -recursive deploy/tofu

# Локальное применение (нужны секреты в окружении):
export TF_VAR_postgres_password=...
export TF_VAR_rabbitmq_password=...
export TF_VAR_minio_root_password=...
tofu -chdir=deploy/tofu/envs/local apply
```

## Отношение к остальной документации

Этот каталог — только развёртывание. Доменные правила, состав сервисов и каталог
контекстов живут в `architecture/` и `docs/`; `deploy/README.md` описывает формат
`services.yaml`. Форма развёртывания выводится из `services.yaml`, и правка
группировки не меняет ни код, ни доменную модель.
