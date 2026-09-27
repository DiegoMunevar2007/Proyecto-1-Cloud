# Solo valores del despliegue. El runbook de las pruebas de carga, los comandos de
# SSH por IAP y la consulta PromQL de verificación de la ingesta viven en README.md:
# un output es un valor que un script consume, no documentación.
output "project_id" {
  description = "Proyecto de GCP donde se desplegó la plataforma"
  value       = var.project_id
}

output "app_url" {
  description = "URL pública de la API a través del proxy inverso"
  value       = "https://${local.web_domain}"
}

output "web_instance_name" {
  description = "Nombre de la instancia de Web Server"
  value       = google_compute_instance.web.name
}

output "worker_instance_name" {
  description = "Nombre de la instancia de Worker Server"
  value       = google_compute_instance.worker.name
}

output "web_public_ip" {
  description = "IP pública reservada de Web Server"
  value       = google_compute_address.web.address
}

output "web_internal_ip" {
  description = "IP privada de Web Server"
  value       = google_compute_address.web_internal.address
}

output "worker_internal_ip" {
  description = "IP privada de Worker Server (Redis/asynq)"
  value       = google_compute_address.worker_internal.address
}

output "postgres_private_ip" {
  description = "IP privada de la instancia administrada de PostgreSQL"
  value       = google_sql_database_instance.postgres.private_ip_address
}

output "postgres_connection_name" {
  description = "Nombre de conexión de Cloud SQL"
  value       = google_sql_database_instance.postgres.connection_name
}

output "bucket_originals" {
  description = "Bucket de objetos originales"
  value       = google_storage_bucket.originals.name
}

output "bucket_hls" {
  description = "Bucket de derivados HLS"
  value       = google_storage_bucket.hls.name
}

output "bucket_public" {
  description = "Bucket público (miniaturas e insignias)"
  value       = google_storage_bucket.public.name
}

output "service_account_email" {
  description = "Cuenta de servicio de runtime de las VMs"
  value       = google_service_account.app.email
}

output "metrics_scrape_interval" {
  description = "Intervalo de recolección de métricas del Ops Agent en cada VM"
  value       = var.metrics_scrape_interval
}

output "metrics_targets" {
  description = "Endpoint /metrics que el Ops Agent scrapea en cada VM, solo en loopback"
  value = {
    web    = "127.0.0.1:8080/metrics"
    worker = "127.0.0.1:9101/metrics"
  }
}

output "loadgen_internal_ip" {
  description = "IP privada del generador de carga (vacío si deploy_loadgen está en false)"
  value       = var.deploy_loadgen ? google_compute_instance.loadgen[0].network_interface[0].network_ip : ""
}
