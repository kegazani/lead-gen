# Модуль сети/DNS.
#
# DNS отдельным сервисом НЕ вводится: в контейнерной сети Docker имя контейнера
# (и сетевой псевдоним) разрешается встроенным резолвером Docker (127.0.0.11).
# Поэтому модуль создаёт только сеть, а адреса узлов из `deploy/infrastructure.yaml`
# ложатся на неё сетевыми псевдонимами (aliases) — см. модуль `data`.
#
# Внешний DNS, публикация портов наружу и TLS в объём не входят: адреса узлов
# внутренние, наружу не публикуются.

terraform {
  required_providers {
    docker = {
      source = "kreuzwerker/docker"
    }
  }
}

resource "docker_network" "this" {
  name     = var.name
  driver   = var.driver
  internal = var.internal

  dynamic "labels" {
    for_each = var.labels
    content {
      label = labels.key
      value = labels.value
    }
  }
}
