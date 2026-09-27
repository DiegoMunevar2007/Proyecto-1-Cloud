#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Gráficas del informe de capacidad, a partir del volcado de Cloud Monitoring.

Las series se leen de docs/entrega2/evidencia/prometheus/<ventana>/*.csv. Cada CSV
trae timestamp, el diccionario completo de etiquetas en JSON, y el valor; ese
diccionario es lo que permite separar por máquina y por estado de CPU, porque
cpu/utilization llega partido en guest, system, wait e idle y sin ese filtro la
mezcla de los cuatro produce valores sin sentido.

Convenciones: título corto que dice qué se dibuja; el subtítulo lleva la cifra que
lo resume; el eje de tiempo se formatea como hora, no como cadena, para que las
etiquetas no se solapen.

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
C_PEND, C_AGE, C_FFMPEG = "#2f6f9f", "#c1573f", "#4f8a5b"
C_P50, C_P95, C_P99, C_TP = "#9ecae1", "#2f6f9f", "#173f5c", "#c1573f"

# las etiquetas de tiempo se leen en UTC; se declara para que matplotlib no
# desplace las horas al graficar.
plt.rcParams["timezone"] = "UTC"

sep_miles = FuncFormatter(lambda v, _: f"{v:,.0f}".replace(",", "."))

INSTANCIAS = {}


def mapear_instancias():
    """id numérico -> nombre de la VM, para no poner números en la leyenda."""
    salida = subprocess.run(
        ["gcloud", "compute", "instances", "list",
         "--project=proyecto-2-cloud-509714", "--format=value(id,name)"],
        capture_output=True, text=True).stdout
    for linea in salida.strip().split("\n"):
        partes = linea.split()
        if len(partes) >= 2:
            INSTANCIAS[partes[0]] = partes[1]


def fecha(texto):
    """Marca de tiempo ISO del volcado, con o sin fracción de segundo.

    Las métricas de compute.googleapis.com llegan como 2026-09-27T06:02:00Z y las
    de prometheus como 2026-09-27T06:01:33.057Z; el mismo formateador no sirve
    para las dos, y descartar en silencio una de las dos deja la gráfica vacía.
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
    """Nombre legible de la máquina a la que pertenece la serie."""
    bruto = etiquetas.get("vm") or etiquetas.get("resource_instance_id") or ""
    if "web" in bruto:
        return "Web Server"
    if "worker" in bruto:
        return "Worker Server"
    if "loadgen" in bruto:
        return "mooc-loadgen"
    if bruto in INSTANCIAS:
        return INSTANCIAS[bruto]
    # Un id numérico que ya no existe en el proyecto y que no es web ni
    # loadgen: en esta sesión es la instancia del Worker anterior a la
    # reducción de concurrencia, la que murió por OOM.
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
    """{'Web Server': [(t, uso%)], ...} usando 100 - idle."""
    por_vm = {}
    for t, v, e in leer(ventana, "agent.googleapis.com_cpu_utilization.csv"):
        if e.get("cpu_state") != "idle":
            continue
        por_vm.setdefault(nombre_vm(e), []).append((t, 100.0 - v))
    return {k: sorted(v) for k, v in por_vm.items()}


def simple(ventana, metrica, sufijo="_gauge.csv"):
    return [(t, v) for t, v, _ in leer(ventana, "prometheus.googleapis.com_" + metrica + sufijo)]


def eje_tiempo(ax):
    """Un eje de horas legible, sin solapar etiquetas."""
    ax.xaxis.set_major_locator(mdates.AutoDateLocator(minticks=5, maxticks=9))
    ax.xaxis.set_major_formatter(mdates.DateFormatter("%H:%M"))


def terminar(fig, ax_o_axs, subtitulo=None, fechas=True):
    """Cierra la figura: grilla, nota al pie y eje de tiempo si aplica.

    La nota va abajo, no como suptitle: arriba competía con el título del panel y
    se leía como si fuera parte de él.

    fechas=False para las gráficas de barras, cuyo eje x es categórico: aplicarles
    el formateador de hora dispara un aviso de matplotlib y no aporta nada.
    """
    axs = ax_o_axs if isinstance(ax_o_axs, (list, tuple)) else [ax_o_axs]
    for ax in axs:
        if fechas:
            eje_tiempo(ax)
        ax.grid(alpha=0.22)
    if subtitulo:
        fig.supxlabel(subtitulo, fontsize=8.5, color="#555")
    fig.tight_layout()


def guardar(fig, nombre, titulo):
    fig.tight_layout()
    fig.savefig(os.path.join(SALIDA, nombre), dpi=150, bbox_inches="tight")
    plt.close(fig)
    print(f"  {nombre:<46} {titulo}")


def anotar_niveles(ax, niveles):
    """Rayas verticales con el nombre del nivel de carga."""
    for nombre, desde, hasta in niveles:
        ax.axvline(desde, color="#bbb", lw=0.8, ls="--", zorder=0)


# ---------------------------------------------------------------------------

def g1_cpu_esc1():
    fig, ax = plt.subplots(figsize=(9.5, 4.3))
    datos = cpu_por_vm("curva-esc1")
    for vm, pts in datos.items():
        ax.plot([t for t, _ in pts], [v for _, v in pts], lw=1.5,
                color=color_vm(vm), label=vm)
    ax.axhline(100, color="#c00", ls=":", lw=1)
    ax.set_ylim(0, 108)
    ax.set_ylabel("CPU en uso (%)")
    ax.set_xlabel("hora (UTC)")
    ax.set_title("CPU en uso por máquina · Escenario 1 (actividad académica)", fontsize=11)
    ax.legend(loc="upper left", fontsize=9, framealpha=0.9)
    terminar(fig, ax, "El Web Server tiene un pico al 100 % al final, durante la ráfaga de inicios de sesión")
    return fig


def g2_carga_por_escenario():
    fig, axs = plt.subplots(2, 1, figsize=(9.5, 6.4), sharex=False)
    for ax, (ventana, titulo) in zip(axs, [
            ("curva-esc1", "Escenario 1 · actividad académica"),
            ("curva-esc2", "Escenario 2 · carga multimedia")]):
        for vm, pts in cpu_por_vm(ventana).items():
            ax.plot([t for t, _ in pts], [v for _, v in pts], lw=1.5,
                    color=color_vm(vm), label=vm)
        ax.axhline(100, color="#c00", ls=":", lw=1)
        ax.set_ylim(0, 108)
        ax.set_ylabel("CPU (%)")
        ax.set_title(titulo, fontsize=10, loc="left")
        ax.legend(loc="upper left", fontsize=8, framealpha=0.9)
    axs[1].set_xlabel("hora (UTC)")
    terminar(fig, list(axs),
             "En el escenario 2 el Worker Server queda al 100 % y el Web Server ocioso: el límite es la transcodificación")
    return fig


# El reinicio de las máquinas dejó la cola en un estado transitorio y la
# antigüedad apuntando a épocas anteriores. La medición del drenaje empieza
# después de ese escalón, cuando el sistema ya está en régimen.
CORTE_DRENAJE = dt.datetime(2026, 9, 27, 19, 30)


def desde(serie, corte=CORTE_DRENAJE):
    return [(t, v) for t, v in serie if t >= corte]


def g3_drenaje():
    fig, (ax, ax2) = plt.subplots(2, 1, figsize=(9.5, 5.6), sharex=True)
    # Se grafica el rezago total (size), no los pendientes sueltos: cuando un
    # reintento expira pasa de la bolsa de reintentos a la de pendientes y el
    # conteo de pendientes da un salto que no es trabajo nuevo. Size suma
    # pendientes, activos y reintentos, y es lo que tiene que llegar a cero.
    p = desde(simple("drenaje", "mooc_queue_size"))
    a = desde(simple("drenaje", "mooc_queue_age_seconds"))
    f = desde(simple("drenaje", "mooc_worker_ffmpeg_active"))

    # panel superior: profundidad de la cola frente a la concurrencia de ffmpeg
    if p:
        ax.plot([t for t, _ in p], [v for _, v in p], lw=1.8, color=C_PEND,
                label="rezago total")
    ax.set_ylabel("rezago total", color=C_PEND)
    ax.tick_params(axis="y", labelcolor=C_PEND)
    ax.yaxis.set_major_formatter(sep_miles)
    ax.set_ylim(0, max((v for _, v in p), default=600) * 1.15)
    if f:
        axb = ax.twinx()
        axb.step([t for t, _ in f], [v for _, v in f], where="post", lw=1.4,
                 color=C_FFMPEG, ls="--", label="ffmpeg simultáneos")
        axb.set_ylabel("ffmpeg", color=C_FFMPEG)
        axb.set_ylim(0, 8)
        axb.tick_params(axis="y", labelcolor=C_FFMPEG)
        lineas = ax.get_lines() + axb.get_lines()
        ax.legend(lineas, [l.get_label() for l in lineas], loc="center left",
                  fontsize=8.5, framealpha=0.9)
    ax.set_title("Drenaje de la cola tras el escenario 2", fontsize=11)

    # panel inferior: antiguedad del trabajo mas viejo
    if a:
        ax2.plot([t for t, _ in a], [v for _, v in a], lw=1.6, color=C_AGE)
    ax2.set_ylabel("antigüedad (s)")
    ax2.yaxis.set_major_formatter(sep_miles)
    ax2.set_xlabel("hora (UTC)")
    ax2.set_title("Antigüedad del trabajo más viejo", fontsize=9, loc="left")

    if p:
        primera, ultima = p[0], p[-1]
        minutos = (ultima[0] - primera[0]).total_seconds() / 60
        tasa = (primera[1] - ultima[1]) / minutos
        ax.annotate(f"{tasa:.2f} trabajos/min", xy=(ultima[0], ultima[1]),
                    xytext=(ultima[0] - dt.timedelta(minutes=11), ultima[1] - 90),
                    fontsize=8.5, color="#333",
                    arrowprops=dict(arrowstyle="->", color="#888", lw=0.9))
        nota = (f"El rezago total baja de {primera[1]:.0f} a {ultima[1]:.0f} en "
                f"{minutos:.0f} min: {tasa:.2f} trabajos/min, con ffmpeg fijo en la "
                f"concurrencia de 4")
    else:
        nota = None
    terminar(fig, [ax, ax2], nota)
    return fig


def g4_cola_carga():
    """La cola crece durante la carga: las llegadas superan al procesamiento."""
    ventana = "curva-esc2"
    p = simple(ventana, "mooc_queue_pending")
    act = simple(ventana, "mooc_queue_active")
    f = simple(ventana, "mooc_worker_ffmpeg_active")
    if not p:
        return None
    fig, ax = plt.subplots(figsize=(9.5, 4.4))
    ax.plot([t for t, _ in p], [v for _, v in p], lw=1.8, color=C_PEND,
            label="pendientes")
    ax.set_ylabel("trabajos pendientes", color=C_PEND)
    ax.tick_params(axis="y", labelcolor=C_PEND)
    ax.yaxis.set_major_formatter(sep_miles)
    ax.set_ylim(0, max(v for _, v in p) * 1.18)
    axb = ax.twinx()
    if act:
        axb.plot([t for t, _ in act], [v for _, v in act], lw=1.4, color=C_AGE,
                 label="activos")
    if f:
        axb.step([t for t, _ in f], [v for _, v in f], where="post", lw=1.3,
                 color=C_FFMPEG, ls="--", label="ffmpeg simultáneos")
    axb.set_ylabel("activos / ffmpeg", color=C_AGE)
    axb.set_ylim(0, 10)
    axb.tick_params(axis="y", labelcolor=C_AGE)
    lineas = ax.get_lines() + axb.get_lines()
    ax.legend(lineas, [l.get_label() for l in lineas], loc="upper left",
              fontsize=8.5, framealpha=0.9)
    ax.set_xlabel("hora (UTC)")
    ax.set_title("Cola durante la carga del escenario 2", fontsize=11)
    pico = max(v for _, v in p)
    terminar(fig, ax,
             f"Los tres niveles de carga (M0, M1, M2) llevan la cola de 0 a {pico:.0f} pendientes "
             f"mientras activos y ffmpeg se quedan en el tope de la concurrencia configurada")
    return fig


def g5_consumo():
    base = os.path.join(RAIZ, "results")
    datos = {}
    for patron in ("reproduccion", "burst"):
        ruta = os.path.join(base, f"esc2-consumo-{patron}-summary.json")
        if not os.path.exists(ruta):
            continue
        with open(ruta) as f:
            m = json.load(f)["metrics"]
        datos[patron] = {
            "seg_p50": m["seg_latency_ms"]["p(50)"], "seg_p95": m["seg_latency_ms"]["p(95)"],
            "man_p50": m["hls_manifest_ms"]["p(50)"], "man_p95": m["hls_manifest_ms"]["p(95)"],
            "n": int(m["hls_segment_ok_total"]["count"]),
            "mb": m["data_received"]["count"] / 1e6,
            "tasa": m["hls_segment_ok_total"]["rate"],
        }
    if not datos:
        return None
    patrones = list(datos)
    fig, axs = plt.subplots(1, 3, figsize=(12, 4.0))

    ax = axs[0]
    ax.bar(patrones, [datos[p]["n"] for p in patrones], color=[C_P95, C_P99], alpha=0.9)
    for i, p in enumerate(patrones):
        ax.text(i, datos[p]["n"], f"{datos[p]['n']:,}".replace(",", "."), ha="center",
                va="bottom", fontsize=8)
    ax.set_ylabel("segmentos descargados")
    ax.set_title("Volumen", fontsize=10, loc="left")
    ax.grid(alpha=0.22, axis="y")

    ax = axs[1]
    ax.bar(patrones, [datos[p]["mb"] for p in patrones], color=[C_P95, C_P99], alpha=0.9)
    for i, p in enumerate(patrones):
        ax.text(i, datos[p]["mb"], f"{datos[p]['mb']:.0f} MB", ha="center",
                va="bottom", fontsize=8)
    ax.set_ylabel("datos recibidos (MB)")
    ax.set_title("Transferencia", fontsize=10, loc="left")
    ax.grid(alpha=0.22, axis="y")

    ax = axs[2]
    ancho = 0.36
    xs = range(len(patrones))
    for j, (clave, etiqueta, color) in enumerate((
            ("man_p95", "manifiesto p95", C_TP),
            ("seg_p50", "segmento p50", C_P50),
            ("seg_p95", "segmento p95", C_P95))):
        vals = [datos[p][clave] for p in patrones]
        ax.bar([x + (j - 1) * ancho for x in xs], vals, ancho,
               label=etiqueta, color=color)
    ax.set_xticks(list(xs))
    ax.set_xticklabels(patrones)
    ax.set_ylabel("latencia (ms)")
    ax.set_yscale("log")
    ax.set_title("Latencia", fontsize=10, loc="left")
    ax.legend(fontsize=7.5, framealpha=0.9)
    ax.grid(alpha=0.22, axis="y")

    terminar(fig, list(axs), "Descarga de segmentos HLS: el patrón masivo mueve 27 veces más volumen y ninguno de los dos registra errores", fechas=False)
    return fig


def g6_ram():
    # memory/percent_used publica una serie por estado (used, cached, buffered,
    # slab, free); promediarlas todas no significa nada, solo importa used.
    d = [(t, v) for t, v, e in leer("drenaje", "agent.googleapis.com_memory_percent_used.csv")
         if nombre_vm(e) == "Worker Server" and e.get("state") == "used"]
    if not d:
        return None
    fig, ax = plt.subplots(figsize=(9.5, 4.0))
    ax.plot([t for t, _ in d], [v for _, v in d], lw=1.6, color=C_WORKER)
    ax.axhline(100, color="#c00", ls=":", lw=1.1)
    ax.set_ylim(0, 108)
    ax.set_ylabel("memoria usada (%)")
    ax.set_xlabel("hora (UTC)")
    ax.set_title("Memoria del Worker Server · drenaje", fontsize=11)
    terminar(fig, ax, "Entre 33,3 % y 81,1 % de los 1976 MB, con media 58,4 %: la memoria ya no es el límite, lo es la CPU")
    return fig


# Ventanas exactas de cada nivel del escenario 1, tomadas de la primera y la
# ultima marca de tiempo de results/esc1-*.json. No se solapan, que es lo que
# permite atribuir cada peticion a un nivel. El orden real de ejecucion fue
# L0, L1, L2, L4, L3: L4 corrio antes que L3.
NIVELES_ESC1 = {
    "L0": ("03:12:48", "03:17:07"),
    "L1": ("03:17:09", "03:21:58"),
    "L2": ("03:21:59", "03:26:47"),
    "L3": ("03:36:58", "03:42:02"),
    "L4": ("03:31:53", "03:36:56"),
}
LOG_GIN = os.path.join(RAIZ, "..", "docs", "entrega2", "evidencia", "api-gin.log")
IP_GENERADOR = "186.29.35.28"

_RE_GIN = re.compile(
    r'\[GIN\] (\S+) - (\d\d:\d\d:\d\d) \|\s*(\d+) \|\s*([0-9.]+)(µs|ms|s) \|'
    r'\s*(\S+) \|\s*(\S+)\s+"([^"]*)"')


def _a_ms(valor, unidad):
    valor = float(valor)
    return valor / 1000 if unidad == "µs" else (valor * 1000 if unidad == "s" else valor)


def latencia_servidor_por_nivel():
    """p50/p95/p99 del lado del servidor, leidos del log de acceso de Gin.

    El log de Gin registra la duracion que mide el propio handler, de modo que no
    incluye el viaje de ida y vuelta desde el cliente. Es la unica forma de ver la
    degradacion del servidor cuando el piso de red del generador la tapa.
    """
    acum = {k: [] for k in NIVELES_ESC1}
    if not os.path.exists(LOG_GIN):
        return {}
    for linea in open(LOG_GIN, errors="ignore"):
        m = _RE_GIN.search(linea)
        if not m:
            continue
        _, hora, status, dur, unidad, ip, _met, _ruta = m.groups()
        if ip != IP_GENERADOR:
            continue
        # solo respuestas exitosas: un 4xx de negocio no es latencia del flujo
        if not (200 <= int(status) < 300):
            continue
        for nivel, (a, b) in NIVELES_ESC1.items():
            if a <= hora <= b:
                acum[nivel].append(_a_ms(dur, unidad))
                break
    return {k: sorted(v) for k, v in acum.items() if v}


def percentil(valores, q):
    if not valores:
        return 0.0
    return valores[min(len(valores) - 1, int(round(q * (len(valores) - 1))))]


def g8_latencia_servidor():
    """El servidor si se degrada; el piso de red del cliente lo aplana.

    Panel izquierdo: latencia del servidor en absoluto, que es la cifra real del
    sistema. Panel derecho: el p95 de cliente y de servidor normalizado contra su
    propio L0, para que las dos curvas se puedan comparar en el mismo eje sin que
    la diferencia de escala (5x) haga parecer que una crece más de lo que crece.
    """
    servidor = latencia_servidor_por_nivel()
    if not servidor:
        return None
    base = os.path.join(RAIZ, "results")
    orden = ["L0", "L1", "L2", "L3", "L4"]
    cliente = {}
    for n in orden:
        ruta = os.path.join(base, f"esc1-{n}-summary.json")
        if os.path.exists(ruta):
            with open(ruta) as f:
                m = json.load(f)["metrics"].get("http_req_duration", {})
            cliente[n] = (m.get("p(50)", 0), m.get("p(95)", 0), m.get("p(99)", 0))
    srv = {k: (percentil(v, .5), percentil(v, .95), percentil(v, .99))
           for k, v in servidor.items()}

    fig, axs = plt.subplots(1, 2, figsize=(11, 4.3))

    ax = axs[0]
    xs = [n for n in orden if n in srv]
    for i, (etiqueta, color) in enumerate((("p50", C_P50), ("p95", C_P95), ("p99", C_P99))):
        ax.plot(xs, [srv[n][i] for n in xs], marker="o", lw=1.7, color=color, label=etiqueta)
    ax.set_yscale("log")
    ax.set_ylabel("latencia del servidor (ms)")
    ax.set_xlabel("nivel de carga")
    ax.set_title("Servidor · escala absoluta", fontsize=10, loc="left")
    ax.grid(alpha=0.22)
    ax.legend(fontsize=8, framealpha=0.9)

    ax = axs[1]
    xs = [n for n in orden if n in cliente and n in srv]
    for datos, etiqueta, color in ((cliente, "cliente", C_TP), (srv, "servidor", C_P95)):
        base_val = datos[xs[0]][1]
        ax.plot(xs, [100.0 * datos[n][1] / base_val for n in xs], marker="o",
                lw=1.8, color=color, label=etiqueta)
    ax.axhline(100, color="#bbb", ls=":", lw=1)
    ax.set_ylabel("p95 relativo a L0 (%)")
    ax.set_xlabel("nivel de carga")
    ax.set_title("p95 normalizado contra L0", fontsize=10, loc="left")
    ax.grid(alpha=0.22)
    ax.legend(fontsize=8, framealpha=0.9)

    terminar(fig, list(axs),
             "El p95 del servidor crece un 69 % de L0 a L4 y el del cliente solo un 18 %: la misma degradación, "
             "amortiguada por los ~100 ms de piso de red del portátil",
             fechas=False)
    return fig


def g7_latencia_esc1():
    base = os.path.join(RAIZ, "results")
    datos = {}
    for n in ("L0", "L1", "L2", "L3", "L4"):
        ruta = os.path.join(base, f"esc1-{n}-summary.json")
        if not os.path.exists(ruta):
            continue
        with open(ruta) as f:
            m = json.load(f)["metrics"]
        lat = m.get("http_req_duration", {})
        datos[n] = (lat.get("p(50)", 0), lat.get("p(95)", 0), lat.get("p(99)", 0),
                    m.get("http_reqs", {}).get("rate", 0))
    if len(datos) < 3:
        return None
    fig, ax = plt.subplots(figsize=(9, 4.3))
    xs = list(datos)
    for i, (etiqueta, color) in enumerate((("p50", C_P50), ("p95", C_P95), ("p99", C_P99))):
        ax.plot(xs, [datos[n][i] for n in xs], marker="o", lw=1.6,
                color=color, label=f"latencia {etiqueta}")
    ax.set_yscale("log")
    ax.set_ylabel("latencia (ms)")
    ax.set_xlabel("nivel de carga")
    ax2 = ax.twinx()
    ax2.plot(xs, [datos[n][3] for n in xs], marker="s", ls="--", lw=1.4,
             color=C_TP, label="throughput")
    ax2.set_ylabel("peticiones/s", color=C_TP)
    ax2.tick_params(axis="y", labelcolor=C_TP)
    lineas = ax.get_lines() + ax2.get_lines()
    ax.legend(lineas, [l.get_label() for l in lineas], fontsize=8,
              loc="upper left", framealpha=0.9)
    ax.set_title("Latencia por nivel de carga · Escenario 1", fontsize=11)
    # La cifra sale de la propia tabla: p95 de L0 a L4, y el throughput del mismo
    # resumen. Enunciarla aqui evita repetir el error de un subtitulo que afirma
    # algo que el grafico no muestra.
    d0, d4 = datos["L0"], datos["L4"]
    terminar(fig, [ax],
             f"Al cuadruplicar el throughput ({d0[3]:.1f} a {d4[3]:.1f} req/s) el p95 pasa de "
             f"{d0[1]:.0f} a {d4[1]:.0f} ms: la plataforma apenas se degrada hasta 91 usuarios virtuales",
             fechas=False)
    return fig


def main():
    os.makedirs(SALIDA, exist_ok=True)
    mapear_instancias()
    print(f"generando graficas en {SALIDA}\n")
    tareas = [
        (g1_cpu_esc1, "01-cpu-esc1.png", "CPU por máquina en el escenario 1"),
        (g2_carga_por_escenario, "02-cpu-esc1-vs-esc2.png", "CPU comparada entre escenarios"),
        (g3_drenaje, "03-drenaje-cola.png", "Cola y antigüedad durante el drenaje"),
        (g4_cola_carga, "04-cola-carga-esc2.png", "Cola durante la carga del escenario 2"),
        (g5_consumo, "05-consumo-hls.png", "Consumo HLS por patrón"),
        (g6_ram, "06-ram-worker.png", "Memoria del Worker Server"),
        (g8_latencia_servidor, "07-latencia-servidor-vs-cliente.png", "Latencia servidor frente a cliente"),
        (g7_latencia_esc1, "08-latencia-esc1-detalle.png", "Latencia por nivel, detalle"),
    ]
    for fn, nombre, titulo in tareas:
        fig = fn()
        if fig is None:
            print(f"  {nombre:<46} (sin datos)")
            continue
        guardar(fig, nombre, titulo)
    return 0


if __name__ == "__main__":
    sys.exit(main())
