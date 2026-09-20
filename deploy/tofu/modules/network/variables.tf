variable "name" {
  description = "Имя docker-сети. Уникально в пределах docker-хоста."
  type        = string
}

variable "driver" {
  description = "Драйвер сети. `bridge` — локальный контейнерный хост."
  type        = string
  default     = "bridge"
}

variable "internal" {
  description = "Замкнуть сеть: без маршрута наружу. По умолчанию false."
  type        = bool
  default     = false
}

variable "labels" {
  description = "Метки сети (например, владелец и область)."
  type        = map(string)
  default     = {}
}
