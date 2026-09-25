# Infraestructura como código — Entrega 2

Terraform que aprovisiona el despliegue básico en Google Cloud para la
plataforma MOOC:

| Componente del enunciado | Recurso de Google Cloud |
|---|---|
| Servicio de máquinas virtuales — Web Server | `google_compute_instance.web` (e2-small, IP pública) |
| Servicio de máquinas virtuales — Worker Server | `google_compute_instance.worker` (e2-small, solo IP privada) |
| Servicio administrado de base de datos relacional | `google_sql_database_instance` (Cloud SQL PostgreSQL, IP privada) |
| Servicio administrado de almacenamiento de objetos | 3 × `google_storage_bucket` (interoperabilidad S3) |
| Red virtual privada, subredes y firewall | `google_compute_network` / `_subnetwork` / `_firewall` + Cloud NAT |
| Cola de mensajería (Redis/asynq) | Contenedor en Worker Server (según el enunciado, no administrado) |

Cada VM, en su arranque, **instala Docker, clona el repositorio** y levanta sus
contenedores. Los secretos se inyectan desde Secret Manager mediante la cuenta
de servicio de la instancia (no viven en el repositorio ni en los metadatos).

## Requisitos previos

- [Terraform](https://developer.hashicorp.com/terraform/install) >= 1.14
- [Google Cloud CLI](https://cloud.google.com/sdk/docs/install) autenticado
- Un proyecto con facturación habilitada

```bash
gcloud auth login
gcloud auth application-default login

# Crear proyecto y vincular facturación (si aún no existe)
gcloud projects create "$PROJECT_ID"
gcloud billing projects link "$PROJECT_ID" --billing-account="$BILLING_ACCOUNT_ID"
```

> Las APIs necesarias (`compute`, `sqladmin`, `servicenetworking`, `storage`,
> `secretmanager`, `iap`, …) se habilitan desde Terraform.

## Despliegue

```bash
cd IaC
cp terraform.tfvars.example terraform.tfvars   # ajustar project_id y demás
terraform init
terraform plan
terraform apply
```

El arranque de las VM continúa después del `apply`; para seguir el avance:

```bash
gcloud compute ssh "$(terraform output -raw web_instance_name)" --zone=us-central1-a \
  --tunnel-through-iap --command 'sudo tail -f /var/log/mooc-startup.log'
gcloud compute ssh "$(terraform output -raw worker_instance_name)" --zone=us-central1-a \
  --tunnel-through-iap --command 'sudo tail -f /var/log/mooc-startup.log'
```

## Acceso y verificación

```bash
terraform output            # URL, IPs, buckets, IP privada de PostgreSQL
curl -k https://$(terraform output -raw web_public_ip)/health
```

- **SSH**: solo a través de IAP (`gcloud compute ssh ... --tunnel-through-iap`),
  el puerto 22 no está abierto a Internet.
- **HTTPS**: Caddy usa certificado interno por defecto; con `domain` configurado
  solicita Let's Encrypt. Se puede usar un nombre tipo `sslip.io` apuntando a la
  IP pública.
- **Base de datos**: IP privada, alcanzable solo desde las VM por VPC peering.

## Configuración de las VM

Ajustes relevantes de `terraform.tfvars`:

| Variable | Descripción | Por defecto |
|---|---|---|
| `project_id` | Proyecto de GCP | — |
| `region` / `zone` | Región y zona | `us-central1` / `us-central1-a` |
| `machine_type` | Perfil de cómputo (2 vCPU / 2 GiB) | `e2-small` |
| `boot_disk_size_gb` | Disco persistente por VM | `30` |
| `repo_url` / `repo_branch` | Repositorio a clonar en cada VM | repo del grupo / `main` |
| `domain` | Dominio para TLS (vacío = IP pública) | `""` |
| `deploy_frontend` | Desplegar el frontend Next.js existente | `false` |
| `db_tier` | Tier de Cloud SQL | `db-f1-micro` |
| `worker_concurrency` | Concurrencia de asynq | `10` |

## Estructura

```
IaC/
├── terraform.tf / providers.tf / variables.tf / locals.tf
├── network.tf      # VPC, subred, firewall, NAT, Private Service Access
├── compute.tf      # Web Server y Worker Server
├── sql.tf          # Cloud SQL PostgreSQL (IP privada)
├── storage.tf      # Buckets, claves HMAC, CORS, lifecycle
├── iam.tf          # APIs, cuenta de servicio, Secret Manager
├── outputs.tf
├── scripts/        # startup de cada VM (Docker + clone + compose up)
└── deploy/         # docker-compose.cloud.yml y plantilla de entorno
```

## Ajustes de integración ya aplicados en la aplicación

- `S3_BUCKET_ORIGINALS`, `S3_BUCKET_HLS`, `S3_BUCKET_PUBLIC`: nombres de bucket
  configurables (Cloud Storage exige nombres globalmente únicos).
- `POSTGRES_SSLMODE`: configuración del modo SSL del DSN (por defecto `disable`).

## Costos y control

- Estimar con la [calculadora de GCP](https://cloud.google.com/products/calculator)
  usando la región, los `e2-small`, los discos de 30 GiB, la IP reservada, NAT,
  Cloud SQL y las operaciones/egress de Cloud Storage.
- **Apagar** las VM cuando no se usen (`gcloud compute instances stop mooc-web mooc-worker`).
- **Eliminar Cloud SQL** después de cargar evidencias: es el recurso más caro y
  sigue facturando aunque se detenga. Conservar los respaldos/`dumps` y scripts.
- Al terminar: `terraform destroy` (el estado local evita dejar recursos huérfanos).

## Limitaciones conocidas

- La carga TUS directa contra la API S3-compatible de GCS debe verificarse
  (`tusd` con `UsePathStyle`); es el principal riesgo de integración.
- `e2-small` (2 GiB) puede ser ajustado para el worker si ffmpeg agota memoria;
  documentar el cambio de tamaño como una configuración distinta.
- El bucket de objetos público usa `allUsers` `objectViewer`; el control de
  acceso fino lo hace la API mediante URLs firmadas.
