# Servicio administrado de base de datos relacional (PostgreSQL) con IP
# privada. Sin réplicas ni alta disponibilidad: una sola zona.
resource "google_sql_database_instance" "postgres" {
  name                = "${var.name_prefix}-postgres"
  database_version    = var.db_version
  region              = var.region
  deletion_protection = false

  settings {
    edition           = "ENTERPRISE"
    tier              = var.db_tier
    availability_type = "ZONAL"
    disk_type         = "PD_SSD"
    disk_size         = var.db_disk_size_gb

    ip_configuration {
      ipv4_enabled    = false
      private_network = google_compute_network.main.id
    }

    backup_configuration {
      enabled = true
    }
  }

  depends_on = [google_service_networking_connection.psa]
}

resource "google_sql_database" "app" {
  name     = var.db_name
  instance = google_sql_database_instance.postgres.name
}

resource "google_sql_user" "app" {
  name     = var.db_user
  instance = google_sql_database_instance.postgres.name
  password = random_password.postgres.result
}
