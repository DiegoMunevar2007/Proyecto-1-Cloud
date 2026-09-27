#!/usr/bin/env bash
# Instala y configura el Ops Agent de Google Cloud en la VM.
#
# El agente hace dos cosas: reporta las métricas del host (CPU, memoria, disco,
# red) y scrapea el /metrics del contenedor de aplicación para enviarlo a
# Cloud Monitoring. No hay un servidor Prometheus en el despliegue: el agente es
# el recolector, y por eso no compite por los 2 vCPU de la VM que se está midiendo.
#
# Uso: install_ops_agent.sh <scrape-target> [scrape-interval]
#   install_ops_agent.sh 127.0.0.1:8080 30s
#
# Es best effort a propósito: si el agente no se puede instalar, la aplicación
# debe quedar desplegada igual y sin telemetría, no abortar el arranque.
set -uo pipefail

TARGET="${1:-}"
INTERVAL="${2:-30s}"
CONFIG_DIR=/etc/google-cloud-ops-agent
CONFIG_FILE="${CONFIG_DIR}/config.yaml"
AGENT_PKG=google-cloud-ops-agent

log()  { echo "==> [ops-agent] $*"; }
warn() { echo "AVISO [ops-agent] $*" >&2; }

if [[ -z "${TARGET}" ]]; then
	warn "falta el objetivo de scrape; no se instala el agente"
	exit 0
fi

# ---------------------------------------------------------------------------
# Instalación
# ---------------------------------------------------------------------------
if dpkg-query --show "${AGENT_PKG}" >/dev/null 2>&1; then
	log "${AGENT_PKG} ya está instalado"
else
	workdir="$(mktemp -d)"
	cd "${workdir}" || exit 0
	log "descargando el instalador del agente"
	if ! curl -fsSO https://dl.google.com/cloudagents/add-google-cloud-ops-agent-repo.sh; then
		warn "no se pudo descargar el instalador; la telemetría queda sin configurar"
		cd / && rm -rf "${workdir}"
		exit 0
	fi
	# --also-install añade el repositorio e instala el paquete en un solo paso.
	if ! bash add-google-cloud-ops-agent-repo.sh --also-install; then
		warn "falló la instalación del agente; se reintentará en el próximo arranque"
		cd / && rm -rf "${workdir}"
		exit 0
	fi
	cd / && rm -rf "${workdir}"
fi

# ---------------------------------------------------------------------------
# Configuración
# ---------------------------------------------------------------------------
# Un receptor prometheus no admite processors: el filtrado se hace con
# metric_relabel_configs dentro del propio scrape_config.
#
# Se fuerza collection_interval en hostmetrics porque el valor por defecto del
# agente es 60s, demasiado grueso para distinguir una degradación de un pico
# durante una prueba de carga.
#
# El pipeline lista los dos receptores porque la configuración del usuario
# reemplaza la que trae el agente por defecto, que solo escucha hostmetrics.
log "escribiendo ${CONFIG_FILE} (scrape=${TARGET}, intervalo=${INTERVAL})"
mkdir -p "${CONFIG_DIR}"
cat > "${CONFIG_FILE}" <<YAML
# Generado por scripts/IaC/scripts/install_ops_agent.sh. No editar a mano:
# se sobrescribe en cada arranque de la VM.
metrics:
  receivers:
    hostmetrics:
      type: hostmetrics
      collection_interval: ${INTERVAL}
    prometheus:
      type: prometheus
      config:
        scrape_configs:
          - job_name: mooc-app
            scrape_interval: ${INTERVAL}
            static_configs:
              - targets: ['${TARGET}']
  service:
    pipelines:
      metrics:
        receivers: [hostmetrics, prometheus]
YAML
chmod 600 "${CONFIG_FILE}"

# ---------------------------------------------------------------------------
# Arranque y verificación
# ---------------------------------------------------------------------------
if ! systemctl restart "${AGENT_PKG}"; then
	warn "no se pudo reiniciar el agente"
	exit 0
fi
sleep 3
if systemctl is-active --quiet "${AGENT_PKG}"; then
	log "agente activo; las métricas de ${TARGET} van a Cloud Monitoring"
else
	warn "el agente no quedó activo; revisar con: journalctl -u ${AGENT_PKG}"
fi
