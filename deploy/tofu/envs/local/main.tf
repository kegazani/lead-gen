# Локальное окружение: единый docker host на unix-сокете, состояние локальное.
#
# Единый источник истины — deploy/services.yaml и deploy/infrastructure.yaml.
# Оба читаются здесь через `yamldecode(file(...))` и передаются модулям
# переменными: модули не знают про файлы и не дублируют список сервисов и узлов.

locals {
  repo_root  = abspath("${path.root}/../../../..")
  deploy_dir = abspath("${path.root}/../../..")

  secret_dir    = var.secret_dir != "" ? var.secret_dir : abspath("${path.root}/../../.secrets")
  config_dir    = var.config_dir != "" ? var.config_dir : abspath("${path.root}/../../.generated")
  build_context = var.build_context != "" ? var.build_context : local.repo_root

  services       = yamldecode(file("${local.deploy_dir}/services.yaml"))
  infrastructure = yamldecode(file("${local.deploy_dir}/infrastructure.yaml"))

  # Нормализация: необязательные поля получают значения по умолчанию.
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

  # Цели Prometheus: роль api каждого процесса слушает :8080 внутри контейнера.
  scrape_targets = [for process in local.processes : "${var.name_prefix}-${process.name}:8080"]
}

module "network" {
  source = "../../modules/network"

  name   = var.network_name
  labels = { "lead-gen.environment" = "local" }
}

module "secrets" {
  source = "../../modules/secrets"

  # Пути к файлам секретов на docker-хосте. Файлы готовит SOPS+age, а не OpenTofu:
  # значения не попадают ни в репозиторий, ни в состояние.
  secret_dir = local.secret_dir

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
  render_config  = true
  config_dir     = local.config_dir
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

  # Конфигурация монтируется только на чтение.
  config_source = "${local.deploy_dir}/services.yaml"

  depends_on = [module.data, module.observability]
}
