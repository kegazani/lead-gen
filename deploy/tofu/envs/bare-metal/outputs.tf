output "network" {
  description = "Имя docker-сети окружения."
  value       = module.network.name
}

output "docker_host" {
  description = "Docker-хост окружения (без учётных данных)."
  value       = var.docker_host
}

output "data_containers" {
  description = "Идентификатор узла -> контейнер данных."
  value       = module.data.containers
}

output "data_endpoints" {
  description = "Идентификатор узла -> внутренний адрес на docker-сети."
  value       = module.data.endpoints
}

output "app_containers" {
  description = "Группа процессов -> контейнер служебного образа."
  value       = module.app.containers
}

output "app_image" {
  description = "Образ, использованный процессами."
  value       = module.app.image
}

output "release_plan" {
  description = "Несекретный candidate plan для внешнего release runner; не является подтверждением stage."
  value       = module.app.release_plan
}

output "observability_containers" {
  description = "Идентификатор узла метрик -> контейнер."
  value       = module.observability.containers
}

output "secret_dir" {
  description = "Каталог файлов секретов на docker-хосте (значений здесь нет)."
  value       = module.secrets.secret_dir
}
