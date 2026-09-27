# Costos e ingreso de datos en el estimador

Ficha para reproducir la estimación en el estimador de precios de Google Cloud. Los valores salen del despliegue real (Terraform y `gcloud`), no de la configuración prevista. Fecha de referencia: 2026-09-27.

## Compute Engine

| concepto | valor |
|---|---|
| instancias | 3 |
| tipo de máquina desplegado | `e2-small` |
| vCPU efectivos por máquina | **0,5** (núcleo compartido) |
| memoria por máquina | 2 GiB |
| región / zona | `us-central1` / `us-central1-a` |
| sistema operativo | Debian GNU/Linux 12 (bookworm) |
| disco de arranque | 30 GiB `pd-balanced` por máquina, 90 GiB en total |
| IP externa | 1, reservada y en uso (`35.255.181.251`) |
| IPs internas | 2, en uso (`10.10.1.10`, `10.10.1.20`) |

### El punto que más se equivoca: `e2-small` no son 2 vCPU

`e2-small` es un tipo de **núcleo compartido**. Presenta 2 vCPU al sistema operativo, pero solo para ráfagas cortas, y se factura como 0,5 vCPU. La documentación de Google sitúa los tipos E2 compartidos entre 0,25 y 1 vCPU, y la aritmética lo confirma: 0,5 × $0,021811 + 2 × $0,002924 = $0,01675 por hora, que es el precio de lista del `e2-small` en `us-central1`.

El enunciado pide 2 vCPU y 2 GiB por máquina. La combinación exacta del catálogo para eso es **`e2-highcpu-2`** (2 vCPU dedicadas, 2 GiB), no `e2-small`. La variable del IaC declara el objetivo como "2 vCPU / 2 GiB", pero el valor por defecto elegido no lo cumple: entrega un cuarto del vCPU pedido.

| tipo | vCPU efectivo | memoria | $/hora | $/mes por máquina | 3 máquinas |
|---|---|---|---|---|---|
| `e2-small` (desplegado) | 0,5 | 2 GiB | 0,01675 | 12,23 | 36,69 |
| `e2-highcpu-2` (cumple el enunciado) | 2 | 2 GiB | 0,04947 | 36,11 | 108,34 |

Esta diferencia no es solo de costo. Todas las mediciones de capacidad se hicieron sobre 0,5 vCPU por máquina, así que el worker saturado al 100 % con cuatro ffmpeg lo estaba sobre un cuarto del vCPU especificado. Con `e2-highcpu-2` la tasa de procesamiento y el punto de degradación serían otros, y el enunciado exige identificar los resultados como de una configuración diferente si cambia el tamaño.

## Cloud SQL

| concepto | valor |
|---|---|
| motor | PostgreSQL 17 (no MySQL) |
| edición | Enterprise |
| tipo de máquina | `db-f1-micro`, vCPU compartida, 0.6 GiB |
| almacenamiento | 20 GB SSD |
| aumento automático | activado, sin límite |
| zona / región | `us-central1-a` / `us-central1` |
| disponibilidad | zonal, sin réplica |
| respaldos automáticos | activados, inicio 03:00 |
| recuperación a un punto en el tiempo | desactivada |
| conectividad | solo IP privada, sin IPv4 pública |

La instancia debe eliminarse al terminar la entrega, después de conservar los respaldos o los datos sintéticos y el guion que la reconstruye.

## Cloud Storage

| bucket | clase | región | bytes observados |
|---|---|---|---|
| `mooc-45ef78-originals` | STANDARD | `us-central1` | 3 498 288 097 |
| `mooc-45ef78-hls` | STANDARD | `us-central1` | 1 055 753 368 |
| `mooc-45ef78-public` | STANDARD | `us-central1` | 2 527 |
| total | | | ~4,55 GB |

## Red

| concepto | valor |
|---|---|
| Cloud NAT | 1 pasarela `mooc-nat`, asignación automática de IP, todas las subredes y rangos |
| egreso a Internet | operación normal e instalación de dependencias |
| reglas de firewall | 3, sin costo |
| IAP para SSH | sin costo por túnel |

No hay balanceador de carga, ni CDN, ni replicación entre regiones.

## Secret Manager

Cinco secretos: `mooc-jwt-secret`, `mooc-postgres-password`, `mooc-redis-password`, `mooc-s3-access-key`, `mooc-s3-secret-key`. Los accesos ocurren al arrancar los contenedores, dentro de la franja gratuita.

## Estimación

Entrada en el estimador, con 730 horas al mes y la configuración desplegada:

| servicio | concepto | cantidad | costo, USD |
|---|---|---|---|
| Compute Engine | E2 Instance Core | 1 095 vCPU-h (0,5 × 3 × 730) | 23,88 |
| Compute Engine | E2 Instance Ram | 4 380 GB-h (2 × 3 × 730) | 12,81 |
| Compute Engine | Balanced PD | 90 GB | 9,00 |
| Cloud SQL | PostgreSQL Zonal Micro | 730 h | 7,67 |
| Cloud SQL | SSD de 20 GB | 14 600 GB-h | 3,40 |
| Networking | IP externa en uso | 1 | ~3,65 |
| Networking | Cloud NAT, pasarela | 730 h | ~1,02 |
| Cloud Storage | Standard US Regional | 4,55 GB | 0,09 |
| Secret Manager | 5 secretos y sus accesos | | 0 |
| | **total con `e2-small`** | | **~61,5** |
| | **total con `e2-highcpu-2`** | | **~133,2** |

Quedan fuera tres conceptos que sí se generan: el **egreso a Internet** (instalación de dependencias y salida de segmentos HLS desde Cloud Storage, 1,93 GB en las pruebas), las **operaciones de Cloud Storage** de clase A y B, que se cobran aparte del almacenamiento, y los **respaldos de Cloud SQL** que excedan el tamaño de la instancia.

El supuesto de 730 horas es un techo: supone las tres máquinas encendidas todo el mes. El enunciado pide apagarlas cuando no se usan, y en esta entrega estuvieron apagadas la mayor parte del tiempo. Con 8 horas al día, el cómputo baja a menos de un tercio.

## Consumo observado durante las pruebas

| concepto | observado |
|---|---|
| datos en Cloud Storage | 4,55 GB entre los tres buckets |
| subidas directas aceptadas | 388 en el nivel más alto del escenario 2, más 4 del sembrado |
| segmentos descargados | 272 en el patrón de reproducción y 7 299 en el de descarga masiva |
| trabajos de transcodificación | 967 completados y 1 186 fallidos, de los que 509 son del reinicio de las máquinas |
| peticiones a la API | 31 388 durante el escenario 1 |
| transferencia desde Cloud Storage al cliente | 68,6 MB en el patrón suave y 1,93 GB en el masivo |
| pico de transferencia de subida | 2,16 MB/s, limitado por el enlace del portátil |

## Costos fijos

| concepto | valor | costo |
|---|---|---|
| presupuesto y alertas | **no configurados** | no aplica |
| etiquetas de facturación | `project`, `environment` y `managed_by`, más `role` en el generador | no aplica |

El laboratorio no permitió configurar presupuestos ni alertas de consumo, que es una limitación a documentar. Las máquinas y los discos sí llevan etiquetas, así que el informe de facturación se puede filtrar por entorno y por proyecto, aunque no por componente individual más allá del generador.

## Qué se conserva y qué se elimina

Se elimina la instancia de Cloud SQL, que es el cargo que sigue corriendo aunque las máquinas estén apagadas. Se conservan los buckets, que cuestan por byte almacenado y no por tiempo, y los discos de arranque, que cuestan por tamaño aprovisionado aunque la máquina esté detenida. **Detener una máquina no elimina el costo de su disco ni el de su IP reservada**, y ese es el error de costeo más común en este tipo de entrega.
