locals {
  required_apis = toset([
    "compute.googleapis.com",
    "sqladmin.googleapis.com",
    "servicenetworking.googleapis.com",
    "storage.googleapis.com",
    "secretmanager.googleapis.com",
    "iap.googleapis.com",
    "iam.googleapis.com",
    "cloudresourcemanager.googleapis.com",
    "logging.googleapis.com",
    "monitoring.googleapis.com",
  ])
}

resource "google_project_service" "enabled" {
  for_each = local.required_apis

  service            = each.value
  disable_on_destroy = false
}

# Cuenta de servicio de runtime de las VM (mínimo privilegio).
resource "google_service_account" "app" {
  account_id   = "${var.name_prefix}-app"
  display_name = "MOOC runtime (API y workers)"

  depends_on = [google_project_service.enabled]
}

resource "google_project_iam_member" "app_logging" {
  project = var.project_id
  role    = "roles/logging.logWriter"
  member  = "serviceAccount:${google_service_account.app.email}"
}

resource "google_project_iam_member" "app_monitoring" {
  project = var.project_id
  role    = "roles/monitoring.metricWriter"
  member  = "serviceAccount:${google_service_account.app.email}"
}

# Secretos generados en el despliegue (nunca en el repositorio).
resource "random_password" "postgres" {
  length  = 32
  special = false
}

resource "random_password" "jwt" {
  length  = 48
  special = true
}

resource "random_password" "redis" {
  length  = 32
  special = false
}

resource "google_secret_manager_secret" "postgres_password" {
  secret_id = local.secret_postgres_password

  replication {
    auto {}
  }

  depends_on = [google_project_service.enabled]
}

resource "google_secret_manager_secret_version" "postgres_password" {
  secret      = google_secret_manager_secret.postgres_password.id
  secret_data = random_password.postgres.result
}

resource "google_secret_manager_secret" "jwt" {
  secret_id = local.secret_jwt

  replication {
    auto {}
  }

  depends_on = [google_project_service.enabled]
}

resource "google_secret_manager_secret_version" "jwt" {
  secret      = google_secret_manager_secret.jwt.id
  secret_data = random_password.jwt.result
}

resource "google_secret_manager_secret" "redis_password" {
  secret_id = local.secret_redis_password

  replication {
    auto {}
  }

  depends_on = [google_project_service.enabled]
}

resource "google_secret_manager_secret_version" "redis_password" {
  secret      = google_secret_manager_secret.redis_password.id
  secret_data = random_password.redis.result
}

locals {
  app_secrets = {
    postgres_password = google_secret_manager_secret.postgres_password.id
    jwt               = google_secret_manager_secret.jwt.id
    redis_password    = google_secret_manager_secret.redis_password.id
  }
}

resource "google_secret_manager_secret_iam_member" "app_access" {
  for_each = local.app_secrets

  project   = var.project_id
  secret_id = each.value
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.app.email}"
}
