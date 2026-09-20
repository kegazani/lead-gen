terraform {
  required_version = ">= 1.6.0"

  required_providers {
    docker = {
      source  = "kreuzwerker/docker"
      version = "~> 3.0"
    }
    local = {
      source  = "hashicorp/local"
      version = "~> 2.4"
    }
  }

  # Состояние — вне репозитория, в GitLab-managed HTTP backend.
  #
  # Адрес и доступ НЕ зашиты в конфигурацию: блок пуст, параметры приходят извне.
  # Вариант 1 — переменные окружения GitLab:
  #   TF_HTTP_ADDRESS, TF_HTTP_LOCK_ADDRESS, TF_HTTP_UNLOCK_ADDRESS,
  #   TF_HTTP_USERNAME, TF_HTTP_PASSWORD
  # Вариант 2 — файл вне репозитория:
  #   tofu -chdir=deploy/tofu/envs/bare-metal init \
  #     -backend-config=/etc/lead-gen/backend.gitlab.hcl
  #
  # Для проверок без состояния: `init -backend=false`.
  backend "http" {}
}

provider "docker" {
  # Docker-хост по SSH. Значение по умолчанию — заглушка, задаётся переменной
  # (TF_VAR_docker_host или tfvars вне репозитория).
  host = var.docker_host
}
