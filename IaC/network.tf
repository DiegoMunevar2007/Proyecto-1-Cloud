# Red virtual privada, subred, reglas de firewall y salida a Internet.
# Solo Web Server expone servicios a Internet; Worker Server es privado.

resource "google_compute_network" "main" {
  name                    = "${var.name_prefix}-vpc"
  auto_create_subnetworks = false
  routing_mode            = "REGIONAL"

  depends_on = [google_project_service.enabled]
}

resource "google_compute_subnetwork" "app" {
  name                     = "${var.name_prefix}-app-subnet"
  ip_cidr_range            = local.app_subnet_cidr
  region                   = var.region
  network                  = google_compute_network.main.id
  private_ip_google_access = true
}

# Direcciones internas fijas: la API y el worker se referencian por IP estable.
resource "google_compute_address" "web_internal" {
  name         = "${var.name_prefix}-web-internal-ip"
  address_type = "INTERNAL"
  address      = local.web_internal_ip
  region       = var.region
  subnetwork   = google_compute_subnetwork.app.id
}

resource "google_compute_address" "worker_internal" {
  name         = "${var.name_prefix}-worker-internal-ip"
  address_type = "INTERNAL"
  address      = local.worker_internal_ip
  region       = var.region
  subnetwork   = google_compute_subnetwork.app.id
}

# IP pública reservada y estable de Web Server.
resource "google_compute_address" "web" {
  name         = "${var.name_prefix}-web-ip"
  address_type = "EXTERNAL"
  region       = var.region
}

# Salida a Internet de las VM (apt, clonado de git, pull de imágenes).
resource "google_compute_router" "main" {
  name    = "${var.name_prefix}-router"
  region  = var.region
  network = google_compute_network.main.id
}

resource "google_compute_router_nat" "main" {
  name                               = "${var.name_prefix}-nat"
  router                             = google_compute_router.main.name
  region                             = var.region
  nat_ip_allocate_option             = "AUTO_ONLY"
  source_subnetwork_ip_ranges_to_nat = "ALL_SUBNETWORKS_ALL_IP_RANGES"

  log_config {
    enable = true
    filter = "ERRORS_ONLY"
  }
}

# SSH solo por Identity-Aware Proxy (no se abre el puerto 22 a Internet).
resource "google_compute_firewall" "iap_ssh" {
  name          = "${var.name_prefix}-allow-iap-ssh"
  network       = google_compute_network.main.name
  direction     = "INGRESS"
  priority      = 1000
  source_ranges = var.authorized_ssh_ranges
  target_tags   = [local.common_tag]

  allow {
    protocol = "tcp"
    ports    = ["22"]
  }
}

# Punto público único: Web Server (API y proxy inverso).
resource "google_compute_firewall" "web" {
  name          = "${var.name_prefix}-allow-web"
  network       = google_compute_network.main.name
  direction     = "INGRESS"
  priority      = 1000
  source_ranges = ["0.0.0.0/0"]
  target_tags   = [local.web_tag]

  allow {
    protocol = "tcp"
    ports    = ["80", "443"]
  }
}

# Comunicación interna entre componentes (web -> Redis en Worker Server).
resource "google_compute_firewall" "internal" {
  name          = "${var.name_prefix}-allow-internal"
  network       = google_compute_network.main.name
  direction     = "INGRESS"
  priority      = 1000
  source_ranges = [local.app_subnet_cidr]
  target_tags   = [local.common_tag]

  allow {
    protocol = "all"
  }
}

# Acceso privado a servicios administrados (Cloud SQL) vía Private Service Access.
resource "google_compute_global_address" "psa" {
  name          = "${var.name_prefix}-psa-range"
  purpose       = "VPC_PEERING"
  address_type  = "INTERNAL"
  prefix_length = 16
  network       = google_compute_network.main.id
}

resource "google_service_networking_connection" "psa" {
  network                 = google_compute_network.main.id
  service                 = "servicenetworking.googleapis.com"
  reserved_peering_ranges = [google_compute_global_address.psa.name]
  deletion_policy = "REMOVE_PEERING"

  depends_on = [google_project_service.enabled]
}
