output "containers" {
  description = "Имя группы процессов -> имя контейнера."
  value       = { for name, container in docker_container.service : name => container.name }
}

output "image" {
  description = "Ссылка на образ, использованный контейнерами."
  value       = var.build ? docker_image.service[0].image_id : var.image_name
}

output "release_plan" {
  description = "Только несекретный plan для внешнего release runner; не означает успешный stage."
  value = var.candidate_secret_generation == "" ? null : {
    environment                 = var.environment
    deployment_id               = var.deployment_id
    candidate_secret_generation = var.candidate_secret_generation
    active_secret_generation    = lookup(var.active_secret_generations, "intake", var.active_secret_generation)
    active_secret_generations   = { for group in keys(local.secret_groups) : group => lookup(var.active_secret_generations, group, var.active_secret_generation) }
    secret_generations          = sort(tolist(local.retained_generations))
    expected_activation_target  = var.activation_target
    docker_daemon_id            = var.docker_daemon_id
    source_root                 = var.secret_source_dir
    network_name                = var.network_name
    network_id                  = var.network_id
    network_driver              = "bridge"
    network_labels = {
      "lead-gen.environment" = var.environment
    }
    docker_host          = var.docker_host
    volume_project       = var.volume_project
    helper_image_digest  = var.build ? docker_image.service[0].image_id : var.prebuilt_image_digest
    runtime_image_digest = var.build ? docker_image.service[0].image_id : var.prebuilt_image_digest
    release_state_volume = var.release_state_volume
    release_trust_volume = var.release_trust_volume
    release_trust_key_id = var.release_trust_key_id
    runtime_bundles = {
      for group in keys(local.secret_groups) : group => {
        volume_name = "${var.volume_project}-secrets-${group}-${var.candidate_secret_generation}"
        volume_id   = docker_volume.runtime_secret_bundle["${group}-${var.candidate_secret_generation}"].id
        driver      = "local"
        labels = {
          "lead-gen.release.environment" = var.environment
          "lead-gen.release.deployment"  = var.deployment_id
          "lead-gen.release.target"      = group
          "lead-gen.release.generation"  = var.candidate_secret_generation
          "lead-gen.release.daemon"      = var.docker_daemon_id
        }
        path  = "/run/secrets/${var.candidate_secret_generation}"
        files = { for target, source in var.process_secret_files[group] : basename(source) => { target_path = "/run/secrets/${var.candidate_secret_generation}/${basename(target)}", source_path = source } }
      }
    }
    db_bundles = {
      for job, spec in var.db_jobs : job => {
        target      = spec.target
        operation   = spec.operation
        volume_name = "${var.volume_project}-secrets-${job}-${var.candidate_secret_generation}"
        volume_id   = docker_volume.db_secret_bundle["${job}-${var.candidate_secret_generation}"].id
        driver      = "local"
        labels = {
          "lead-gen.release.environment" = var.environment
          "lead-gen.release.deployment"  = var.deployment_id
          "lead-gen.release.target"      = job
          "lead-gen.release.generation"  = var.candidate_secret_generation
          "lead-gen.release.daemon"      = var.docker_daemon_id
        }
        files = { for target, source in spec.secret_files : basename(source) => { target_path = "/run/secrets/${var.candidate_secret_generation}/${basename(target)}", source_path = source } }
      }
    }
    active_runtime = {
      for group in keys(local.secret_groups) : group => {
        container_name = docker_container.service[group].name
        container_id   = docker_container.service[group].id
        image_digest   = var.build ? docker_image.service[0].image_id : var.prebuilt_image_digest
        volume_name    = "${var.volume_project}-secrets-${group}-${lookup(var.active_secret_generations, group, var.active_secret_generation)}"
        volume_id      = "${var.volume_project}-secrets-${group}-${lookup(var.active_secret_generations, group, var.active_secret_generation)}"
        driver         = "local"
        labels = {
          "lead-gen.release.environment" = var.environment
          "lead-gen.release.deployment"  = var.deployment_id
          "lead-gen.release.target"      = group
          "lead-gen.release.generation"  = lookup(var.active_secret_generations, group, var.active_secret_generation)
          "lead-gen.release.daemon"      = var.docker_daemon_id
        }
        generation = lookup(var.active_secret_generations, group, var.active_secret_generation)
      }
    }
  }
}
