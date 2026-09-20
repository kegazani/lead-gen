terraform {
  # OpenTofu совместим с Terraform; ограничение задано по языку, не по сборке.
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

  # Состояние локальное и лежит в каталоге окружения (в репозиторий не попадает,
  # см. deploy/tofu/.gitignore).
  backend "local" {}
}

provider "docker" {
  # Локальный docker-хост через unix-сокет.
  host = var.docker_host
}
