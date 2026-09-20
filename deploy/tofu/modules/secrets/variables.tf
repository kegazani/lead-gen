variable "postgres_password" {
  description = "Пароль PostgreSQL (зелёная зона). Источник: TF_VAR_* или SOPS+age."
  type        = string
  sensitive   = true

  validation {
    condition     = length(var.postgres_password) > 0
    error_message = "postgres_password пуст: источник секрета не задан."
  }
}

variable "rabbitmq_password" {
  description = "Пароль пользователя RabbitMQ по умолчанию. Источник: TF_VAR_* или SOPS+age."
  type        = string
  sensitive   = true

  validation {
    condition     = length(var.rabbitmq_password) > 0
    error_message = "rabbitmq_password пуст: источник секрета не задан."
  }
}

variable "minio_root_password" {
  description = "Пароль root S3-совместимого хранилища (MinIO). Источник: TF_VAR_* или SOPS+age."
  type        = string
  sensitive   = true

  validation {
    condition     = length(var.minio_root_password) > 0
    error_message = "minio_root_password пуст: источник секрета не задан."
  }
}

variable "secret_dir" {
  description = "Каталог файлов секретов НА docker-хосте. Готовит внешний механизм (SOPS+age), OpenTofu значения не пишет."
  type        = string

  validation {
    condition     = var.secret_dir != ""
    error_message = "secret_dir обязателен: нужно знать, откуда контейнеры читают секреты."
  }
}
