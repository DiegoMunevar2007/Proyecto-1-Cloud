resource "random_id" "bucket_suffix" {
  byte_length = 3
}

# Tres buckets lógicos (originales, derivados HLS y público) servidos por
# la API S3-compatible de Cloud Storage.
resource "google_storage_bucket" "originals" {
  name                        = local.bucket_originals
  location                    = var.region
  storage_class               = "STANDARD"
  force_destroy               = true
  uniform_bucket_level_access = true

  cors {
    origin          = var.cors_origins
    method          = ["GET", "HEAD", "PUT", "POST", "PATCH", "DELETE", "OPTIONS"]
    response_header = ["Content-Type", "Authorization", "Upload-Offset", "Upload-Length", "Tus-Resumable", "Location", "Content-Range", "ETag"]
    max_age_seconds = 3600
  }

  # Los sidecars de tusd (.info/.part) expiran; los objetos finales quedan limpios.
  lifecycle_rule {
    condition {
      age            = 7
      matches_prefix = ["tus-meta/"]
    }
    action {
      type = "Delete"
    }
  }
}

resource "google_storage_bucket" "hls" {
  name                        = local.bucket_hls
  location                    = var.region
  storage_class               = "STANDARD"
  force_destroy               = true
  uniform_bucket_level_access = true

  cors {
    origin          = var.cors_origins
    method          = ["GET", "HEAD", "OPTIONS"]
    response_header = ["Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "ETag"]
    max_age_seconds = 3600
  }
}

resource "google_storage_bucket" "public" {
  name                        = local.bucket_public
  location                    = var.region
  storage_class               = "STANDARD"
  force_destroy               = true
  uniform_bucket_level_access = true

  cors {
    origin          = var.cors_origins
    method          = ["GET", "HEAD", "OPTIONS"]
    response_header = ["Content-Type", "Content-Length", "ETag"]
    max_age_seconds = 3600
  }
}

# Lectura anónima de derivados e imágenes públicas (el control de acceso lo
# verifica la API antes de revelar la URL).
resource "google_storage_bucket_iam_member" "hls_public" {
  bucket = google_storage_bucket.hls.name
  role   = "roles/storage.objectViewer"
  member = "allUsers"
}

resource "google_storage_bucket_iam_member" "public_public" {
  bucket = google_storage_bucket.public.name
  role   = "roles/storage.objectViewer"
  member = "allUsers"
}

locals {
  app_buckets = {
    originals = google_storage_bucket.originals.name
    hls       = google_storage_bucket.hls.name
    public    = google_storage_bucket.public.name
  }
}

resource "google_storage_bucket_iam_member" "app_object_admin" {
  for_each = local.app_buckets

  bucket = each.value
  role   = "roles/storage.objectAdmin"
  member = "serviceAccount:${google_service_account.app.email}"
}

resource "google_storage_bucket_iam_member" "app_bucket_reader" {
  for_each = local.app_buckets

  bucket = each.value
  role   = "roles/storage.legacyBucketReader"
  member = "serviceAccount:${google_service_account.app.email}"
}

# Claves HMAC (interoperabilidad S3) para que API y workers firmen URLs.
resource "google_storage_hmac_key" "app" {
  service_account_email = google_service_account.app.email
  project               = var.project_id
}

resource "google_secret_manager_secret" "s3_access" {
  secret_id = local.secret_s3_access

  replication {
    auto {}
  }

  depends_on = [google_project_service.enabled]
}

resource "google_secret_manager_secret_version" "s3_access" {
  secret      = google_secret_manager_secret.s3_access.id
  secret_data = google_storage_hmac_key.app.access_id
}

resource "google_secret_manager_secret" "s3_secret" {
  secret_id = local.secret_s3_secret

  replication {
    auto {}
  }

  depends_on = [google_project_service.enabled]
}

resource "google_secret_manager_secret_version" "s3_secret" {
  secret      = google_secret_manager_secret.s3_secret.id
  secret_data = google_storage_hmac_key.app.secret
}

resource "google_secret_manager_secret_iam_member" "s3_access" {
  project   = var.project_id
  secret_id = google_secret_manager_secret.s3_access.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.app.email}"
}

resource "google_secret_manager_secret_iam_member" "s3_secret" {
  project   = var.project_id
  secret_id = google_secret_manager_secret.s3_secret.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.app.email}"
}
