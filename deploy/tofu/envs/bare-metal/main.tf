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

  scrape_targets = [for process in local.processes : "${var.name_prefix}-${process.name}:8080"]
}

module "network" {
  source = "../../modules/network"

  name   = var.network_name
  labels = { "lead-gen.environment" = "bare-metal" }
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

  processes    = local.processes
  network_name = module.network.name
  name_prefix  = var.name_prefix

  build         = var.build_image
  build_context = local.build_context
  image_name    = var.image_name

  config_source = var.app_config_source

  depends_on = [module.data, module.observability]
}
