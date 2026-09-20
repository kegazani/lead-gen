# Модуль данных: PostgreSQL (зелёная зона), Valkey, RabbitMQ, Temporal и
# S3-совместимое хранилище (MinIO).
#
# Состав узлов НЕ дублируется: он приходит переменной `nodes`, которую env-слой
# читает из `deploy/infrastructure.yaml` через `yamldecode(file(...))`. Модуль
# выводит из этого списка управляемые узлы по `kind`:
#   postgres       -> PostgreSQL
#   broker         -> RabbitMQ
#   cache          -> Valkey
#   workflow       -> Temporal
#   object-storage -> MinIO
# Узлы прочих видов остаются другим модулям: `metrics` — observability,
# `external` — вообще не контейнер (внешний провайдер). Узел красной зоны
# (`zone: red`, контур клиента) не поднимается здесь: его адрес внутренний и
# модулю недоступен.
#
# Адрес узла в `infrastructure.yaml` (`<id>:<port>`) задаёт и сетевой псевдоним
# (он же `<id>`), и внутренний порт контейнера. Публикации портов наружу нет.

terraform {
  required_providers {
    docker = {
      source = "kreuzwerker/docker"
    }
  }
}

locals {
  # Единый источник истины — переданный список узлов. Фильтр по видам и зонам,
  # а не второй список сервисов.
  managed = {
    for node in var.nodes : node.id => node
    if contains(var.managed_kinds, node.kind) && node.zone != "red"
  }

  # Вид узла -> образ. Соответствие виду, а не узлу: узлы приходят из YAML.
  image_for_kind = {
    postgres       = var.postgres_image
    broker         = var.rabbitmq_image
    cache          = var.valkey_image
    workflow       = var.temporal_image
    object-storage = var.minio_image
  }

  # Каталог данных для именованного тома. Вида нет в карте — том не создаётся.
  data_dir_for_kind = {
    postgres       = "/var/lib/postgresql/data"
    broker         = "/var/lib/rabbitmq"
    cache          = "/data"
    object-storage = "/data"
  }

  # Внутренний порт по умолчанию, если адрес узла порт не задаёт.
  default_port_for_kind = {
    postgres       = 5432
    broker         = 5672
    cache          = 6379
    workflow       = 7233
    object-storage = 9000
  }

  # Точка входа и команда запуска по виду узла. Там, где образ не читает секрет
  # из файла сам, нужна обёртка: она читает смонтированный файл ВНУТРИ контейнера,
  # поэтому в конфигурацию и состояние попадает только путь к файлу.
  entrypoint_for_kind = var.use_file_secrets ? {
    broker   = ["/bin/sh", "-c"]
    workflow = ["/bin/sh", "-c"]
  } : {}

  command_for_kind = merge(
    {
      object-storage = ["server", "/data", "--console-address", ":9001"]
    },
    var.use_file_secrets ? {
      # RabbitMQ отклоняет RABBITMQ_DEFAULT_PASS_FILE как устаревшую переменную:
      # конфигурация (пользователь и пароль) собирается из файла секрета при старте.
      broker = [
        "printf 'default_user = ${var.rabbitmq_user}\\ndefault_pass = ' > /etc/rabbitmq/rabbitmq.conf; cat /run/secrets/rabbitmq-password >> /etc/rabbitmq/rabbitmq.conf; exec docker-entrypoint.sh rabbitmq-server"
      ]
      # temporalio/auto-setup читает пароль только из POSTGRES_PWD: экспортируем
      # его из файла перед штатным запуском образа (аргумент `autosetup`).
      workflow = [
        "export POSTGRES_PWD=\"$(cat /run/secrets/postgres-password)\"; exec /etc/temporal/entrypoint.sh autosetup"
      ]
    } : {},
  )

  # Порт: из первого адреса узла вида `host:port`, иначе по виду узла.
  node_port = {
    for id, node in local.managed : id => coalesce(
      try(tonumber(split(":", node.addresses[0])[1]), null),
      local.default_port_for_kind[node.kind]
    )
  }

  # Узел PostgreSQL зелёной зоны — база для Temporal.
  postgres_ids  = sort([for id, node in local.managed : id if node.kind == "postgres"])
  postgres_host = length(local.postgres_ids) > 0 ? local.postgres_ids[0] : "postgres-green"
  postgres_port = length(local.postgres_ids) > 0 ? local.node_port[local.postgres_ids[0]] : 5432

  # Файлы секретов, подготовленные модулем `secrets`, монтируются в /run/secrets.
  secret_file_for_kind = {
    postgres       = "postgres-password"
    broker         = "rabbitmq-password"
    workflow       = "postgres-password"
    object-storage = "minio-root-password"
  }

  # Доставка секрета по виду узла. В файловом режиме значение остаётся в файле;
  # `*_FILE` используют только образы с родной поддержкой (PostgreSQL, MinIO),
  # остальные читают файл через обёртку (broker, workflow) — у них здесь пусто.
  secret_file_env = {
    postgres       = ["POSTGRES_PASSWORD_FILE=/run/secrets/${local.secret_file_for_kind["postgres"]}"]
    object-storage = ["MINIO_ROOT_PASSWORD_FILE=/run/secrets/${local.secret_file_for_kind["object-storage"]}"]
    broker         = []
    cache          = []
    workflow       = []
  }

  # Резервный режим: значения идут переменными и оседают в состоянии.
  secret_value_env = {
    postgres       = ["POSTGRES_PASSWORD=${var.postgres_password}"]
    object-storage = ["MINIO_ROOT_PASSWORD=${var.minio_root_password}"]
    broker         = ["RABBITMQ_DEFAULT_PASS=${var.rabbitmq_password}"]
    cache          = []
    workflow       = ["POSTGRES_PWD=${var.postgres_password}"]
  }

  password_env = {
    for id, node in local.managed :
    id => var.use_file_secrets ? local.secret_file_env[node.kind] : local.secret_value_env[node.kind]
  }

  # Имена и адреса, секретами не являющиеся. Пользователь RabbitMQ в файловом
  # режиме приходит из rabbitmq.conf (его собирает обёртка), поэтому здесь он
  # остаётся только в резервном режиме.
  identity_env_for_kind = {
    postgres = ["POSTGRES_USER=${var.postgres_user}", "POSTGRES_DB=${var.postgres_db}"]
    broker   = var.use_file_secrets ? [] : ["RABBITMQ_DEFAULT_USER=${var.rabbitmq_user}"]
    cache    = []
    workflow = [
      "DB=postgres12",
      "DB_PORT=${local.postgres_port}",
      "POSTGRES_SEEDS=${local.postgres_host}",
      "POSTGRES_USER=${var.postgres_user}",
      "DBNAME=temporal",
    ]
    object-storage = ["MINIO_ROOT_USER=${var.minio_root_user}"]
  }

  node_env = {
    for id, node in local.managed :
    id => concat(local.identity_env_for_kind[node.kind], local.password_env[id])
  }

  secret_mounts = {
    for id, node in local.managed : id => (
      var.use_file_secrets && contains(keys(local.secret_file_for_kind), node.kind) ? [{
        source = "${var.secret_dir}/${local.secret_file_for_kind[node.kind]}"
        target = "/run/secrets/${local.secret_file_for_kind[node.kind]}"
      }] : []
    )
  }
}

resource "docker_volume" "data" {
  for_each = {
    for id, node in local.managed : id => node
    if contains(keys(local.data_dir_for_kind), node.kind)
  }

  name = "${var.name_prefix}-${each.key}"

  labels {
    label = "lead-gen.node"
    value = each.key
  }
  labels {
    label = "lead-gen.kind"
    value = each.value.kind
  }
}

resource "docker_container" "data" {
  for_each = local.managed

  name       = "${var.name_prefix}-${each.key}"
  image      = local.image_for_kind[each.value.kind]
  entrypoint = try(local.entrypoint_for_kind[each.value.kind], null)
  command    = try(local.command_for_kind[each.value.kind], null)
  restart    = var.restart_policy
  must_run   = true

  # env наследует чувствительность значений только в режиме use_file_secrets =
  # false; при работе через файлы здесь остаются лишь пути к ним.
  env = local.node_env[each.key]

  networks_advanced {
    name    = var.network_name
    aliases = [each.value.id]
  }

  dynamic "volumes" {
    for_each = contains(keys(local.data_dir_for_kind), each.value.kind) ? [local.data_dir_for_kind[each.value.kind]] : []
    content {
      volume_name    = docker_volume.data[each.key].name
      container_path = volumes.value
    }
  }

  dynamic "mounts" {
    for_each = local.secret_mounts[each.key]
    content {
      source    = mounts.value.source
      target    = mounts.value.target
      type      = "bind"
      read_only = true
    }
  }

  labels {
    label = "lead-gen.node"
    value = each.key
  }
  labels {
    label = "lead-gen.kind"
    value = each.value.kind
  }
  labels {
    label = "lead-gen.zone"
    value = each.value.zone
  }
  labels {
    label = "lead-gen.label"
    value = each.value.label
  }
}
