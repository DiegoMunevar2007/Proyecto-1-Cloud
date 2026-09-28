#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Gráficas del informe de capacidad.

Criterio: una idea por gráfica, un solo eje vertical, escala lineal y sin
normalizaciones. Cada figura tiene que entenderse sin leer el informe.

Las series se leen de docs/entrega2/evidencia/prometheus/<ventana>/*.csv. Cada CSV
trae timestamp, el diccionario de etiquetas en JSON y el valor; las etiquetas son
lo que permite separar por máquina y por estado de CPU, porque
cpu/utilization llega partido en guest, system, wait e idle.

    python3 graficas.py
"""

import csv
import datetime as dt
import json
import os
import re
import subprocess
import sys

import matplotlib
matplotlib.use("Agg")
import matplotlib.dates as mdates
import matplotlib.pyplot as plt
from matplotlib.ticker import FuncFormatter

RAIZ = os.path.dirname(os.path.abspath(__file__))
PROM = os.path.join(RAIZ, "..", "docs", "entrega2", "evidencia", "prometheus")
SALIDA = os.path.join(RAIZ, "graficas")

C_WEB, C_WORKER, C_VIEJO, C_LOADGEN = "#2f6f9f", "#c1573f", "#8c6bb1", "#8a8a8a"
C_PEND, C_REZAGO = "#2f6f9f", "#c1573f"
C_CLIENTE, C_SERVIDOR = "#c1573f", "#2f6f9f"

sep_miles = FuncFormatter(lambda v, _: f"{v:,.0f}".replace(",", "."))
plt.rcParams["timezone"] = "UTC"

INSTANCIAS = {}


def mapear_instancias():
    """id de instancia a nombre de máquina, para no poner números en la leyenda."""
    salida = subprocess.run(
        ["gcloud", "compute", "instances", "list",
         "--project=proyecto-2-cloud-509714", "--format=value(id,name)"],
        capture_output=True, text=True).stdout
    for linea in salida.strip().split("\n"):
        partes = linea.split()
        if len(partes) >= 2:
            INSTANCIAS[partes[0]] = partes[1]


def fecha(texto):
    """Marca de tiempo del volcado, con o sin fracción de segundo.

    Las métricas de compute.googleapis.com llegan como 2026-09-27T23:14:00Z y las
    de prometheus como 2026-09-27T23:14:33.057Z, así que el mismo formateador no
    sirve para las dos.
    """
    for formato in ("%Y-%m-%dT%H:%M:%SZ", "%Y-%m-%dT%H:%M:%S.%fZ"):
        try:
            return dt.datetime.strptime(texto, formato)
        except (ValueError, TypeError):
            continue
    return None


def leer(ventana, archivo):
    """[(datetime_utc, valor, etiquetas)] ordenada por tiempo."""
    ruta = os.path.join(PROM, ventana, archivo)
    if not os.path.exists(ruta):
        return []
    filas = []
    with open(ruta) as f:
        for r in csv.DictReader(f):
            t = fecha(r["timestamp"])
            if t is None:
                continue
            try:
                valor = float(r["value"])
            except (ValueError, TypeError):
                continue
            filas.append((t, valor, json.loads(r["labels"])))
    filas.sort(key=lambda x: x[0])
    return filas


def nombre_vm(etiquetas):
    bruto = etiquetas.get("vm") or etiquetas.get("resource_instance_id") or ""
    if "web" in bruto:
        return "Web Server"
    if "worker" in bruto:
        return "Worker Server"
    if "loadgen" in bruto:
        return "mooc-loadgen"
    if bruto in INSTANCIAS:
        return INSTANCIAS[bruto]
    if bruto.isdigit():
        return "Worker Server (anterior)"
    return bruto or "(desconocida)"


def color_vm(nombre):
    if nombre == "Web Server":
        return C_WEB
    if nombre == "Worker Server":
        return C_WORKER
    if nombre.startswith("Worker"):
        return C_VIEJO
    return C_LOADGEN


def cpu_por_vm(ventana):
    """{máquina: [(t, uso%)]} calculado como 100 menos el estado idle."""
    por_vm = {}
    for t, v, e in leer(ventana, "agent.googleapis.com_cpu_utilization.csv"):
        if e.get("cpu_state") != "idle":
            continue
        por_vm.setdefault(nombre_vm(e), []).append((t, 100.0 - v))
    return {k: sorted(v) for k, v in por_vm.items()}


def cpu_media(ventana, maquina, desde=None):
    """CPU media de una máquina en una ventana, opcionalmente desde un instante.

    La instancia del worker cambió a mitad de la sesión, así que en el escenario
    1 aparece como "Worker Server (anterior)". Se agrupa cualquier nombre que
    empiece por Worker para no dejarlo fuera del gráfico.
    """
    por_vm = cpu_por_vm(ventana)
    if maquina == "Worker Server":
        pts = [p for k, v in por_vm.items() if k.startswith("Worker") for p in v]
    else:
        pts = por_vm.get(maquina, [])
    if desde:
        pts = [(t, v) for t, v in pts if t >= desde]
    return sum(v for _, v in pts) / len(pts) if pts else 0.0


def simple(ventana, metrica, sufijo="_gauge.csv"):
    return [(t, v) for t, v, _ in leer(ventana, "prometheus.googleapis.com_" + metrica + sufijo)]


def memoria_worker(ventana):
    # memory/percent_used publica una serie por estado (used, cached, buffered,
    # slab, free); promediarlas no significa nada, solo importa used.
    return [(t, v) for t, v, e in leer(ventana, "agent.googleapis.com_memory_percent_used.csv")
            if nombre_vm(e) == "Worker Server" and e.get("state") == "used"]


def eje_tiempo(ax):
    ax.xaxis.set_major_locator(mdates.AutoDateLocator(minticks=5, maxticks=9))
    ax.xaxis.set_major_formatter(mdates.DateFormatter("%H:%M"))


def terminar(fig, ax_o_axs, subtitulo=None, fechas=True):
    """Cierra la figura: grilla, nota al pie y eje de tiempo si aplica.

    fechas=False para las gráficas de barras, cuyo eje x es categórico.
    """
    axs = ax_o_axs if isinstance(ax_o_axs, (list, tuple)) else [ax_o_axs]
    for ax in axs:
        if fechas:
            eje_tiempo(ax)
        ax.grid(alpha=0.22, axis="y")
    if subtitulo:
        fig.supxlabel(subtitulo, fontsize=8.5, color="#555")
    fig.tight_layout()


def texto_num(valor):
    """Número para poner encima de una barra, sin decimales si es entero.

    No se recortan ceros: 100 tiene que seguir siendo 100 y no 1.
    """
    return f"{valor:.0f}" if abs(valor - round(valor)) < 0.05 else f"{valor:.1f}"


def barras(ax, categorias, series, ancho=0.36, unidad="", alto=None):
    """Barras agrupadas, con el valor encima de cada una.

    series es una lista de (etiqueta, color, [valores]).
    """
    n = len(series)
    xs = list(range(len(categorias)))
    for i, (etiqueta, color, valores) in enumerate(series):
        pos = [x + (i - (n - 1) / 2) * ancho for x in xs]
        ax.bar(pos, valores, ancho, color=color, label=etiqueta)
        for x, v in zip(pos, valores):
            ax.text(x, v, texto_num(v), ha="center", va="bottom", fontsize=8)
    ax.set_xticks(xs)
    ax.set_xticklabels(categorias)
    ax.set_ylim(0, alto if alto else max(
        (max(v) for _, _, v in series), default=1) * 1.18)
    # Leyenda solo si hay más de una serie: con una sola barra no aporta nada.
    if any(etiqueta for etiqueta, _, _ in series):
        ax.legend(fontsize=8.5, framealpha=0.9)


def guardar(fig, nombre, titulo):
    fig.tight_layout()
    fig.savefig(os.path.join(SALIDA, nombre), dpi=150, bbox_inches="tight")
    plt.close(fig)
    print(f"  {nombre:<40} {titulo}")


# ---------------------------------------------------------------------------

def g1_cpu_por_corrida():
    """Una barra por máquina y por corrida: quién se satura y cuándo."""
    # El drenaje se mide desde las 19:30, que es cuando el sistema queda en
    # régimen después del reinicio; antes de eso el worker estaba apagado y su
    # media no describe la corrida.
    corte = dt.datetime(2026, 9, 27, 19, 30)
    ventanas = [("curva-esc1", "Escenario 1\nacadémico", None),
                ("curva-esc2", "Escenario 2\ncarga", None),
                ("drenaje", "Escenario 2\ndrenaje", corte)]
    maquinas = ["Web Server", "Worker Server"]
    categorias = [t for _, t, _ in ventanas]
    series = [(m, color_vm(m), [cpu_media(v, m, d) for v, _, d in ventanas]) for m in maquinas]

    fig, ax = plt.subplots(figsize=(8.5, 4.3))
    barras(ax, categorias, series, alto=112)
    ax.set_ylabel("CPU media en uso (%)")
    ax.set_title("CPU media por máquina y por corrida", fontsize=11)
    terminar(fig, ax, "En el escenario 1 ninguna máquina pasa del 10 %. En el escenario 2 el Worker "
                      "Server llega al 84 % y al 99 % mientras la API queda por debajo del 3 %.",
             fechas=False)
    return fig


def g2_cpu_t1():
    """La API falló con el Web Server casi ocioso."""
    datos = cpu_por_vm("escalada-t1")
    if not datos:
        return None
    orden = [m for m in ("Web Server", "Worker Server") if m in datos]
    medias = [sum(v for _, v in datos[m]) / len(datos[m]) for m in orden]
    fig, ax = plt.subplots(figsize=(6.5, 4.0))
    ax.bar(orden, medias, width=0.5, color=[color_vm(m) for m in orden])
    for i, v in enumerate(medias):
        ax.text(i, v, texto_num(v), ha="center", va="bottom", fontsize=9)
    ax.set_ylim(0, 114)
    ax.grid(alpha=0.22, axis="y")
    ax.set_ylabel("CPU media en uso (%)")
    ax.set_title("CPU durante la escalada T1", fontsize=11)
    terminar(fig, ax, "La API devolvió 56 errores 500 por falta de conexiones a PostgreSQL "
                      "con el Web Server al 7,6 % de CPU.", fechas=False)
    return fig


def g3_cola_carga():
    """La cola crece durante la carga mientras el worker no da abasto."""
    p = simple("curva-esc2", "mooc_queue_pending")
    if not p:
        return None
    fig, ax = plt.subplots(figsize=(8.5, 4.3))
    ax.plot([t for t, _ in p], [v for _, v in p], lw=1.9, color=C_PEND)
    ax.set_ylim(0, max(v for _, v in p) * 1.15)
    ax.yaxis.set_major_formatter(sep_miles)
    ax.set_ylabel("trabajos pendientes")
    ax.set_xlabel("hora (UTC)")
    tmax, vmax = max(p, key=lambda x: x[1])
    ax.annotate(f"{vmax:.0f}", xy=(tmax, vmax), xytext=(tmax, vmax + 45),
                ha="right", fontsize=9, color="#333")
    ax.set_title("Cola durante la carga del escenario 2", fontsize=11)
    terminar(fig, ax, f"Los tres niveles de carga llevan la cola de 0 a {vmax:.0f} trabajos pendientes.")
    return fig


def g4_drenaje():
    """El rezago apenas baja mientras se procesan los trabajos."""
    corte = dt.datetime(2026, 9, 27, 19, 30)
    p = [(t, v) for t, v in simple("drenaje", "mooc_queue_size") if t >= corte]
    if not p:
        return None
    primera, ultima = p[0], p[-1]
    minutos = (ultima[0] - primera[0]).total_seconds() / 60
    tasa = (primera[1] - ultima[1]) / minutos
    fig, ax = plt.subplots(figsize=(8.5, 4.3))
    ax.plot([t for t, _ in p], [v for _, v in p], lw=1.9, color=C_REZAGO)
    ax.set_ylim(0, max(v for _, v in p) * 1.15)
    ax.yaxis.set_major_formatter(sep_miles)
    ax.set_ylabel("trabajos en la cola")
    ax.set_xlabel("hora (UTC)")
    ax.annotate(f"{tasa:.2f} trabajos/min", xy=(ultima[0], ultima[1]),
                xytext=(ultima[0] - dt.timedelta(minutes=12), ultima[1] + 12),
                fontsize=9, color="#333",
                arrowprops=dict(arrowstyle="->", color="#888", lw=0.9))
    ax.set_title("Drenaje de la cola", fontsize=11)
    terminar(fig, ax, f"El rezago baja de {primera[1]:.0f} a {ultima[1]:.0f} en {minutos:.0f} minutos "
                      "con cuatro transcodificaciones simultáneas.")
    return fig


def g5_consumo():
    """Los dos patrones de consumo no se parecen en nada."""
    base = os.path.join(RAIZ, "results")
    datos = {}
    for patron in ("reproduccion", "burst"):
        ruta = os.path.join(base, f"esc2-consumo-{patron}-summary.json")
        if os.path.exists(ruta):
            with open(ruta) as f:
                m = json.load(f)["metrics"]
            datos[patron] = int(m["hls_segment_ok_total"]["count"])
    if not datos:
        return None
    patrones = list(datos)
    valores = [datos[p] for p in patrones]
    fig, ax = plt.subplots(figsize=(6.5, 4.0))
    ax.bar(patrones, valores, width=0.5, color=C_PEND)
    for i, v in enumerate(valores):
        ax.text(i, v, texto_num(v), ha="center", va="bottom", fontsize=9)
    ax.set_ylim(0, max(valores) * 1.18)
    ax.grid(alpha=0.22, axis="y")
    ax.set_ylabel("segmentos descargados")
    ax.set_title("Consumo HLS por patrón", fontsize=11)
    terminar(fig, ax, "La descarga masiva mueve 27 veces más segmentos que la cadencia de "
                      "reproducción. Ninguno de los dos registra errores.", fechas=False)
    return fig


def g6_ram_worker():
    """La memoria del worker se queda lejos del límite."""
    d = memoria_worker("drenaje")
    if not d:
        return None
    fig, ax = plt.subplots(figsize=(8.5, 4.0))
    ax.plot([t for t, _ in d], [v for _, v in d], lw=1.9, color=C_WORKER)
    ax.axhline(100, color="#c00", ls=":", lw=1)
    ax.set_ylim(0, 112)
    ax.set_ylabel("memoria usada (%)")
    ax.set_xlabel("hora (UTC)")
    ax.set_title("Memoria del Worker Server durante el drenaje", fontsize=11)
    terminar(fig, ax, f"Entre {min(v for _, v in d):.1f} % y {max(v for _, v in d):.1f} % de los 1976 MB. "
                      "La corrección del OOM se sostiene.")
    return fig


_LOG_GIN = os.path.join(RAIZ, "..", "docs", "entrega2", "evidencia", "api-gin.log")
_IP_GENERADOR = "186.29.35.28"
_RE_GIN = re.compile(
    r'\[GIN\] (\S+) - (\d\d:\d\d:\d\d) \|\s*(\d+) \|\s*([0-9.]+)(µs|ms|s) \|'
    r'\s*(\S+) \|\s*(\S+)\s+"([^"]*)"')
NIVELES_ESC1 = {
    "L0": ("03:12:48", "03:17:07"), "L1": ("03:17:09", "03:21:58"),
    "L2": ("03:21:59", "03:26:47"), "L3": ("03:36:58", "03:42:02"),
    "L4": ("03:31:53", "03:36:56"),
}


def _a_ms(valor, unidad):
    valor = float(valor)
    return valor / 1000 if unidad == "µs" else (valor * 1000 if unidad == "s" else valor)


def latencia_servidor_por_nivel():
    """p95 del lado del servidor por nivel, leído del log de acceso de Gin."""
    acum = {k: [] for k in NIVELES_ESC1}
    if not os.path.exists(_LOG_GIN):
        return {}
    for linea in open(_LOG_GIN, errors="ignore"):
        m = _RE_GIN.search(linea)
        if not m:
            continue
        _, hora, status, dur, unidad, ip, _met, _ruta = m.groups()
        if ip != _IP_GENERADOR or not (200 <= int(status) < 300):
            continue
        for nivel, (a, b) in NIVELES_ESC1.items():
            if a <= hora <= b:
                acum[nivel].append(_a_ms(dur, unidad))
                break
    salida = {}
    for k, v in acum.items():
        if v:
            v.sort()
            salida[k] = v[min(len(v) - 1, int(round(0.95 * (len(v) - 1))))]
    return salida


def g7_latencia_esc1():
    """La latencia que ve el usuario es casi toda red, no trabajo del servidor."""
    servidor = latencia_servidor_por_nivel()
    base = os.path.join(RAIZ, "results")
    cliente = {}
    for n in ("L0", "L1", "L2", "L3", "L4"):
        ruta = os.path.join(base, f"esc1-{n}-summary.json")
        if os.path.exists(ruta):
            with open(ruta) as f:
                cliente[n] = json.load(f)["metrics"].get("http_req_duration", {}).get("p(95)", 0)
    niveles = [n for n in ("L0", "L1", "L2", "L3", "L4") if n in cliente]
    if not niveles:
        return None
    fig, ax = plt.subplots(figsize=(8.5, 4.3))
    barras(ax, niveles, [
        ("medida en el cliente", C_CLIENTE, [cliente[n] for n in niveles]),
        ("medida en el servidor", C_SERVIDOR, [servidor.get(n, 0) for n in niveles]),
    ])
    ax.set_ylabel("latencia p95 (ms)")
    ax.set_title("Latencia p95 por nivel de carga", fontsize=11)
    terminar(fig, ax, "El cliente mide entre seis y ocho veces lo que mide el servidor. La diferencia "
                      "es el piso de red del portátil, no trabajo de la aplicación.", fechas=False)
    return fig


def main():
    os.makedirs(SALIDA, exist_ok=True)
    mapear_instancias()
    print(f"generando graficas en {SALIDA}\n")
    tareas = [
        (g1_cpu_por_corrida, "01-cpu-por-corrida.png", "CPU media por máquina y corrida"),
        (g2_cpu_t1, "02-cpu-t1.png", "CPU durante la escalada T1"),
        (g3_cola_carga, "03-cola-carga-esc2.png", "Cola durante la carga del escenario 2"),
        (g4_drenaje, "04-drenaje-cola.png", "Drenaje de la cola"),
        (g5_consumo, "05-consumo-hls.png", "Consumo HLS por patrón"),
        (g6_ram_worker, "06-ram-worker.png", "Memoria del Worker Server"),
        (g7_latencia_esc1, "07-latencia-esc1.png", "Latencia p95 por nivel"),
    ]
    for fn, nombre, titulo in tareas:
        fig = fn()
        if fig is None:
            print(f"  {nombre:<40} (sin datos)")
            continue
        guardar(fig, nombre, titulo)
    return 0


if __name__ == "__main__":
    sys.exit(main())
