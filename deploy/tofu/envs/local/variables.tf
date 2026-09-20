variable "docker_host" {
  description = "Адрес docker-хоста. Локальное окружение — unix-сокет."
  type        = string
  default     = "unix:///var/run/docker.sock"
}

variable "name_prefix" {
  description = "Префикс имён контейнеров, томов и сети."
  type        = string
  default     = "lead-gen"
}

variable "network_name" {
  description = "Имя docker-сети."
  type        = string
  default     = "lead-gen-local"
}

variable "image_name" {
  description = "Тег служебного образа."
  type        = string
  default     = "lead-gen-service:local"
}

variable "build_image" {
  description = "Собирать служебный образ из корневого Dockerfile."
  type        = bool
  default     = true
}

variable "build_context" {
  description = "Каталог сборки. Пусто — корень репозитория."
  type        = string
  default     = ""
}

variable "secret_dir" {
  description = "Каталог файлов секретов. Пусто — deploy/tofu/.secrets."
  type        = string
  default     = ""
}

variable "config_dir" {
  description = "Каталог рендера конфигурации. Пусто — deploy/tofu/.generated."
  type        = string
  default     = ""
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
