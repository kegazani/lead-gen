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

variable "name_prefix" {
  description = "Префикс имён контейнеров и томов."
  type        = string
  default     = "lead-gen"
}

variable "network_name" {
  description = "Имя docker-сети."
  type        = string
}

variable "restart_policy" {
  description = "Политика перезапуска контейнеров."
  type        = string
  default     = "unless-stopped"
}

variable "prometheus_image" {
  description = "Образ Prometheus."
  type        = string
  default     = "prom/prometheus:v3.1.0"
}

variable "retention" {
  description = "Срок хранения данных Prometheus (флаг retention.time)."
  type        = string
  default     = "15d"
}

variable "scrape_targets" {
  description = "Адреса `host:port` на docker-сети для джобы lead-gen-app."
  type        = list(string)
  default     = []
}

variable "enable_otlp_receiver" {
  description = "Включить приёмник OTLP/HTTP Prometheus (пометка про OpenTelemetry)."
  type        = bool
  default     = true
}

variable "render_config" {
  description = "Рендерить prometheus.yml этим запуском. false — конфигурация готова на хосте (config_source)."
  type        = bool
  default     = true
}

variable "config_dir" {
  description = "Каталог для рендера prometheus.yml на docker-хосте. Обязателен при render_config = true."
  type        = string
  default     = ""

  validation {
    condition     = !var.render_config || var.config_dir != ""
    error_message = "config_dir обязателен, когда render_config = true."
  }
}

variable "config_source" {
  description = "Путь к готовому prometheus.yml на docker-хосте (при render_config = false)."
  type        = string
  default     = ""
}
