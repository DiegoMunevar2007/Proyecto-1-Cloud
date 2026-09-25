data "google_compute_image" "boot" {
  project = var.boot_image_project
  family  = var.boot_image_family
}

# VM pública: API modular en Go y proxy inverso.
resource "google_compute_instance" "web" {
  name         = "${var.name_prefix}-web"
  machine_type = var.machine_type
  zone         = var.zone
  tags         = [local.web_tag, local.common_tag]
  labels       = local.labels

  allow_stopping_for_update = true

  boot_disk {
    initialize_params {
      image = data.google_compute_image.boot.self_link
      size  = var.boot_disk_size_gb
      type  = var.boot_disk_type
    }
  }

  network_interface {
    network    = google_compute_network.main.id
    subnetwork = google_compute_subnetwork.app.id
    network_ip = google_compute_address.web_internal.address

    access_config {
      nat_ip = google_compute_address.web.address
    }
  }

  service_account {
    email  = google_service_account.app.email
    scopes = ["cloud-platform"]
  }

  metadata = merge(local.common_metadata, {
    "mooc-role"             = "web"
    "mooc-domain"           = local.web_domain
    "mooc-compose-services" = var.deploy_frontend ? "backend frontend caddy" : "backend caddy"
  })

  metadata_startup_script = file("${path.module}/scripts/web_startup.sh")

  depends_on = [
    google_project_service.enabled,
    google_sql_database.app,
    google_sql_user.app,
    google_secret_manager_secret_version.postgres_password,
    google_secret_manager_secret_version.jwt,
    google_secret_manager_secret_version.redis_password,
    google_secret_manager_secret_version.s3_access,
    google_secret_manager_secret_version.s3_secret,
  ]
}

# VM privada: workers en Go, Redis (asynq) y ClamAV, sin IP pública.
resource "google_compute_instance" "worker" {
  name         = "${var.name_prefix}-worker"
  machine_type = var.machine_type
  zone         = var.zone
  tags         = [local.worker_tag, local.common_tag]
  labels       = local.labels

  allow_stopping_for_update = true

  boot_disk {
    initialize_params {
      image = data.google_compute_image.boot.self_link
      size  = var.boot_disk_size_gb
      type  = var.boot_disk_type
    }
  }

  network_interface {
    network    = google_compute_network.main.id
    subnetwork = google_compute_subnetwork.app.id
    network_ip = google_compute_address.worker_internal.address
  }

  service_account {
    email  = google_service_account.app.email
    scopes = ["cloud-platform"]
  }

  metadata = merge(local.common_metadata, {
    "mooc-role"             = "worker"
    "mooc-compose-services" = "redis clamav worker"
  })

  metadata_startup_script = file("${path.module}/scripts/worker_startup.sh")

  depends_on = [
    google_project_service.enabled,
    google_sql_database.app,
    google_sql_user.app,
    google_secret_manager_secret_version.postgres_password,
    google_secret_manager_secret_version.jwt,
    google_secret_manager_secret_version.redis_password,
    google_secret_manager_secret_version.s3_access,
    google_secret_manager_secret_version.s3_secret,
  ]
}
