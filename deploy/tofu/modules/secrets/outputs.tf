output "secret_dir" {
  description = "Каталог файлов секретов на docker-хосте (монтируется в контейнеры)."
  value       = var.secret_dir
}

output "file_names" {
  description = "Имена файлов секретов, которые обязаны существовать. Значений здесь нет."
  value       = local.secret_names
}

output "files" {
  description = "Имя секрета -> путь к файлу на docker-хосте. Значений здесь нет."
  value       = { for name in local.secret_names : name => "${var.secret_dir}/${name}" }
}
