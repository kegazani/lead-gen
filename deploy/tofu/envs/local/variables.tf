variable "docker_host" {
  description = "Адрес docker-хоста. Локальное окружение — unix-сокет."
  type        = string
  default     = "unix:///var/run/docker.sock"
}

variable "docker_daemon_id" {
  description = "Закреплённый точный Engine /info.ID для release plan; получить и зафиксировать оператором."
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
  default     = "lead-gen-local"
}

variable "image_name" {
  description = "Тег служебного образа."
  type        = string
  default     = "lead-gen-service:local"
}

variable "image_digest" {
  description = "Ожидаемый image config digest при использовании предварительно загруженного образа."
  type        = string
  default     = ""
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
  default     = "local"

  validation {
    condition     = can(regex("^[a-z0-9][a-z0-9_-]{0,31}$", var.environment))
    error_message = "environment должен быть безопасным идентификатором Docker."
  }
}

variable "deployment_id" {
  description = "Уникальный ID deployment, часть имени secret volumes."
  type        = string
  default     = "local"
  validation {
    condition     = can(regex("^[a-z0-9][a-z0-9_-]{0,31}$", var.deployment_id))
    error_message = "deployment_id должен быть безопасным идентификатором Docker."
  }
}

variable "active_secret_generation" {
  description = "Active generation, остаётся неизменным в первой фазе ротации."
  type        = string
  default     = "local-initial"
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
  default     = ["local-initial"]
}

variable "external_secret_generations" {
  description = "Поколения старого G1 rollback-window, которые остаются вне OpenTofu state."
  type        = set(string)
  default     = []
}

variable "config_dir" {
  description = "Каталог рендера конфигурации. Пусто — deploy/tofu/.generated."
  type        = string
  default     = ""
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
