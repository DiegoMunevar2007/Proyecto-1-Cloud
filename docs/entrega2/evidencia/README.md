# Evidencia del incidente de agotamiento de memoria

Incidente del 2026-09-27 en el **Worker Server** (`mooc-worker`), que dejó a
ClamAV y Redis sin servicio y produjo respuestas 401 y 500 con 2 a 20 s de
latencia en la API.

El enunciado pide documentar el caso: *"Si una dependencia no puede ejecutarse con
esos recursos, deberá documentarse el fallo, el ajuste mínimo aplicado y su efecto
sobre costo y capacidad."* Eso es exactamente lo que hay en este directorio.

## Archivos

| Archivo | Qué es | Cómo se obtuvo |
|---|---|---|
| `worker-serial.log` | Consola serial completa del worker (6 812 líneas) | `gcloud compute instances get-serial-port-output mooc-worker --port=1 --start=0` |
| `oom-worker-serial.txt` | Las 6 líneas del OOM killer dentro del log anterior | `grep` sobre el log anterior |
| `api-gin.log` | Líneas `[GIN]` del log de la API: 46 147 respuestas en 14 h | `docker logs mooc-cloud-backend-1 --since 14h \| grep "^\[GIN\]"` |
| `cascada-no-2xx.log` | Las 4 567 respuestas 4xx/5xx del log anterior | `grep` sobre el log anterior |
| `resumen-latencia-no-2xx.txt` | Histograma de latencia × código de respuesta | `python3` sobre el log anterior |
| `sondeos-manuales-antes-de-reiniciar.md` | Sondeos con `curl` y RESP, con sus comandos | ver el documento |

## Cronología

| hora (UTC) | evento |
|---|---|
| 02:20:10 | Arranca `mooc-worker` |
| 02:29:15 | **OOM killer mata `clamd`** (pid 6395, 913 700 kB RSS) a los 538 s de arranque, mientras cargaba la base de firmas |
| ~04:00 | Redis deja de responder comandos; la API devuelve 401 tras ~5 s en toda ruta autenticada y 500 en `/health` |
| 04:30 | Se capturan estos archivos |

## Las dos líneas que lo prueban

```
Out of memory: Killed process 6395 (clamd) total-vm:966332kB, anon-rss:913700kB
```

```
| 500 |  1569.4ms | GET "/health"      # el único handler que hace rdb.Ping()
```

## Distribución de latencia durante la caída

43 995 respuestas en la ventana de 14 h del log de la API:

| bucket | 2xx | 4xx | 5xx |
|---|---|---|---|
| respuesta < 50 ms | 41 567 | 2 259 | 0 |
| 2 s – 10 s | 0 | **149** | **14** |
| > 10 s | 0 | **3** | 0 |

Las 2 259 4xx rápidas son rechazos esperados por reglas de negocio (404 de
`download-url` en recursos sin objeto, 409 de inscripción repetida). Los **166
fallos de 2 a 20 s** son la firma del incidente: ninguna otra combinación de
código y latencia aparece en el log.

## Reproduction

El incidente depende de que ClamAV cargue su base de firmas dentro de los 2 GiB de
la VM. Se reproduce reiniciando el worker con el `docker-compose.cloud.yml` sin
límites de memoria:

```bash
gcloud compute instances reset mooc-worker \
  --project=proyecto-2-cloud-509714 --zone=us-central1-a
# esperar ~9 min y observar:
curl -sk -o /dev/null -w "%{time_total}s http=%{http_code}\n" https://35.255.181.251/health
```
