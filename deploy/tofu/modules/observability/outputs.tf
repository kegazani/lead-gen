output "containers" {
  description = "Идентификатор узла метрик -> имя контейнера."
  value       = { for id, container in docker_container.prometheus : id => container.name }
}

output "config_file" {
  description = "Путь к prometheus.yml на docker-хосте."
  value       = var.render_config ? local_file.prometheus_config[0].filename : var.config_source
}

output "otlp_receiver_enabled" {
  description = "Включён ли приёмник OTLP/HTTP. Коллектор OpenTelemetry отдельно не разворачивается."
  value       = var.enable_otlp_receiver
}
