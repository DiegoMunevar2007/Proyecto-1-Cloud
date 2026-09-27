# Sondeos manuales durante la caída de Redis (2026-09-27 ~04:00 UTC)

Estos no vienen de k6 ni del agente: son sondeos puntuales hechos con `curl` y un
cliente RESP de shell, ejecutados **antes** de reiniciar el Worker Server. Se
conservan porque las series de Prometheus y los contadores de Redis se pierden al
reiniciar el contenedor.

Cada apartado indica el comando exacto para reproducirlo.

## 1. El síntoma: /health falla, el catálogo no

```bash
TOKEN=$(python3 -c "import json;print(json.load(open('capacity-planning/datasets/estudiantes.json'))['admin']['token'])")
for i in 1 2 3; do curl -sk -o /dev/null -w "%{time_total}s http=%{http_code}\n" https://35.255.181.251/health; done
for i in 1 2 3; do curl -sk -o /dev/null -w "%{time_total}s http=%{http_code}\n" https://35.255.181.251/api/v1/courses/24; done
```

| endpoint | latencia | código |
|---|---|---|
| `GET /health` (Postgres + Redis) | 5.412 s / 5.478 s / 5.539 s | **500** |
| `GET /api/v1/courses/24`, token admin válido | 0.484 s / 0.398 s / 0.478 s / 0.420 s / 0.404 s | 200 |
| `GET /api/v1/courses/24`, sin token | 0.424 s / 0.398 s / 0.484 s | 200 |

`/health` es el único handler que hace `rdb.Ping()` (`backend/main.go`, handler
`healthCheck`). El 500 tras ~5 s es un **timeout**, no saturación: el catálogo, que
solo toca Postgres, responde en 0.4 s contra el mismo servidor y en el mismo
segundo.

## 2. Redis acepta TCP pero no responde

Desde `mooc-loadgen`, que está en la misma subred que el worker:

```bash
timeout 4 bash -c "</dev/tcp/10.10.1.20/6379" && echo ABIERTO     # -> ABIERTO
exec 3<>/dev/tcp/10.10.1.20/6379
printf '*1\r\n$4\r\nPING\r\n' >&3
timeout 6 head -c 7 <&3                                        # -> SIN RESPUESTA
timeout 4 bash -c "</dev/tcp/10.10.1.20/3310"                  # -> TIMEOUT (clamd)
timeout 4 bash -c "</dev/tcp/10.179.0.3/5432"                  # -> ABIERTO (Cloud SQL)
```

El puerto 6379 acepta la conexión porque el backlog del kernel la encola, pero
Redis nunca contesta el PING. El 3310 de ClamAV está cerrado porque `clamd` fue
matado por el OOM killer. Cloud SQL responde, lo que descarta un problema de red
o de firewall entre VMs.

## 3. La memoria del Web Server era suficiente

```bash
gcloud compute ssh mooc-web --tunnel-through-iap \
  --command 'free -m | awk "/Mem:/{print \$2\" MB total, \"\$7\" MB disponibles\"}"'
```

```
1976 MB total, 1246 MB disponibles
```

El OOM ocurrió en el **Worker Server**, no en el Web Server: el Web Server tenía
más de la mitad de su RAM libre mientras servía 2xx en 0.4 s.

## 4. Latencia vista desde el propio servidor

Histograma de la API (`GET /metrics`, vía la IP pública) en el momento de la caída:

| ruta | n | avg servidor |
|---|---|---|
| `/api/v1/auth/login` | 2 641 | **542.8 ms** |
| `/health` | 26 | **1 569.4 ms** |
| `/api/v1/courses` | 5 508 | 2.5 ms |
| `/api/v1/progress/:course_id` | 1 330 | 38.1 ms |

`/auth/login` y `/health` son las dos rutas que consultan Redis
(`ResolveSessionTokenWithRole` y `healthCheck`). Las que no lo consultan,
mantienen su latencia normal. Es la firma de una dependencia caída, no de una
plataforma saturada.

## 5. Estado de la instancia

```bash
gcloud compute instances describe mooc-worker --format='value(status,lastStartTimestamp)'
```

```
RUNNING   2026-09-26T19:20:10.233-07:00
```

La VM nunca se cayó: el OOM mató un proceso, no la máquina. El OOM ocurrió a los
**538 s** de arranque (2026-09-27T02:29:15Z), mientras ClamAV cargaba su base de
firmas. Ver `oom-worker-serial.txt`.

## 6. Hallazgo lateral: el detalle de curso es público

`GET /api/v1/courses/:id` respondió **200 sin cabecera `Authorization`** y con un
token inventado:

```bash
curl -sk -o /dev/null -w "%{http_code}\n" https://35.255.181.251/api/v1/courses/24
curl -sk -o /dev/null -w "%{http_code}\n" -H "Authorization: Bearer tokeninvalido123" https://35.255.181.251/api/v1/courses/24
```

Ambas: `200`. No es una regresión de la caída (esa ruta no toca Redis) y queda
documentado aparte como observación de control de acceso.
