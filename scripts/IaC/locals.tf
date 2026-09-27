locals {
  labels = {
    project     = var.name_prefix
    environment = "entrega2"
    managed_by  = "terraform"
  }

  # Red
  app_subnet_cidr = "10.10.1.0/24"

  web_internal_ip    = "10.10.1.10"
  worker_internal_ip = "10.10.1.20"

  web_tag    = "web"
  worker_tag = "worker"
  common_tag = "app"

  # Dominio efectivo del proxy inverso (TLS interno si no hay dominio).
  web_domain = var.domain != "" ? var.domain : google_compute_address.web.address

  # Nombres de secretos (misma fuente para iam.tf y metadatos de las VM).
  secret_postgres_password = "${var.name_prefix}-postgres-password"
  secret_jwt               = "${var.name_prefix}-jwt-secret"
  secret_redis_password    = "${var.name_prefix}-redis-password"
  secret_s3_access         = "${var.name_prefix}-s3-access-key"
  secret_s3_secret         = "${var.name_prefix}-s3-secret-key"

  # Buckets con sufijo aleatorio: los nombres de Cloud Storage son globales.
  bucket_originals = "${var.name_prefix}-${random_id.bucket_suffix.hex}-originals"
  bucket_hls       = "${var.name_prefix}-${random_id.bucket_suffix.hex}-hls"
  bucket_public    = "${var.name_prefix}-${random_id.bucket_suffix.hex}-public"

  # Configuración inyectada como metadatos (sin secretos): los scripts de
  # arranque la leen y resuelven los secretos con la cuenta de servicio.
  common_metadata = {
    "mooc-repo-url"                 = var.repo_url
    "mooc-repo-branch"              = var.repo_branch
    "mooc-app-dir"                  = var.app_dir
    "mooc-project-id"               = var.project_id
    "mooc-region"                   = var.region
    "mooc-postgres-host"            = google_sql_database_instance.postgres.private_ip_address
    "mooc-postgres-port"            = "5432"
    "mooc-postgres-db"              = google_sql_database.app.name
    "mooc-postgres-user"            = google_sql_user.app.name
    "mooc-postgres-sslmode"         = "disable"
    "mooc-redis-host"               = local.worker_internal_ip
    "mooc-redis-port"               = "6379"
    "mooc-s3-endpoint"              = "storage.googleapis.com"
    "mooc-s3-public-endpoint"       = "https://storage.googleapis.com"
    "mooc-s3-use-ssl"               = "true"
    "mooc-s3-region"                = "auto"
    "mooc-bucket-originals"         = google_storage_bucket.originals.name
    "mooc-bucket-hls"               = google_storage_bucket.hls.name
    "mooc-bucket-public"            = google_storage_bucket.public.name
    "mooc-worker-concurrency"       = tostring(var.worker_concurrency)
    "mooc-install-ops-agent"        = tostring(var.install_ops_agent)
    "mooc-metrics-scrape-interval"  = var.metrics_scrape_interval
    "mooc-secret-postgres-password" = local.secret_postgres_password
    "mooc-secret-jwt"               = local.secret_jwt
    "mooc-secret-redis-password"    = local.secret_redis_password
    "mooc-secret-s3-access"         = local.secret_s3_access
    "mooc-secret-s3-secret"         = local.secret_s3_secret
  }
}
