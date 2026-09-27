variable "project_id" {
  description = "ID del proyecto de Google Cloud donde se despliega la plataforma"
  type        = string
}

variable "region" {
  description = "Región de Google Cloud para red, cómputo, base de datos y almacenamiento"
  type        = string
  default     = "us-central1"
}

variable "zone" {
  description = "Zona de disponibilidad donde se ubican las máquinas virtuales"
  type        = string
  default     = "us-central1-a"
}

variable "name_prefix" {
  description = "Prefijo para nombrar todos los recursos del despliegue"
  type        = string
  default     = "mooc"

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{2,20}$", var.name_prefix))
    error_message = "name_prefix debe ser minúsculas (letras, dígitos o guiones) y comenzar con letra."
  }
}

variable "machine_type" {
  description = "Tipo de máquina de las dos VM (2 vCPU / 2 GiB objetivo)"
  type        = string
  default     = "e2-small"
}

variable "boot_disk_size_gb" {
  description = "Tamaño del disco de arranque persistente por máquina en GiB"
  type        = number
  default     = 30

  validation {
    condition     = var.boot_disk_size_gb >= 30
    error_message = "El enunciado exige al menos 30 GiB de almacenamiento persistente por VM."
  }
}

variable "boot_disk_type" {
  description = "Tipo de disco de arranque de las VM"
  type        = string
  default     = "pd-balanced"
}

variable "boot_image_project" {
  description = "Proyecto de la imagen de arranque"
  type        = string
  default     = "debian-cloud"
}

variable "boot_image_family" {
  description = "Familia de imagen de arranque (incluye Docker y google-cloud-cli)"
  type        = string
  default     = "debian-12"
}

variable "repo_url" {
  description = "URL HTTPS del repositorio que se clona en cada VM"
  type        = string
  default     = "https://github.com/DiegoMunevar2007/Proyecto-1-Cloud.git"
}

variable "repo_branch" {
  description = "Rama del repositorio a desplegar"
  type        = string
  default     = "main"
}

variable "app_dir" {
  description = "Directorio destino del clon en cada VM"
  type        = string
  default     = "/opt/mooc"
}

variable "domain" {
  description = "Dominio para el certificado TLS. Vacío usa la IP pública de Web Server"
  type        = string
  default     = ""
}

variable "deploy_frontend" {
  description = "Desplegar el frontend Next.js existente detrás del proxy inverso"
  type        = bool
  default     = false
}

variable "worker_concurrency" {
  description = "Concurrencia de asynq en Worker Server"
  type        = number
  default     = 10
}

variable "install_ops_agent" {
  description = "Instalar el Ops Agent de Google Cloud en las VM para enviar métricas a Cloud Monitoring"
  type        = bool
  default     = true
}

variable "metrics_scrape_interval" {
  description = <<-EOT
    Intervalo de recolección de métricas: tanto el hostmetrics del Ops Agent
    como el scrape de /metrics. El valor por defecto del agente es 60s, demasiado
    grueso para separar una degradación de un pico. Más corto da más resolución
    y más muestras facturadas.
  EOT
  type        = string
  default     = "30s"

  validation {
    condition     = can(regex("^[0-9]+(ms|s|m)$", var.metrics_scrape_interval))
    error_message = "metrics_scrape_interval debe ser una duración de Prometheus, por ejemplo 15s, 30s o 1m."
  }
}

variable "deploy_loadgen" {
  description = <<-EOT
    Aprovisiona una tercera máquina, de pruebas y no de la aplicación, para
    ejecutar k6 desde dentro de la región. El enunciado exige que el generador
    corra fuera de las dos VM de la aplicación y que se verifique que no limita
    los resultados: desde un enlace doméstico, la subida de los archivos del
    escenario 2 mide la conexión del portátil y no el sistema. Apagado por
    defecto: sólo se enciende para las corridas de capacidad.
  EOT
  type        = bool
  default     = false
}

variable "loadgen_machine_type" {
  description = "Tipo de máquina del generador de carga. k6 no necesita mucho; el límite suele ser la red"
  type        = string
  default     = "e2-small"
}

variable "k6_version" {
  description = "Versión de k6 que se instala en el generador (etiqueta de release, con la v)"
  type        = string
  default     = "v2.3.0"
}

variable "db_version" {
  description = "Versión de PostgreSQL del servicio administrado"
  type        = string
  default     = "POSTGRES_17"
}

variable "db_tier" {
  description = "Tier de la instancia administrada de PostgreSQL"
  type        = string
  default     = "db-f1-micro"
}

variable "db_disk_size_gb" {
  description = "Tamaño del disco de la instancia administrada en GiB"
  type        = number
  default     = 20
}

variable "db_name" {
  description = "Nombre de la base de datos transaccional"
  type        = string
  default     = "mydb"
}

variable "db_user" {
  description = "Usuario administrador de la base de datos"
  type        = string
  default     = "postgres"
}

variable "authorized_ssh_ranges" {
  description = "Rangos CIDR autorizados para SSH (por defecto el rango de IAP)"
  type        = list(string)
  default     = ["35.235.240.0/20"]
}

variable "cors_origins" {
  description = "Orígenes permitidos por CORS en el bucket de objetos"
  type        = list(string)
  default     = ["*"]
}
