# Модуль наблюдаемости: Prometheus.
#
# Узел берётся из `deploy/infrastructure.yaml` по виду `metrics` — состав не
# дублируется. Конфигурация Prometheus рендерится из шаблона модуля; список
# целей задаёт env-слой (адреса процессов и узлов на docker-сети).
#
# OpenTelemetry. Приёмник OTLP включается флагом `--web.enable-otlp-receiver`
# (переменная `enable_otlp_receiver`), поэтому метрики по OTLP/HTTP можно слать
# прямо в Prometheus на `/api/v1/otlp/v1/metrics`. Коллектор OpenTelemetry НЕ
# разворачивается: это отдельная ступень, её место — здесь, когда трассы и логи
# понадобятся как первоклассные данные. Пока приложениям достаточно задать
# `OTEL_EXPORTER_OTLP_ENDPOINT` на адрес Prometheus.
#
# TLS и внешний DNS не входят в объём: порт 9090 внутренний, наружу не публикуется.

terraform {
  required_providers {
    docker = {
      source = "kreuzwerker/docker"
    }
    local = {
      source = "hashicorp/local"
    }
  }
}

locals {
  metrics_nodes = { for node in var.nodes : node.id => node if node.kind == "metrics" }

  prometheus_args = concat(
    [
      "--config.file=/etc/prometheus/prometheus.yml",
      "--storage.tsdb.path=/prometheus",
      "--storage.tsdb.retention.time=${var.retention}",
    ],
    var.enable_otlp_receiver ? ["--web.enable-otlp-receiver"] : [],
  )

  config_file = var.render_config ? local_file.prometheus_config[0].filename : var.config_source
}

resource "local_file" "prometheus_config" {
  count = var.render_config ? 1 : 0

  filename = "${var.config_dir}/prometheus.yml"
  content = templatefile("${path.module}/prometheus.yml.tftpl", {
    stack   = var.name_prefix
    targets = var.scrape_targets
  })
}

resource "docker_volume" "prometheus" {
  for_each = local.metrics_nodes

  name = "${var.name_prefix}-${each.key}-data"

  labels {
    label = "lead-gen.node"
    value = each.key
  }
}

resource "docker_container" "prometheus" {
  for_each = local.metrics_nodes

  name     = "${var.name_prefix}-${each.key}"
  image    = var.prometheus_image
  command  = local.prometheus_args
  restart  = var.restart_policy
  must_run = true

  networks_advanced {
    name    = var.network_name
    aliases = [each.value.id]
  }

  mounts {
    source    = local.config_file
    target    = "/etc/prometheus/prometheus.yml"
    type      = "bind"
    read_only = true
  }

  volumes {
    volume_name    = docker_volume.prometheus[each.key].name
    container_path = "/prometheus"
  }

  labels {
    label = "lead-gen.node"
    value = each.key
  }
}
