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

output "ssh_web" {
  description = "Comando SSH a Web Server mediante IAP"
  value       = "gcloud compute ssh ${google_compute_instance.web.name} --zone=${var.zone} --tunnel-through-iap"
}

output "ssh_worker" {
  description = "Comando SSH a Worker Server mediante IAP"
  value       = "gcloud compute ssh ${google_compute_instance.worker.name} --zone=${var.zone} --tunnel-through-iap"
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
  description = "Cuenta de servicio de runtime de las VM"
  value       = google_service_account.app.email
}
