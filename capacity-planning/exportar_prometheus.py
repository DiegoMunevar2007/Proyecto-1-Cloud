#!/usr/bin/env python3
"""Exporta de Cloud Monitoring las métricas recolectadas por el Ops Agent.

Motivo: `gcloud monitoring time-series` no existe en la CLI, y la API REST exige
un tipo de métrica exacto por petición e `interval.endTime` obligatorio. Por eso
el volcado se hace por métrica y con paginación.

La API guarda los histogramas como distribución nativa (sufijo /histogram), no
como series _bucket. Se exportan crudas (bucketCounts + explicitBuckets) para
que los percentiles se puedan recalcular y auditar; `calcular_percentiles.py`
hace esa conversión.

Uso:
    python3 exportar_prometheus.py --project <PROJECT_ID> --salida <DIR>

Reproduce cada archivo: el manifest anota la llamada exacta que lo generó.
"""

import argparse
import json
import os
import subprocess
import sys
import time
from urllib.parse import urlencode

API = "https://monitoring.googleapis.com/v3/projects"

# Métricas de la aplicación. Los nombres exactos salen de metricDescriptors.list;
# se descubren con --descubrir para no depender de esta lista.
APP_METRICS = [
    "prometheus.googleapis.com/mooc_http_request_duration_seconds/histogram",
    "prometheus.googleapis.com/mooc_http_requests_total/counter",
    "prometheus.googleapis.com/mooc_http_requests_inflight/gauge",
    "prometheus.googleapis.com/mooc_queue_pending/gauge",
    "prometheus.googleapis.com/mooc_queue_active/gauge",
    "prometheus.googleapis.com/mooc_queue_age_seconds/gauge",
    "prometheus.googleapis.com/mooc_queue_size/gauge",
    "prometheus.googleapis.com/mooc_queue_retry/gauge",
    "prometheus.googleapis.com/mooc_queue_archived/gauge",
    "prometheus.googleapis.com/mooc_queue_scrape_ok/gauge",
    "prometheus.googleapis.com/mooc_queue_processed_total/counter",
    "prometheus.googleapis.com/mooc_queue_failed_total/counter",
    "prometheus.googleapis.com/mooc_worker_jobs_active/gauge",
    "prometheus.googleapis.com/mooc_worker_ffmpeg_active/gauge",
    "prometheus.googleapis.com/mooc_worker_jobs_completed_total/counter",
    "prometheus.googleapis.com/mooc_worker_jobs_failed_total/counter",
    "prometheus.googleapis.com/process_resident_memory_bytes/gauge",
    "prometheus.googleapis.com/go_goroutines/gauge",
    "prometheus.googleapis.com/up/gauge",
]

# Métricas de host. El enunciado pide CPU, memoria, red y disco de los servidores.
#
# La red no viene del hostmetrics del Ops Agent: en este proyecto no existen
# métricas interface/network/* (se comprobó con metricDescriptors.list, que
# devolvió 259 tipos de agent.googleapis.com y ninguno de red). La red de las
# máquinas sale de las métricas estándar de GCE, y la CPU se cross-checkea contra
# instance/cpu/utilization para validar la del agente.
HOST_METRICS = [
    "agent.googleapis.com/cpu/utilization",
    "agent.googleapis.com/processes/rss_usage",
    "agent.googleapis.com/processes/vm_usage",
    "agent.googleapis.com/disk/percent_used",
    "agent.googleapis.com/disk/operation_count",
    # processes/vm_usage y processes/rss_usage son deltas: describen variacion, no
    # nivel, y dan maximos imposibles (2485 MB en una VM de 1976 MB). Para el
    # consumo absoluto estan memory/percent_used y memory/balloon/ram.
    "agent.googleapis.com/memory/percent_used",
    "agent.googleapis.com/memory/balloon/ram",
    "agent.googleapis.com/processes/count_by_state",
    "compute.googleapis.com/instance/cpu/utilization",
    "compute.googleapis.com/instance/network/received_bytes_count",
    "compute.googleapis.com/instance/network/sent_bytes_count",
    "compute.googleapis.com/instance/network/received_packets_count",
    "compute.googleapis.com/instance/network/sent_packets_count",
]

# Ventanas reales, derivadas de la primera y la última marca de tiempo de cada
# results/*.json (JSONL, una muestra por línea). No son estimaciones.
#
# Las ventanas por corrida permiten atribuir un valor a un nivel. Las ventanas
# "curva" cubren toda la sesión en un solo intervalo: como los niveles duran entre
# 2 y 4 minutos, aislarlos deja solo unos pocos puntos por serie, mientras que en
# la curva completa cada nivel aparece como un escalón y la tendencia es legible.
WINDOWS = {
    "curva-esc1":      ("2026-09-27T03:10:00Z", "2026-09-27T03:46:00Z"),
    "curva-esc2":      ("2026-09-27T05:45:00Z", "2026-09-27T06:02:00Z"),
    "saturacion-worker": ("2026-09-27T06:00:00Z", "2026-09-27T07:10:00Z"),
    "incidente-oom":   ("2026-09-27T02:20:00Z", "2026-09-27T02:50:00Z"),
    "esc1-L0":         ("2026-09-27T03:10:00Z", "2026-09-27T03:19:00Z"),
    "esc1-L1":         ("2026-09-27T03:15:00Z", "2026-09-27T03:24:00Z"),
    "esc1-L2":         ("2026-09-27T03:20:00Z", "2026-09-27T03:29:00Z"),
    "esc1-L4":         ("2026-09-27T03:30:00Z", "2026-09-27T03:39:00Z"),
    "esc1-L3":         ("2026-09-27T03:35:00Z", "2026-09-27T03:44:00Z"),
    "esc1-login":      ("2026-09-27T03:40:00Z", "2026-09-27T03:46:00Z"),
    "esc2-carga-M0":   ("2026-09-27T05:45:00Z", "2026-09-27T05:53:00Z"),
    "esc2-carga-M1":   ("2026-09-27T05:50:00Z", "2026-09-27T05:58:00Z"),
    "esc2-carga-M2":   ("2026-09-27T05:54:00Z", "2026-09-27T06:02:00Z"),
    "sesion-completa": ("2026-09-27T02:20:00Z", "2026-09-27T07:10:00Z"),
}


def token():
    return subprocess.run(
        ["gcloud", "auth", "print-access-token"],
        capture_output=True, text=True, check=True).stdout.strip()


# Las series de hostmetrics identifican la máquina por instance_id numérico. Se
# resuelve el nombre una vez para que los CSV sean legibles sin consultar la API.
INSTANCIAS = {}


def mapear_instancias(project):
    out = subprocess.run(
        ["gcloud", "compute", "instances", "list", f"--project={project}",
         "--format=value(id,name)"],
        capture_output=True, text=True).stdout.strip().split("\n")
    for linea in out:
        if not linea.strip():
            continue
        partes = linea.split()
        if len(partes) >= 2:
            INSTANCIAS[partes[0]] = partes[1]


def get(tok, url, intentos=4):
    """GET con reintentos: la API de Monitoring devuelve 429 y 503 bajo carga."""
    for i in range(intentos):
        out = subprocess.run(
            ["curl", "-sS", "-H", f"Authorization: Bearer {tok}", url],
            capture_output=True, text=True)
        try:
            d = json.loads(out.stdout)
        except json.JSONDecodeError:
            d = {"error": {"message": out.stderr or "respuesta no JSON"}}
        if "error" not in d:
            return d
        msg = d["error"].get("message", "")
        if "exactamente" in msg or "does not support" in msg or "single metric" in msg:
            return d  # filtro inválido, no tiene sentido reintentar
        time.sleep(2 ** i)
    return d


def descubrir(tok, project, prefijo):
    """Lista los tipos de métrica que existen. No falla nunca."""
    url = f"{API}/{project}/metricDescriptors?" + urlencode(
        {"filter": f'metric.type=starts_with("{prefijo}")', "pageSize": 1000})
    d = get(tok, url)
    return sorted(m["type"] for m in d.get("metricDescriptors", []))


def exportar(tok, project, mtype, inicio, fin, destino):
    """Vuelca una métrica. Devuelve (n_series, n_puntos, n_errores).

    Los puntos se piden sin agregación: la resolución nativa ya es la del scrape
    (30 s), y agregar solo reetiqueta o inventa valores.
    """
    filtro = f'metric.type="{mtype}"'
    base = f"{API}/{project}/timeSeries?"
    params = {
        "filter": filtro,
        "interval.startTime": inicio,
        "interval.endTime": fin,
        "pageSize": 2000,
    }
    es_distribucion = mtype.endswith("/histogram")
    # No se pide agregación. El scrape ya es de 30 s, así que alinear solo
    # reetiqueta los puntos, y además ALIGN_MEAN es inválido tanto para un
    # contador (necesita ALIGN_RATE) como para una distribución (cuyos
    # bucketCounts se exportan precisamente para recalcular percentiles). Pedir
    # los puntos en crudo es lo fiel y lo que no falla en silencio.

    series, puntos, errores, paginas = [], 0, 0, 0
    token_pagina = None
    while True:
        p = dict(params)
        if token_pagina:
            p["pageToken"] = token_pagina
        url = base + urlencode(p)
        d = get(tok, url)
        if "error" in d:
            errores += 1
            break
        paginas += 1
        for ts in d.get("timeSeries", []):
            filas = []
            for pt in ts.get("points", []):
                filas.append({
                    "time": pt.get("interval", {}).get("endTime"),
                    "value": pt.get("value", {}),
                })
            puntos += len(filas)
            series.append({
                "labels": ts.get("metric", {}).get("labels", {}),
                # El recurso completo, no solo sus etiquetas: instance_id es lo
                # único que identifica la VM y vive ahí.
                "resource": ts.get("resource", {}),
                "puntos": filas,
            })
        token_pagina = d.get("nextPageToken")
        if not token_pagina or paginas > 40:
            break

    os.makedirs(os.path.dirname(destino), exist_ok=True)
    if es_distribucion:
        with open(destino, "w") as f:
            json.dump(series, f, indent=1)
    else:
        import csv
        with open(destino, "w", newline="") as f:
            w = csv.writer(f)
            # labels lleva el diccionario completo de métrica y recurso en JSON. Con una
            # lista fija de columnas se perdían etiquetas necesarias para
            # filtrar: cpu/utilization llega partido en cpu_name=guest|idle|... y
            # sin eso "idle" se mezclaba con el uso real.
            w.writerow(["timestamp", "labels", "value"])
            for s in series:
                ctx = {"resource_type": s["resource"].get("type", ""),
                       **{f"resource_{k}": v for k, v in s["resource"].get("labels", {}).items()},
                       **s["labels"]}
                # El hostmetrics llega con instance_id numérico; el nombre de la VM
                # es lo legible, y el mapeo se guarda junto a los datos.
                iid = s["resource"].get("labels", {}).get("instance_id")
                if iid:
                    ctx["vm"] = INSTANCIAS.get(iid, iid)
                for pt in s["puntos"]:
                    v = pt["value"]
                    w.writerow([
                        pt["time"],
                        json.dumps(ctx, ensure_ascii=False, sort_keys=True),
                        v.get("doubleValue", v.get("int64Value", v.get("distributionValue", ""))),
                    ])
    return len(series), puntos, errores


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--project", required=True)
    ap.add_argument("--salida", default="docs/entrega2/evidencia/prometheus")
    ap.add_argument("--descubrir", action="store_true",
                    help="solo lista los tipos de métrica disponibles y sale")
    ap.add_argument("--sin-host", action="store_true")
    ap.add_argument("--ventana", action="append",
                    help="exporta solo la ventana indicada (repetible)")
    args = ap.parse_args()

    tok = token()
    mapear_instancias(args.project)
    if INSTANCIAS:
        print(f"instancias mapeadas: {len(INSTANCIAS)} -> "
              + ", ".join(f"{k}={v}" for k, v in sorted(INSTANCIAS.items(),
                                                         key=lambda kv: kv[1])))

    if args.descubrir:
        for prefijo in ("prometheus.googleapis.com/", "agent.googleapis.com/"):
            tipos = descubrir(tok, args.project, prefijo)
            print(f"{prefijo}  ->  {len(tipos)} tipos")
            for t in tipos:
                print("   ", t)
        return 0

    manifest = []
    ventanas = WINDOWS.items()
    if args.ventana:
        elegidas = set(args.ventana)
        ventanas = [(k, v) for k, v in WINDOWS.items() if k in elegidas]
        faltan = elegidas - {k for k, _ in ventanas}
        if faltan:
            print(f"ventanas desconocidas, ignoradas: {', '.join(sorted(faltan))}")
    for nombre, (inicio, fin) in ventanas:
        for mtype in APP_METRICS + ([] if args.sin_host else HOST_METRICS):
            slug = mtype.replace("/", "_")
            ext = "json" if mtype.endswith("/histogram") else "csv"
            destino = os.path.join(args.salida, nombre, f"{slug}.{ext}")
            try:
                ns, np, err = exportar(tok, args.project, mtype, inicio, fin, destino)
            except Exception as e:  # una métrica que falla no debe tumbar el volcado
                manifest.append({"ventana": nombre, "metrica": mtype, "error": str(e)})
                print(f"  [error] {nombre:<18}{mtype}")
                continue
            manifest.append({
                "ventana": nombre, "metrica": mtype,
                "inicio": inicio, "fin": fin,
                "alineacion": "ninguna (puntos en crudo)", "paginas": None,
                "series": ns, "puntos": np, "errores": err,
                "archivo": os.path.relpath(destino, args.salida),
                "comando": f'timeSeries?filter=metric.type="{mtype}"'
                           f'&interval.startTime={inicio}&interval.endTime={fin}'
                           '(sin agregacion)',
            })
            marca = "  " if err == 0 else f" ({err} paginas con error)"
            print(f"  {nombre:<18}{mtype:<62}{ns:>4} series {np:>7} pts{marca}")

    # Fusionar con lo ya exportado: lanzar una sola ventana no debe borrar el
    # manifiesto de las anteriores.
    ruta_man = os.path.join(args.salida, "manifest.json")
    previas = []
    if os.path.exists(ruta_man):
        try:
            previas = json.load(open(ruta_man)).get("entradas", [])
        except (ValueError, OSError):
            previas = []
    vistas = {(e["ventana"], e["metrica"]) for e in previas}
    manifest = previas + [e for e in manifest
                          if (e["ventana"], e["metrica"]) not in vistas]

    with open(ruta_man, "w") as f:
        json.dump(manifest, f, indent=1)
    print(f"\nmanifest -> {ruta_man} ({len(manifest)} entradas, "
          f"{len(manifest) - len(previas)} nuevas)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
