variable "nodes" {
  description = "Узлы из deploy/infrastructure.yaml (единый источник истины). Приходят из env-слоя."
  type = list(object({
    id        = string
    kind      = string
    label     = string
    zone      = string
    addresses = list(string)
  }))
}

variable "managed_kinds" {
  description = "Виды узлов, поднимаемые контейнерами данных. Прочие виды обрабатывают другие модули."
  type        = list(string)
  default     = ["postgres", "broker", "cache", "workflow", "object-storage"]
}

variable "name_prefix" {
  description = "Префикс имён контейнеров и томов."
  type        = string
  default     = "lead-gen"
}

variable "network_name" {
  description = "Имя docker-сети, в которую подключаются узлы данных."
  type        = string
}

variable "restart_policy" {
  description = "Политика перезапуска контейнеров."
  type        = string
  default     = "unless-stopped"
}

variable "postgres_image" {
  description = "Образ PostgreSQL зелёной зоны."
  type        = string
  default     = "postgres:17-alpine"
}

variable "rabbitmq_image" {
  description = "Образ RabbitMQ (с management-плагином)."
  type        = string
  default     = "rabbitmq:4-management"
}

variable "valkey_image" {
  description = "Образ Valkey."
  type        = string
  default     = "valkey/valkey:8-alpine"
}

variable "temporal_image" {
  description = "Образ Temporal (auto-setup: поднимает схему в PostgreSQL)."
  type        = string
  default     = "temporalio/auto-setup:1.25"
}

variable "minio_image" {
  description = "Образ S3-совместимого хранилища. Образ живёт в quay.io; тег стоит закрепить к конкретному релизу."
  type        = string
  default     = "quay.io/minio/minio:latest"
}

variable "postgres_user" {
  description = "Пользователь PostgreSQL (не секрет)."
  type        = string
  default     = "lead_gen"
}

variable "postgres_db" {
  description = "База PostgreSQL по умолчанию (не секрет)."
  type        = string
  default     = "lead_gen"
}

variable "rabbitmq_user" {
  description = "Пользователь RabbitMQ по умолчанию (не секрет)."
  type        = string
  default     = "lead_gen"
}

variable "minio_root_user" {
  description = "Пользователь root S3-хранилища (не секрет)."
  type        = string
  default     = "lead-gen-admin"
}

variable "secret_dir" {
  description = "Каталог файлов секретов на docker-хосте (выход модуля secrets)."
  type        = string
  default     = ""
}

variable "use_file_secrets" {
  description = "Читать секреты из файлов (state-safe). false передаёт значения как env — они попадут в состояние."
  type        = bool
  default     = true
}

variable "postgres_password" {
  description = "Пароль PostgreSQL. Используется только при use_file_secrets = false."
  type        = string
  sensitive   = true
  default     = null
}

variable "rabbitmq_password" {
  description = "Пароль RabbitMQ. Используется только при use_file_secrets = false."
  type        = string
  sensitive   = true
  default     = null
}

variable "minio_root_password" {
  description = "Пароль root S3-хранилища. Используется только при use_file_secrets = false."
  type        = string
  sensitive   = true
  default     = null
}
