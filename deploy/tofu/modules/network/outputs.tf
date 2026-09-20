output "name" {
  description = "Имя созданной docker-сети."
  value       = docker_network.this.name
}

output "id" {
  description = "Идентификатор созданной docker-сети."
  value       = docker_network.this.id
}
