# Модуль служебного образа: один образ — один или несколько процессов.
#
# Состав групп процессов НЕ дублируется: он приходит переменной `processes`,
# которую env-слой читает из `deploy/services.yaml` через `yamldecode(file(...))`.
# На каждую группу поднимается один контейнер того же образа с флагом
# `-process <name>`: единица развёртывания — группа из конфигурации, а не из кода.
#
# Образ собирается из корневого `Dockerfile` (build = true) либо берётся готовым
# по `image_name` (build = false) — например, после публикации в реестр.

terraform {
  required_providers {
    docker = {
      source = "kreuzwerker/docker"
    }
  }
}

locals {
  process_groups = { for process in var.processes : process.name => process }
  secret_groups  = { for group, files in var.process_secret_files : group => group if length(files) > 0 }
  group_generation = {
    for group in keys(local.process_groups) : group => (
      group == var.activation_target && var.verified_secret_generation != ""
      ? var.verified_secret_generation
      : lookup(var.active_secret_generations, group, var.active_secret_generation)
    )
  }
  retained_generations = setunion(var.secret_generations, toset(compact(concat([var.active_secret_generation, var.candidate_secret_generation], values(var.active_secret_generations)))))
  managed_generations  = setsubtract(local.retained_generations, var.external_secret_generations)
  runtime_secret_versions = {
    for pair in setproduct(keys(local.secret_groups), local.managed_generations) : "${pair[0]}-${pair[1]}" => { group = pair[0], generation = pair[1] }
  }
  db_secret_versions = {
    for pair in setproduct(keys(var.db_jobs), local.managed_generations) : "${pair[0]}-${pair[1]}" => { job = pair[0], generation = pair[1] }
  }
}

resource "terraform_data" "intake_activation_gate" {
  count = var.verified_secret_generation == "" ? 0 : 1

  depends_on = [docker_volume.release_trust_anchor]

  input = {
    target      = var.activation_target
    generation  = var.verified_secret_generation
    attestation = var.attestation_path
    digest      = filesha256(var.attestation_path)
  }

  triggers_replace = [
    var.verified_secret_generation,
    var.candidate_secret_generation,
    var.active_secret_generation,
    filesha256(var.attestation_path),
    var.release_state_volume,
    var.release_trust_volume,
  ]

  provisioner "local-exec" {
    command = "python3 ${path.module}/../../../scripts/tofu-activation-gate.py"
    environment = {
      LEADGEN_ACTIVATION_DOCKER_HOST  = var.docker_host
      LEADGEN_ACTIVATION_STATE_VOLUME = var.release_state_volume
      LEADGEN_ACTIVATION_TRUST_VOLUME = var.release_trust_volume
      LEADGEN_ACTIVATION_ATTESTATION  = var.attestation_path
      LEADGEN_ACTIVATION_IMAGE        = var.build ? docker_image.service[0].image_id : var.prebuilt_image_digest
      LEADGEN_RELEASE_TRUST_KEY_ID    = var.release_trust_key_id
    }
  }

  lifecycle {
    precondition {
      condition     = var.activation_target == "intake" && var.verified_secret_generation == var.candidate_secret_generation
      error_message = "Phase 2 допускается только для точного intake candidate generation."
    }
    precondition {
      condition     = var.release_state_volume != "" && var.release_trust_volume != "" && var.attestation_path != "" && var.release_trust_key_id != ""
      error_message = "Apply-time gate требует staged trust volume, release-state volume, attestation и key ID."
    }
  }
}

resource "docker_volume" "runtime_secret_bundle" {
  for_each = local.runtime_secret_versions

  name = "${var.volume_project}-secrets-${each.value.group}-${each.value.generation}"

  labels {
    label = "lead-gen.release.environment"
    value = var.environment
  }
  labels {
    label = "lead-gen.release.deployment"
    value = var.deployment_id
  }
  labels {
    label = "lead-gen.release.target"
    value = each.value.group
  }
  labels {
    label = "lead-gen.release.generation"
    value = each.value.generation
  }
  labels {
    label = "lead-gen.release.daemon"
    value = var.docker_daemon_id
  }
}

resource "docker_volume" "db_secret_bundle" {
  for_each = local.db_secret_versions

  name = "${var.volume_project}-secrets-${each.value.job}-${each.value.generation}"

  labels {
    label = "lead-gen.release.environment"
    value = var.environment
  }
  labels {
    label = "lead-gen.release.deployment"
    value = var.deployment_id
  }
  labels {
    label = "lead-gen.release.target"
    value = each.value.job
  }
  labels {
    label = "lead-gen.release.generation"
    value = each.value.generation
  }
  labels {
    label = "lead-gen.release.daemon"
    value = var.docker_daemon_id
  }
}

resource "docker_volume" "release_trust_anchor" {
  count = var.release_trust_volume == "" ? 0 : 1

  name = var.release_trust_volume

  labels {
    label = "lead-gen.release.environment"
    value = var.environment
  }
  labels {
    label = "lead-gen.release.deployment"
    value = var.deployment_id
  }
  labels {
    label = "lead-gen.release.trust-key-id"
    value = var.release_trust_key_id
  }
}

resource "docker_image" "service" {
  count = var.build ? 1 : 0

  name = var.image_name

  build {
    context    = var.build_context
    dockerfile = var.dockerfile
    target     = var.build_target
    build_args = var.build_args
  }

  # Пересборка при изменении Dockerfile. Хэш считается только при build = true.
  triggers = var.build ? { dockerfile = filesha256("${var.build_context}/${var.dockerfile}") } : {}
}

resource "docker_container" "service" {
  for_each = local.process_groups

  depends_on = [terraform_data.intake_activation_gate]

  name     = "${var.name_prefix}-${each.key}"
  image    = var.build ? docker_image.service[0].image_id : var.image_name
  restart  = var.restart_policy
  must_run = true
  user     = "65532:65532"

  # `-liveness-addr` задаётся явно, чтобы адрес liveness-слушателя не зависел от
  # дефолта точки входа: healthcheck ниже ходит именно на `127.0.0.1:8091/livez`.
  # `-metrics-addr :8092` задаёт процесс-уровневый слушатель /metrics, который
  # скрейпит Prometheus (цели `:8092` в env-слое), — он отличен от api-роли (:8080).
  command = ["-config", var.config_target, "-process", each.value.name, "-liveness-addr", "127.0.0.1:8091", "-metrics-addr", ":8092"]

  env = concat(["LEADGEN_SECRET_GENERATION=${local.group_generation[each.key]}"], [
    for name, value in lookup(var.process_env, each.key, {}) : "${name}=${value}"
  ])

  networks_advanced {
    name    = var.network_name
    aliases = ["${var.name_prefix}-${each.key}"]
  }

  # Конфигурация развёртывания монтируется только на чтение: она остаётся
  # единственным источником истины и не копируется в образ.
  mounts {
    source    = var.config_source
    target    = var.config_target
    type      = "bind"
    read_only = true
  }

  dynamic "mounts" {
    for_each = contains(keys(local.secret_groups), each.key) ? ["${var.volume_project}-secrets-${each.key}-${local.group_generation[each.key]}"] : []
    content {
      source    = mounts.value
      target    = "/run/secrets"
      type      = "volume"
      read_only = true
    }
  }

  dynamic "mounts" {
    for_each = each.key == "intake" && var.release_state_volume != "" ? [var.release_state_volume] : []
    content {
      source    = mounts.value
      target    = "/run/leadgen-release"
      type      = "volume"
      read_only = true
    }
  }

  dynamic "mounts" {
    for_each = each.key == "intake" && var.release_trust_volume != "" ? [var.release_trust_volume] : []
    content {
      source    = mounts.value
      target    = "/run/leadgen-trust"
      type      = "volume"
      read_only = true
    }
  }

  lifecycle {
    precondition {
      condition     = each.key != "intake" || (var.release_trust_volume != "" && var.release_trust_key_id != "")
      error_message = "Intake runtime требует обязательные staged trust volume и key ID."
    }
    precondition {
      condition     = length(local.process_groups) == length(var.processes)
      error_message = "В processes обнаружены повторяющиеся имена групп."
    }
    precondition {
      condition     = var.verified_secret_generation == "" || (var.activation_target == "intake" && var.verified_secret_generation == var.candidate_secret_generation)
      error_message = "Активация допускается только для точного intake candidate; криптографическое доказательство повторно проверяется apply-time gate."
    }
    precondition {
      condition     = var.candidate_secret_generation == "" || var.candidate_secret_generation == lookup(var.active_secret_generations, var.activation_target, var.active_secret_generation) || !contains(var.secret_generations, var.candidate_secret_generation)
      error_message = "Candidate generation пуста, активна или уже сохранена; повторное использование запрещено."
    }
    precondition {
      condition     = var.verified_secret_generation == "" || (var.verified_secret_generation == var.candidate_secret_generation && var.attestation_path != "")
      error_message = "verified_secret_generation должен точно совпадать с candidate и иметь путь к attestation."
    }
    precondition {
      condition     = var.candidate_secret_generation == "" || var.build || can(regex("^sha256:[a-f0-9]{64}$", var.prebuilt_image_digest))
      error_message = "Для candidate release требуется image digest собранного или заранее загруженного helper image."
    }
    precondition {
      condition     = var.candidate_secret_generation == "" || (var.docker_daemon_id != "" && var.network_id != "")
      error_message = "Для candidate release закрепите Docker Engine ID и фактический network ID."
    }
    precondition {
      condition     = var.candidate_secret_generation == "" || contains(var.external_secret_generations, var.active_secret_generation)
      error_message = "G1 active secret volume должен оставаться внешним и не входить в OpenTofu state в rollback window."
    }
    precondition {
      condition     = length(setintersection(var.external_secret_generations, toset(compact([var.candidate_secret_generation])))) == 0
      error_message = "Candidate generation нельзя объявлять внешним G1 volume."
    }
  }

  labels {
    label = "lead-gen.process"
    value = each.key
  }
  labels {
    label = "lead-gen.mechanism"
    value = each.value.mechanism
  }

  # Liveness-проба уровня процесса (design.md, Решение 3): проба — вызов того же
  # статического бинарника в режиме -healthcheck (distroless без shell/curl),
  # отдельно от /readyz (readiness). Проба проверяет только «процесс жив и
  # отвечает», не здоровье отдельных ролей. Зависший процесс помечается unhealthy;
  # сам контейнер пробой не перезапускается (`restart` реагирует только на выход
  # процесса) — автовосстановление выполняет процессный сторож самозавершением
  # (design.md, Решение 6). Адрес `127.0.0.1:8091/livez` связан с явным
  # `-liveness-addr 127.0.0.1:8091` в `command` выше.
  healthcheck {
    test         = ["CMD", "/usr/local/bin/lead-gen", "-healthcheck", "-healthcheck-url", "http://127.0.0.1:8091/livez"]
    interval     = var.healthcheck_interval
    timeout      = var.healthcheck_timeout
    retries      = var.healthcheck_retries
    start_period = var.healthcheck_start_period
  }
}
