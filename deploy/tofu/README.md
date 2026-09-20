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

* Docker host: `unix:///var/run/docker.sock` (`var.docker_host`).
* Состояние: локальный backend, файл `terraform.tfstate` в каталоге окружения
  (в репозиторий не попадает — см. `.gitignore`).
* Образ служебного образа **собирается** из корневого `Dockerfile` (`build_image = true`).
* Файлы секретов готовит SOPS+age в каталоге `deploy/tofu/.secrets/` (gitignored);
  OpenTofu значения не пишет.
* Конфигурация Prometheus рендерится в `deploy/tofu/.generated/` (gitignored).

### `envs/bare-metal` — боевой хост

* Docker host: `var.docker_host`, по умолчанию `ssh://deploy@localhost`.
* Состояние: backend `http` (GitLab-managed), **вне репозитория**. Адрес и доступ
  не зашиты в конфигурацию — приходят переменными окружения `TF_HTTP_ADDRESS`,
  `TF_HTTP_LOCK_ADDRESS`, `TF_HTTP_UNLOCK_ADDRESS`, `TF_HTTP_USERNAME`,
  `TF_HTTP_PASSWORD` либо через `init -backend-config=<файл вне репозитория>`.
* Образ служебного образа по умолчанию **берётся готовым** из реестра
  (`build_image = false`).
* Файлы секретов и `prometheus.yml` лежат НА docker-хосте и готовятся внешним
  механизмом (SOPS+age); OpenTofu их не пишет.
* `deploy/services.yaml` должен существовать на docker-хосте по пути
  `var.app_config_source` (по умолчанию `/etc/lead-gen/services.yaml`): bind-mount
  указывает на путь удалённого хоста.
* Образ S3-хранилища берётся из `quay.io` (`quay.io/minio/minio`): образ в Docker
  Hub `minio/minio` больше не публикуется.

## Секреты

Значения секретов НЕ хранятся в репозитории и НЕ попадают в состояние:

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
