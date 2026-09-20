output "containers" {
  description = "Имя группы процессов -> имя контейнера."
  value       = { for name, container in docker_container.service : name => container.name }
}

output "image" {
  description = "Ссылка на образ, использованный контейнерами."
  value       = var.build ? docker_image.service[0].image_id : var.image_name
}
