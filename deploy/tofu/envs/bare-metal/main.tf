# Окружение bare-metal: docker-хост по SSH, состояние во внешнем backend.
#
# Единый источник истины тот же — deploy/services.yaml и deploy/infrastructure.yaml.
# Отличие от local в трёх местах:
#   * docker-хост задаётся переменной (SSH), а не локальным сокетом;
#   * файлы секретов и конфигурации лежат НА docker-хосте и готовятся внешним
#     механизмом (SOPS+age) — модуль секретов их не рендерит, значение в
#     состояние не попадает;
#   * образ, как правило, берётся готовым из реестра (build_image = false).

locals {
  repo_root  = abspath("${path.root}/../../../..")
  deploy_dir = abspath("${path.root}/../../..")

  build_context = var.build_context != "" ? var.build_context : local.repo_root

  services       = yamldecode(file("${local.deploy_dir}/services.yaml"))
  infrastructure = yamldecode(file("${local.deploy_dir}/infrastructure.yaml"))

  nodes = [
    for node in local.infrastructure.nodes : {
      id        = node.id
      kind      = node.kind
      label     = try(node.label, "")
      zone      = try(node.zone, "")
      addresses = try(node.addresses, [])
    }
  ]

  processes = [
    for process in local.services.processes : {
      name      = process.name
      mechanism = try(process.mechanism, "")
      services = [
        for service in process.services : {
          id    = service.id
          roles = try(service.roles, [])
        }
      ]
    }
  ]

  # Цели Prometheus: процесс-уровневый слушатель /metrics (-metrics-addr) слушает
  # :8092 внутри контейнера; скрейпится именно он, а не api-роль (:8080).
  scrape_targets = [for process in local.processes : "${var.name_prefix}-${process.name}:8092"]

  runtime_generation = var.verified_secret_generation != "" ? var.verified_secret_generation : var.active_secret_generation
  volume_project     = "${var.name_prefix}-${var.environment}-${var.deployment_id}"

  intake_runtime_paths = {
    request     = "/run/secrets/${local.runtime_generation}/request-runtime-dsn"
    consoleauth = "/run/secrets/${local.runtime_generation}/consoleauth-runtime-dsn"
    vector      = "/run/secrets/${local.runtime_generation}/vector-runtime-dsn"
  }
  intake_runtime_env = {
    LEADGEN_DB_POLICY                    = "runtime-only"
    LEADGEN_REQUEST_RUNTIME_DSN_FILE     = local.intake_runtime_paths.request
    LEADGEN_CONSOLEAUTH_RUNTIME_DSN_FILE = local.intake_runtime_paths.consoleauth
    LEADGEN_VECTOR_RUNTIME_DSN_FILE      = local.intake_runtime_paths.vector
    LEADGEN_CANDIDATE_MANIFEST_FILE      = "/run/leadgen-release/active-candidate.json"
    LEADGEN_CANDIDATE_PUBLIC_KEY_FILE    = "/run/leadgen-trust/active/trusted-release.pub"
    LEADGEN_RELEASE_TRUST_KEY_ID         = var.release_trust_key_id
  }
  runtime_secret_sources = {
    (local.intake_runtime_paths.request)     = "${var.secret_dir}/intake-request-runtime-dsn"
    (local.intake_runtime_paths.consoleauth) = "${var.secret_dir}/intake-consoleauth-runtime-dsn"
    (local.intake_runtime_paths.vector)      = "${var.secret_dir}/intake-vector-runtime-dsn"
  }
  console_runtime_path = "/run/secrets/${local.runtime_generation}/console-password"
  console_secret_mount = { (local.console_runtime_path) = "${var.secret_dir}/console-password" }
  console_groups       = ["core", "demand", "intake", "platform"]
  intake_db_jobs = {
    request-provision = {
      operation = "provision"
      target    = "request-fulfillment"
      secret_files = {
        "/run/secrets/bootstrap-dsn"     = "${var.secret_dir}/request-bootstrap-dsn"
        "/run/secrets/migrator-password" = "${var.secret_dir}/request-migrator-password"
        "/run/secrets/runtime-password"  = "${var.secret_dir}/request-runtime-password"
      }
    }
    request-migrate = {
      operation    = "migrate"
      target       = "request-fulfillment"
      secret_files = { "/run/secrets/migration-dsn" = "${var.secret_dir}/request-migration-dsn" }
    }
    consoleauth-provision = {
      operation = "provision"
      target    = "consoleauth"
      secret_files = {
        "/run/secrets/bootstrap-dsn"     = "${var.secret_dir}/consoleauth-bootstrap-dsn"
        "/run/secrets/migrator-password" = "${var.secret_dir}/consoleauth-migrator-password"
        "/run/secrets/runtime-password"  = "${var.secret_dir}/consoleauth-runtime-password"
      }
    }
    consoleauth-migrate = {
      operation    = "migrate"
      target       = "consoleauth"
      secret_files = { "/run/secrets/migration-dsn" = "${var.secret_dir}/consoleauth-migration-dsn" }
    }
    integration-platform-provision = {
      operation = "provision"
      target    = "integration-platform"
      secret_files = {
        "/run/secrets/bootstrap-dsn"               = "${var.secret_dir}/integration-platform-bootstrap-dsn"
        "/run/secrets/migrator-password"           = "${var.secret_dir}/integration-platform-migrator-password"
        "/run/secrets/runtime-password"            = "${var.secret_dir}/integration-platform-runtime-password"
        "/run/secrets/additional-runtime-password" = "${var.secret_dir}/intake-vector-runtime-password"
      }
    }
    integration-platform-migrate = {
      operation    = "migrate"
      target       = "integration-platform"
      secret_files = { "/run/secrets/migration-dsn" = "${var.secret_dir}/integration-platform-migration-dsn" }
    }
  }
  effective_process_env = merge(
    var.process_env,
    { for group in local.console_groups : group => merge(lookup(var.process_env, group, {}), {
      CONSOLE_USER          = "admin"
      CONSOLE_PASSWORD_FILE = local.console_runtime_path
    }) },
    { intake = merge(lookup(var.process_env, "intake", {}), local.intake_runtime_env, {
      CONSOLE_USER          = "admin"
      CONSOLE_PASSWORD_FILE = local.console_runtime_path
    }) },
  )
  effective_process_secret_files = merge(
    var.process_secret_files,
    { for group in local.console_groups : group => merge(local.console_secret_mount, lookup(var.process_secret_files, group, {})) },
    { intake = merge(local.runtime_secret_sources, local.console_secret_mount, lookup(var.process_secret_files, "intake", {})) },
  )
}

module "network" {
  source = "../../modules/network"

  name   = var.network_name
  labels = { "lead-gen.environment" = var.environment }
}

module "secrets" {
  source = "../../modules/secrets"

  # Файлы секретов на docker-хосте готовит SOPS+age; OpenTofu значения не пишет.
  secret_dir = var.secret_dir

  postgres_password   = var.postgres_password
  rabbitmq_password   = var.rabbitmq_password
  minio_root_password = var.minio_root_password
}

module "data" {
  source = "../../modules/data"

  nodes            = local.nodes
  network_name     = module.network.name
  name_prefix      = var.name_prefix
  secret_dir       = module.secrets.secret_dir
  use_file_secrets = true

  # Значения нужны только как резерв при use_file_secrets = false.
  postgres_password   = var.postgres_password
  rabbitmq_password   = var.rabbitmq_password
  minio_root_password = var.minio_root_password
}

module "observability" {
  source = "../../modules/observability"

  nodes          = local.nodes
  network_name   = module.network.name
  name_prefix    = var.name_prefix
  render_config  = false
  config_source  = "${var.config_dir}/prometheus.yml"
  scrape_targets = local.scrape_targets
}

module "app" {
  source = "../../modules/app"

  processes                   = local.processes
  secret_source_dir           = var.secret_dir
  release_state_volume        = var.release_state_volume
  release_trust_volume        = var.release_trust_volume
  release_trust_key_id        = var.release_trust_key_id
  docker_host                 = var.docker_host
  docker_daemon_id            = var.docker_daemon_id
  environment                 = var.environment
  deployment_id               = var.deployment_id
  active_secret_generation    = var.active_secret_generation
  active_secret_generations   = var.active_secret_generations
  candidate_secret_generation = var.candidate_secret_generation
  verified_secret_generation  = var.verified_secret_generation
  attestation_path            = var.attestation_path
  activation_target           = var.activation_target
  secret_generations          = var.secret_generations
  external_secret_generations = var.external_secret_generations
  volume_project              = local.volume_project
  process_env                 = local.effective_process_env
  process_secret_files        = local.effective_process_secret_files
  db_jobs                     = merge(local.intake_db_jobs, var.db_jobs)
  network_name                = module.network.name
  network_id                  = module.network.id
  name_prefix                 = var.name_prefix

  build                 = var.build_image
  build_context         = local.build_context
  image_name            = var.image_name
  prebuilt_image_digest = var.image_digest

  config_source = var.app_config_source

  depends_on = [module.data, module.observability]
}
