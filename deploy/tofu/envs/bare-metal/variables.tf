variable "docker_host" {
  description = "Адрес docker-хоста по SSH, например ssh://deploy@node-1.example.com."
  type        = string
  default     = "ssh://deploy@localhost"
}

variable "name_prefix" {
  description = "Префикс имён контейнеров, томов и сети."
  type        = string
  default     = "lead-gen"
}

variable "network_name" {
  description = "Имя docker-сети."
  type        = string
  default     = "lead-gen"
}

variable "image_name" {
  description = "Готовый образ служебного образа из реестра."
  type        = string
  default     = "registry.example.com/lead-gen/service:latest"
}

variable "build_image" {
  description = "Собирать образ на удалённом docker-хосте. false — брать готовый из реестра."
  type        = bool
  default     = false
}

variable "build_context" {
  description = "Каталог сборки. Пусто — корень репозитория (только при build_image = true)."
  type        = string
  default     = ""
}

variable "secret_dir" {
  description = "Каталог файлов секретов НА docker-хосте. Готовит внешний механизм (SOPS+age)."
  type        = string
  default     = "/etc/lead-gen/secrets"
}

variable "config_dir" {
  description = "Каталог конфигурации НА docker-хосте (prometheus.yml и т.п.)."
  type        = string
  default     = "/etc/lead-gen/prometheus"
}

variable "app_config_source" {
  description = "Путь к deploy/services.yaml НА docker-хосте."
  type        = string
  default     = "/etc/lead-gen/services.yaml"
}

variable "postgres_password" {
  description = "Пароль PostgreSQL. Источник — окружение (TF_VAR_postgres_password) или SOPS+age."
  type        = string
  sensitive   = true
}

variable "rabbitmq_password" {
  description = "Пароль RabbitMQ. Источник — окружение или SOPS+age."
  type        = string
  sensitive   = true
}

variable "minio_root_password" {
  description = "Пароль root S3-хранилища. Источник — окружение или SOPS+age."
  type        = string
  sensitive   = true
}
