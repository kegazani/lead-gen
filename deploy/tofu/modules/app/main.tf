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

  name     = "${var.name_prefix}-${each.key}"
  image    = var.build ? docker_image.service[0].image_id : var.image_name
  restart  = var.restart_policy
  must_run = true

  command = concat(
    ["-config", var.config_target, "-process", each.value.name],
    var.extra_args,
  )

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

  labels {
    label = "lead-gen.process"
    value = each.key
  }
  labels {
    label = "lead-gen.mechanism"
    value = each.value.mechanism
  }
}
