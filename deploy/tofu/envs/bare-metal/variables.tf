variable "docker_host" {
  description = "Адрес docker-хоста по SSH, например ssh://deploy@node-1.example.com."
  type        = string
  default     = "ssh://deploy@localhost"
}

variable "docker_daemon_id" {
  description = "Закреплённый точный Engine /info.ID удалённого daemon для release plan."
  type        = string
  default     = ""
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

variable "image_digest" {
  description = "Image config digest заранее загруженного immutable release image."
  type        = string
  default     = ""
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

variable "release_state_volume" {
  description = "Externally managed active release-state volume name; no default volume is guessed."
  type        = string
  default     = ""
}

variable "release_trust_key_id" {
  description = "Operator-provisioned release trust key ID."
  type        = string
  default     = ""
}

variable "release_trust_volume" {
  description = "Отдельный named volume для staged external release trust anchor."
  type        = string
  default     = ""

  validation {
    condition     = var.release_trust_volume == "" || can(regex("^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$", var.release_trust_volume))
    error_message = "release_trust_volume должен быть корректным Docker volume name."
  }
}

variable "environment" {
  description = "Идентификатор окружения, часть имени secret volumes."
  type        = string
  default     = "bare-metal"

  validation {
    condition     = can(regex("^[a-z0-9][a-z0-9_-]{0,31}$", var.environment))
    error_message = "environment должен быть безопасным идентификатором Docker."
  }
}

variable "deployment_id" {
  description = "Уникальный ID deployment, часть имени secret volumes."
  type        = string
  default     = "bare-metal"
  validation {
    condition     = can(regex("^[a-z0-9][a-z0-9_-]{0,31}$", var.deployment_id))
    error_message = "deployment_id должен быть безопасным идентификатором Docker."
  }
}

variable "active_secret_generation" {
  description = "Active generation, остаётся неизменным в первой фазе ротации."
  type        = string
  default     = "bare-metal-initial"
  validation {
    condition     = can(regex("^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$", var.active_secret_generation))
    error_message = "active_secret_generation должен быть непустым безопасным ID."
  }
}

variable "active_secret_generations" {
  description = "Per-process-group active generations; absent groups inherit active_secret_generation."
  type        = map(string)
  default     = {}
}

variable "candidate_secret_generation" {
  description = "Новое generation для phase 1; пусто вне ротации."
  type        = string
  default     = ""
}

variable "verified_secret_generation" {
  description = "Candidate generation для phase 2; обязательно подтверждается attestation."
  type        = string
  default     = ""
}

variable "attestation_path" {
  description = "Путь к sanitized external release attestation для phase 2."
  type        = string
  default     = ""
}

variable "activation_target" {
  description = "Allowlisted target в release attestation."
  type        = string
  default     = "intake"
}

variable "secret_generations" {
  description = "Ранее подтверждённые поколения, сохраняемые для rollback; автоочистка запрещена."
  type        = set(string)
  default     = ["bare-metal-initial"]
}

variable "external_secret_generations" {
  description = "Поколения старого G1 rollback-window, которые остаются вне OpenTofu state."
  type        = set(string)
  default     = []
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

variable "process_env" {
  description = "Нечувствительные дополнительные ENV по группе процессов. Значения секретов здесь запрещены."
  type        = map(map(string))
  default     = {}
}

variable "process_extra_args" {
  description = "Устаревший интерфейс CLI args отключён: передача аргументов по группам не поддерживается."
  type        = map(list(string))
  default     = {}

  validation {
    condition     = alltrue([for args in values(var.process_extra_args) : length(args) == 0])
    error_message = "Произвольные process_extra_args запрещены; runtime flags формируются модулем из allowlist."
  }
}

variable "process_secret_files" {
  description = "Дополнительные секретные файлы: группа → путь в контейнере → путь на Docker-хосте."
  type        = map(map(string))
  default     = {}
}

variable "db_jobs" {
  description = "Одноразовые DB jobs; operation/target/paths only, без значений credentials."
  type        = map(any)
  default     = {}

  validation {
    condition = alltrue([
      for job in values(var.db_jobs) : can(job.operation) && can(job.target) && can(job.secret_files) && alltrue([
        for key in keys(job) : contains(["operation", "target", "secret_files"], key)
      ])
    ])
    error_message = "DB jobs принимают только operation, target и secret_files; произвольные args/credentials запрещены."
  }
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
