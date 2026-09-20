variable "processes" {
  description = "Группы процессов из deploy/services.yaml (единый источник истины). Приходят из env-слоя."
  type = list(object({
    name      = string
    mechanism = string
    services = list(object({
      id    = string
      roles = list(string)
    }))
  }))
}


variable "name_prefix" {
  description = "Префикс имён контейнеров."
  type        = string
  default     = "lead-gen"
}

variable "network_name" {
  description = "Имя docker-сети, в которую подключаются процессы."
  type        = string
}

variable "restart_policy" {
  description = "Политика перезапуска контейнеров."
  type        = string
  default     = "unless-stopped"
}

variable "build" {
  description = "Собирать образ из корневого Dockerfile. false — брать готовый image_name."
  type        = bool
  default     = true
}

variable "build_context" {
  description = "Каталог сборки (корень репозитория). Обязателен при build = true."
  type        = string
  default     = "."
}

variable "dockerfile" {
  description = "Путь к Dockerfile относительно build_context."
  type        = string
  default     = "Dockerfile"
}

variable "build_target" {
  description = "Целевая ступень сборки (пусто — последняя)."
  type        = string
  default     = ""
}

variable "build_args" {
  description = "Аргументы сборки."
  type        = map(string)
  default     = {}
}

variable "image_name" {
  description = "Тег образа (при build = true также имя собираемого образа)."
  type        = string
  default     = "lead-gen-service:local"
}

variable "config_source" {
  description = "Путь к deploy/services.yaml на docker-хосте."
  type        = string
}

variable "config_target" {
  description = "Путь к конфигурации внутри контейнера."
  type        = string
  default     = "/etc/lead-gen/services.yaml"
}

variable "extra_args" {
  description = "Дополнительные аргументы точки входа (например, -addr)."
  type        = list(string)
  default     = []
}
