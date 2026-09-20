output "containers" {
  description = "Идентификатор узла -> имя контейнера. Узлы, не управляемые модулем, сюда не входят."
  value       = { for id, container in docker_container.data : id => container.name }
}

output "endpoints" {
  description = "Идентификатор узла -> внутренний адрес `host:port` на docker-сети."
  value       = { for id, node in local.managed : id => "${node.id}:${local.node_port[id]}" }
}

output "volumes" {
  description = "Идентификатор узла -> имя именованного тома данных."
  value       = { for id, volume in docker_volume.data : id => volume.name }
}
