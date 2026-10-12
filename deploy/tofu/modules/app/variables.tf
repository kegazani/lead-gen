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

variable "secret_source_dir" {
  description = "Каталог host file references, доступный только внешнему release runner; Tofu его не монтирует."
  type        = string
}

variable "release_state_volume" {
  description = "Имя externally managed active release-state volume для intake; пустое значение оставляет runtime без полномочий на запуск."
  type        = string
  default     = ""
}

variable "release_trust_volume" {
  description = "Имя отдельного named volume со staged внешним Ed25519 trust anchor."
  type        = string
  default     = ""
}

variable "release_trust_key_id" {
  description = "Key ID независимого Ed25519 trust anchor, используемый verifier-ом при phase 2."
  type        = string
  default     = ""

  validation {
    condition     = var.release_trust_key_id == "" || can(regex("^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$", var.release_trust_key_id))
    error_message = "release_trust_key_id должен быть безопасным key ID."
  }
}

variable "docker_host" {
  description = "Точный Docker endpoint провайдера; release runner обязан использовать тот же endpoint через DOCKER_HOST."
  type        = string
}

variable "docker_daemon_id" {
  description = "Закреплённый Docker Engine /info.ID; release runner сверяет точное совпадение до volume операций."
  type        = string
  default     = ""
}

variable "environment" {
  description = "Идентификатор окружения, включаемый в имена candidate и active volumes."
  type        = string

  validation {
    condition     = can(regex("^[a-z0-9][a-z0-9_-]{0,31}$", var.environment))
    error_message = "environment должен быть безопасным идентификатором Docker."
  }
}

variable "deployment_id" {
  description = "Уникальный ID деплоя, включаемый в имя volume и attestation."
  type        = string

  validation {
    condition     = can(regex("^[a-z0-9][a-z0-9_-]{0,31}$", var.deployment_id))
    error_message = "deployment_id должен быть безопасным идентификатором Docker."
  }
}

variable "active_secret_generation" {
  description = "Текущее active поколение; phase 1 никогда не меняет его."
  type        = string
}

variable "active_secret_generations" {
  description = "Per-process-group active generations; an absent entry inherits active_secret_generation."
  type        = map(string)
  default     = {}

  validation {
    condition = alltrue([
      for group, generation in var.active_secret_generations : contains(["core", "demand", "intake", "platform"], group) && can(regex("^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$", generation))
    ])
    error_message = "active_secret_generations содержит неизвестную группу или некорректное generation."
  }
}

variable "candidate_secret_generation" {
  description = "Новое уникальное поколение для внешнего stage; пусто, когда candidate не готовится."
  type        = string
  default     = ""

  validation {
    condition     = var.candidate_secret_generation == "" || can(regex("^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$", var.candidate_secret_generation))
    error_message = "candidate_secret_generation должен быть непустым безопасным идентификатором либо пустым."
  }
}

variable "verified_secret_generation" {
  description = "Generation из проверенной external attestation; пусто оставляет runtime на active generation."
  type        = string
  default     = ""

  validation {
    condition     = var.verified_secret_generation == "" || can(regex("^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$", var.verified_secret_generation))
    error_message = "verified_secret_generation должен быть безопасным generation ID."
  }
}

variable "attestation_path" {
  description = "Путь к sanitized JSON attestation, доступной Tofu runner-у только на фазе активации."
  type        = string
  default     = ""
}

variable "activation_target" {
  description = "Allowlisted bundle target, которому относится attestation; intake runtime target — intake."
  type        = string
  default     = "intake"

  validation {
    condition     = contains(["core", "demand", "intake", "platform"], var.activation_target)
    error_message = "Неизвестная роль/bundle target для activation."
  }
}

variable "secret_generations" {
  description = "Ранее подтверждённые поколения, чьи volumes сохраняются для rollback; очистка отдельная."
  type        = set(string)

  validation {
    condition = alltrue([
      for generation in var.secret_generations : can(regex("^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$", generation))
    ]) && contains(var.secret_generations, var.active_secret_generation)
    error_message = "secret_generations должны быть корректными и включать active_secret_generation."
  }
}

variable "external_secret_generations" {
  description = "Secret generations retained outside OpenTofu state during the G1 rollback window."
  type        = set(string)
  default     = []

  validation {
    condition = alltrue([
      for generation in var.external_secret_generations : can(regex("^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$", generation)) && contains(var.secret_generations, generation)
    ])
    error_message = "external_secret_generations должны быть безопасными generation IDs из secret_generations."
  }
}

variable "volume_project" {
  description = "Compose project-compatible name prefix including environment and deployment ID."
  type        = string
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

variable "network_id" {
  description = "Фактический ID созданной Docker network, закреплённый в release plan."
  type        = string
  default     = ""
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

variable "prebuilt_image_digest" {
  description = "Image config digest для заранее загруженного release helper image."
  type        = string
  default     = ""
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

variable "process_env" {
  description = "Дополнительное окружение по имени группы процессов. Не передавайте значения секретов: для них используйте process_secret_files."
  type        = map(map(string))
  default     = {}

  validation {
    condition = alltrue([
      for group in keys(var.process_env) : contains([for process in var.processes : process.name], group)
    ])
    error_message = "process_env содержит неизвестную группу процессов."
  }

  validation {
    condition = alltrue(flatten([
      for environment in values(var.process_env) : [
        for name, value in environment : (
          !can(regex("(?i)(dsn|password|secret|token|api[_-]?key)", name)) ||
          contains([
            "LEADGEN_REQUEST_RUNTIME_DSN_FILE=/run/secrets/${var.verified_secret_generation != "" ? var.verified_secret_generation : var.active_secret_generation}/request-runtime-dsn",
            "LEADGEN_CONSOLEAUTH_RUNTIME_DSN_FILE=/run/secrets/${var.verified_secret_generation != "" ? var.verified_secret_generation : var.active_secret_generation}/consoleauth-runtime-dsn",
            "LEADGEN_VECTOR_RUNTIME_DSN_FILE=/run/secrets/${var.verified_secret_generation != "" ? var.verified_secret_generation : var.active_secret_generation}/vector-runtime-dsn",
            "CONSOLE_PASSWORD_FILE=/run/secrets/${var.verified_secret_generation != "" ? var.verified_secret_generation : var.active_secret_generation}/console-password",
          ], "${name}=${value}")
        )
      ]
    ]))
    error_message = "process_env не должен содержать имена credential-переменных; используйте secret file mounts."
  }
}

variable "process_secret_files" {
  description = "Bind mounts секретных файлов по группе: ключ — путь внутри контейнера, значение — путь на Docker-хосте."
  type        = map(map(string))
  default     = {}

  validation {
    condition = alltrue([
      for group in keys(var.process_secret_files) : contains([for process in var.processes : process.name], group) && contains(["core", "demand", "intake", "platform"], group)
    ])
    error_message = "process_secret_files содержит неизвестную или не разрешённую staging группу."
  }

  validation {
    condition = alltrue(flatten([
      for files in values(var.process_secret_files) : [
        for target, source in files : startswith(source, "/") && dirname(source) == var.secret_source_dir && basename(source) == lookup({
          "/run/secrets/${var.verified_secret_generation != "" ? var.verified_secret_generation : var.active_secret_generation}/request-runtime-dsn"     = "intake-request-runtime-dsn",
          "/run/secrets/${var.verified_secret_generation != "" ? var.verified_secret_generation : var.active_secret_generation}/consoleauth-runtime-dsn" = "intake-consoleauth-runtime-dsn",
          "/run/secrets/${var.verified_secret_generation != "" ? var.verified_secret_generation : var.active_secret_generation}/vector-runtime-dsn"      = "intake-vector-runtime-dsn",
          "/run/secrets/${var.verified_secret_generation != "" ? var.verified_secret_generation : var.active_secret_generation}/console-password"        = "console-password",
          }, target, "") && contains([
          "/run/secrets/${var.verified_secret_generation != "" ? var.verified_secret_generation : var.active_secret_generation}/request-runtime-dsn",
          "/run/secrets/${var.verified_secret_generation != "" ? var.verified_secret_generation : var.active_secret_generation}/consoleauth-runtime-dsn",
          "/run/secrets/${var.verified_secret_generation != "" ? var.verified_secret_generation : var.active_secret_generation}/vector-runtime-dsn",
          "/run/secrets/${var.verified_secret_generation != "" ? var.verified_secret_generation : var.active_secret_generation}/console-password",
        ], target) && target != var.config_target
      ]
    ]))
    error_message = "Runtime secret mount имеет неизвестный target, совпадает с config mount или содержит не абсолютный host path."
  }
}

variable "db_jobs" {
  description = "Явно описанные одноразовые DB jobs: operation/target allowlist и именованные secret file mounts; CLI flags формируются модулем."
  type = map(object({
    operation    = string
    target       = string
    secret_files = map(string)
  }))
  default = {}

  validation {
    condition = alltrue([
      for job in values(var.db_jobs) : contains(["provision", "migrate"], job.operation) && contains(["request-fulfillment", "consoleauth", "integration-platform"], job.target)
    ])
    error_message = "DB job должен использовать поддерживаемые operation и allowlisted target."
  }

  validation {
    condition = alltrue([
      for job_name, job in var.db_jobs : (
        alltrue([for source in values(job.secret_files) : startswith(source, "/") && dirname(source) == var.secret_source_dir && contains([
          "request-bootstrap-dsn", "request-migration-dsn", "request-migrator-password", "request-runtime-password",
          "consoleauth-bootstrap-dsn", "consoleauth-migration-dsn", "consoleauth-migrator-password", "consoleauth-runtime-password",
          "integration-platform-bootstrap-dsn", "integration-platform-migration-dsn", "integration-platform-migrator-password",
          "integration-platform-runtime-password", "intake-vector-runtime-password",
        ], basename(source))]) &&
        (job.operation == "migrate" ? toset(keys(job.secret_files)) == toset(["/run/secrets/migration-dsn"]) :
          job.target == "integration-platform" ? toset(keys(job.secret_files)) == toset([
            "/run/secrets/bootstrap-dsn",
            "/run/secrets/migrator-password",
            "/run/secrets/runtime-password",
            "/run/secrets/additional-runtime-password",
            ]) : toset(keys(job.secret_files)) == toset([
            "/run/secrets/bootstrap-dsn",
            "/run/secrets/migrator-password",
            "/run/secrets/runtime-password",
          ])
        )
      )
    ])
    error_message = "DB job принимает только фиксированный набор secret mount targets; DSN/пароли передаются только путями к файлам."
  }
}

# Параметры liveness-пробы контейнера (healthcheck уровня оркестратора, отдельно
# от /readyz). Проба выполняется статическим бинарником `lead-gen -healthcheck`,
# поэтому в distroless-образе shell/curl не нужны. Значения по умолчанию подобраны
# так, чтобы медленный старт (миграции, подключение к БД/рантайму) не считался
# зависанием (design.md, Решение 3).

variable "healthcheck_interval" {
  description = "Интервал между liveness-пробами контейнера."
  type        = string
  default     = "10s"
}

variable "healthcheck_timeout" {
  description = "Таймаут одной liveness-пробы; зависший процесс считается нездоровым."
  type        = string
  default     = "5s"
}

variable "healthcheck_retries" {
  description = "Число неудачных проб до перевода контейнера в unhealthy."
  type        = number
  default     = 3
}

variable "healthcheck_start_period" {
  description = "Пауза перед первой пробой — допуск на медленный старт процесса."
  type        = string
  default     = "30s"
}
