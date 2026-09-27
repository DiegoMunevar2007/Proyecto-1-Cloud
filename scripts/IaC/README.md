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
| Colmena de mensajería (Redis/asynq) | Contenedor en Worker Server (según el enunciado, no administrado) |
| Telemetría | Ops Agent de Google Cloud en cada VM → Cloud Monitoring / Managed Service for Prometheus |

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
- **HTTPS**:
  - Sin `domain` (o con la IP pública), Caddy emite un certificado **interno
    autofirmado**: la conexión es TLS pero el navegador advierte. Para clientes
    HTTP usar `curl -k` (ej. `curl -k https://<IP>/health`). El Caddyfile fija
    `default_sni {$MOOC_DOMAIN}` para que funcione aunque el cliente no envíe
    SNI al conectar por IP.
  - Con `domain` configurado a un nombre DNS que resuelva a la IP pública,
    Caddy solicita automáticamente un certificado de **Let's Encrypt** (puerto 80
    abierto para el reto HTTP-01). Sirve un dominio propio o uno gratuito tipo
    `34.x.x.x.sslip.io`.
  - Caddy no puede emitir certificados públicos para una IP: por eso una IP
    siempre usa el certificado interno.
  - Tras cambiar `domain` y hacer `apply`, re-ejecutar el arranque en Web Server:
    `sudo google_metadata_script_runner startup` (o reiniciar la VM).
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
| `install_ops_agent` | Instalar el Ops Agent en las VM | `true` |
| `metrics_scrape_interval` | Intervalo de métricas del host y de `/metrics` | `30s` |

## Telemetría y medición de capacidad

No se despliega ningún servidor Prometheus. La recolección la hace el **Ops
Agent** de Google Cloud, que corre en cada VM y reporta a **Cloud Monitoring**:
las métricas del host (CPU, memoria, disco, red) y las del endpoint `/metrics` de
cada contenedor. Se consulta con PromQL y se correlaciona en un mismo panel con
las métricas de Cloud SQL y Cloud Storage, que son de sistema y no se cobran.

Lo hace así porque es la opción nativa que **no compite por los recursos de la VM
que se está midiendo**: no hay un Prometheus de 200–300 MB en la misma máquina de
2 GiB, ni una VM adicional de instrumentación.

| Componente | Endpoint de métricas | Publicado en |
|---|---|---|
| API (Web Server) | `http://127.0.0.1:8080/metrics` | solo loopback |
| Worker (Worker Server) | `http://127.0.0.1:9101/metrics` | solo loopback |

Ambos puertos se publican en `127.0.0.1` porque el agente corre en el host, y así
no hay que abrir nada al firewall.

- El agente se instala y configura con
  `scripts/IaC/scripts/install_ops_agent.sh`, invocado al final del arranque de
  cada VM y **best effort**: si falla, la aplicación queda desplegada sin
  telemetría en vez de abortar.
- La configuración fuerza `collection_interval: 30s` porque el defecto del agente
  (60s) es demasiado grueso para separar una degradación de un pico.
- No hizo falta tocar `iam.tf`: `monitoring.googleapis.com` ya estaba habilitada y
  la cuenta de servicio de las VMs ya tiene `roles/monitoring.metricWriter`.

Verificar que la ingesta está correcta:

```bash
# cuenta las series de la aplicación ya ingeridas
curl -s --get \
  --header "Authorization: Bearer $(gcloud auth print-access-token)" \
  --data-urlencode 'query=count({__name__=~"mooc_.*"})' \
  "https://monitoring.googleapis.com/v1/projects/<PROJECT_ID>/location/global/prometheus/api/v1/query"
```

El detalle de las métricas, las consultas PromQL útiles y la estimación de costo
están en [`docs/entrega2/telemetria.md`](../../docs/entrega2/telemetria.md).

## Correr las pruebas de carga contra el despliegue

El generador es k6 y vive en [`capacity-planning/`](../../capacity-planning).
El Taskfile resuelve la URL desde Terraform, así que no hay que copiarla a mano:

```bash
export ENV=gcp
export ADMIN_PASS='...' SEED_PASSWORD='...'

task -d ../../capacity-planning gcp:wait   # espera a que las VM terminen de arrancar
task -d ../../capacity-planning gcp:seed   # siembra el corpus
task -d ../../capacity-planning gcp:all    # escenario 1, integridad y escenario 2
```

`gcp:wait` comprueba dos cosas antes de sondear `/health`: que Terraform devuelva
`app_url` y que el state tenga máquinas virtuales. Un **state parcial** es peor que
uno vacío, porque la IP reservada existe y parece válida mientras ninguna VM la
atiende; sin esa comprobación habría que esperar quince minutos para descubrirlo.

El corpus se genera contra el entorno que se indique y cada dataset guarda su
`baseUrl`. Si se cambia de local a GCP sin volver a sembrar, los guiones fallan
con un mensaje que lo explica en vez de producir errores que parecen defectos de
la plataforma.

Este runbook es también la referencia de las pruebas de carga: no hay output de
Terraform que lo duplique.

### Generador dentro de la nube

Por defecto las pruebas corren desde donde se invoque el Taskfile. Para las
mediciones de transferencia eso tiene un problema: el enlace de subida de una
conexión doméstica se convierte en el techo del escenario 2 y lo que se mide es
la conexión, no el sistema.

```bash
# en terraform.tfvars
deploy_loadgen = true
```

Eso aprovisiona una tercera máquina, **de pruebas y no de la aplicación**,
en la misma subred y sin IP pública, que instala k6 en la versión fijada en
`k6_version`, ffmpeg y el repositorio. Se administra por IAP SSH y su tráfico
sale por el Cloud NAT ya configurado.

```bash
gcloud compute ssh mooc-loadgen --zone=us-central1-a --tunnel-through-iap
# dentro de la VM:
cd /opt/mooc/capacity-planning
export ENV=gcp BASE_URL=... ADMIN_PASS='...' SEED_PASSWORD='...' INSECURE_TLS=true
k6 run k6/esc2_carga_directa.js
```

Las credenciales no viajan por metadatos ni se escriben en el repositorio: se
exportan en la sesión. El enunciado exige que el generador corra fuera de las dos
VM de la aplicación, y esta tercera máquina es infraestructura de pruebas, igual
que lo sería un portátil: no forma parte del despliegue evaluado.

> **Costo:** la máquina de carga se factura mientras está encendida. Apágala
> (`gcloud compute instances stop mooc-loadgen`) entre corridas, y recuerda
> destruirla al terminar.

## Estructura

```
scripts/IaC/
├── terraform.tf / providers.tf / variables.tf / locals.tf
├── network.tf      # VPC, subred, firewall, NAT, Private Service Access
├── compute.tf      # Web Server y Worker Server
├── sql.tf          # Cloud SQL PostgreSQL (IP privada)
├── storage.tf      # Buckets, claves HMAC, CORS, lifecycle
├── iam.tf          # APIs, cuenta de servicio, Secret Manager
├── outputs.tf
├── scripts/        # startup de cada VM e instalación del Ops Agent
└── deploy/         # docker-compose.cloud.yml y plantilla de entorno
```

## Ajustes de integración ya aplicados en la aplicación

- `S3_BUCKET_ORIGINALS`, `S3_BUCKET_HLS`, `S3_BUCKET_PUBLIC`: nombres de bucket
  configurables (Cloud Storage exige nombres globalmente únicos).
- `POSTGRES_SSLMODE`: configuración del modo SSL del DSN (por defecto `disable`).

## Mailpit (correo de pruebas)

Mailpit no forma parte del enunciado; se conserva como dependencia de desarrollo
para no perder la verificación de correos (códigos de registro).

- Se ejecuta como contenedor en **Web Server**, junto a la API, que es el único
  componente que envía correo (`SMTP_HOST=mailpit`, `SMTP_PORT=1025`).
- El SMTP (1025) solo es alcanzable por la red interna de Docker.
- La interfaz (8025) escucha en `127.0.0.1` y **no** se expone a Internet.
- Para leer los códigos durante pruebas o sustentación:

  ```bash
  gcloud compute start-iap-tunnel mooc-web 8025 \
    --local-host-port=localhost:8025 --project=<PROJECT_ID> --zone=us-central1-a
  # abrir http://localhost:8025
  ```

En producción se reemplazaría por un proveedor SMTP real cambiando
`SMTP_HOST`/`SMTP_PORT`.

## Costos y control

- Estimar con la [calculadora de GCP](https://cloud.google.com/products/calculator)
  usando la región, los `e2-small`, los discos de 30 GiB, la IP reservada, NAT,
  Cloud SQL y las operaciones/egress de Cloud Storage.
- **Apagar** las VM cuando no se usen (`gcloud compute instances stop mooc-web mooc-worker`).
- **Eliminar Cloud SQL** después de cargar evidencias: es el recurso más caro y
  sigue facturando aunque se detenga. Conservar los respaldos/`dumps` y scripts.
- Al terminar: `terraform destroy`. La conexión de Service Networking usa
  `deletion_policy = "REMOVE_PEERING"`: Cloud SQL se borra de forma asíncrona y
  puede retener la conexión, así que si el API rechaza el borrado Terraform
  elimina el peering para poder borrar la VPC. No requiere scripts externos.

## Limitaciones conocidas

- La carga TUS directa contra la API S3-compatible de GCS debe verificarse
  (`tusd` con `UsePathStyle`); es el principal riesgo de integración.
- `e2-small` (2 GiB) puede ser ajustado para el worker si ffmpeg agota memoria;
  documentar el cambio de tamaño como una configuración distinta.
- El bucket de objetos público usa `allUsers` `objectViewer`; el control de
  acceso fino lo hace la API mediante URLs firmadas.
