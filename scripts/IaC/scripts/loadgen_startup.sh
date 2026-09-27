#!/usr/bin/env bash
# Arranque del generador de carga (máquina de pruebas, no de la aplicación).
#
# Su único cometido es correr k6 desde dentro de la región, para que el enlace
# del portátil no sea el techo de las mediciones de transferencia. Deja el
# entorno listo y NO siembra ni ejecuta nada: las credenciales se pasan en la
# sesión, nunca por metadatos ni por el repositorio.
set -euo pipefail
exec > >(tee -a /var/log/mooc-startup.log) 2>&1
export PATH="${PATH}:/usr/lib/google-cloud-sdk/bin:/snap/bin"

META_BASE="http://metadata.google.internal/computeMetadata/v1/instance/attributes"

get_meta() {
  curl -fsS -H "Metadata-Flavor: Google" "${META_BASE}/$1" 2>/dev/null || true
}

ROLE="$(get_meta mooc-role)"
APP_DIR="$(get_meta mooc-app-dir)"
REPO_URL="$(get_meta mooc-repo-url)"
REPO_BRANCH="$(get_meta mooc-repo-branch)"
K6_VERSION="$(get_meta mooc-k6-version)"
BASE_URL="$(get_meta mooc-target-url)"

echo "==> [${ROLE}] instalando dependencias"
export DEBIAN_FRONTEND=noninteractive
apt-get update -y
# ffmpeg genera los medios de prueba; el resto son utilidades del runbook.
apt-get install -y ca-certificates curl git ffmpeg jq

# k6 se instala desde el paquete de la versión fijada y no desde el repositorio
# de Grafana: así la versión del generador queda clavada en el tfvars y una
# corrida se puede reproducir meses después sin depender de qué haya publicado.
if ! command -v k6 >/dev/null 2>&1; then
  echo "==> [${ROLE}] instalando k6 ${K6_VERSION}"
  tmp="$(mktemp -d)"
  if curl -fsSL -o "${tmp}/k6.deb" \
    "https://github.com/grafana/k6/releases/download/${K6_VERSION}/k6-${K6_VERSION}-linux-amd64.deb"; then
    dpkg -i "${tmp}/k6.deb" || apt-get install -y -f
  else
    echo "AVISO: no se pudo descargar k6 ${K6_VERSION}; instálalo a mano antes de medir" >&2
  fi
  rm -rf "${tmp}"
fi
k6 version || true

echo "==> [${ROLE}] clonando repositorio"
mkdir -p "$(dirname "${APP_DIR}")"
if [ -d "${APP_DIR}/.git" ]; then
  git -C "${APP_DIR}" fetch --depth 1 origin "${REPO_BRANCH}"
  git -C "${APP_DIR}" reset --hard "origin/${REPO_BRANCH}"
else
  git clone --depth 1 --branch "${REPO_BRANCH}" "${REPO_URL}" "${APP_DIR}"
fi

# Los medios se generan aquí y no se versionan: son ~20 MB de binarios
# reproducibles con ffmpeg.
echo "==> [${ROLE}] generando medios de prueba"
cd "${APP_DIR}/capacity-planning"
task --version >/dev/null 2>&1 || echo "AVISO: task no está instalado; usa los comandos de k6 directamente"
mkdir -p media results
if command -v ffmpeg >/dev/null 2>&1; then
  cat > /tmp/gen_media.sh <<'GEN'
set -e
ffmpeg -y -loglevel error -f lavfi -i testsrc=size=854x480:rate=24:duration=30 \
  -f lavfi -i sine=frequency=440:duration=30 -c:v libx264 -preset veryfast -crf 28 \
  -pix_fmt yuv420p -c:a aac -b:a 128k -shortest media/p1-small.mp4
ffmpeg -y -loglevel error -f lavfi -i testsrc=size=1280x720:rate=24:duration=60 \
  -f lavfi -i sine=frequency=440:duration=60 -c:v libx264 -preset veryfast -crf 26 \
  -pix_fmt yuv420p -c:a aac -b:a 128k -shortest media/p2-medium.mp4
ffmpeg -y -loglevel error -f lavfi -i testsrc=size=1920x1080:rate=24:duration=120 \
  -f lavfi -i sine=frequency=440:duration=120 -c:v libx264 -preset veryfast -crf 24 \
  -pix_fmt yuv420p -c:a aac -b:a 128k -shortest media/p3-large.mp4
ffmpeg -y -loglevel error -f lavfi -i sine=frequency=440:duration=180 \
  -c:a aac -b:a 128k media/a1-audio.m4a
GEN
  bash /tmp/gen_media.sh || echo "AVISO: falló la generación de medios" >&2
  rm -f /tmp/gen_media.sh
fi
ls -lh media/ || true

cat <<FIN
==> [${ROLE}] generador listo.

  URL a medir : ${BASE_URL:-<sin definir>}
  repositorio : ${APP_DIR}/capacity-planning

Antes de medir, en la sesión (nunca por metadatos ni en el repositorio):

  cd ${APP_DIR}/capacity-planning
  export ENV=gcp
  export BASE_URL=${BASE_URL}
  export ADMIN_USER=admin1 ADMIN_PASS='...' SEED_PASSWORD='...'
  export INSECURE_TLS=true

  k6 inspect k6/esc1_academico.js   # comprobar que carga
  task gcp:seed                     # o k6 run k6/seed_students.js
FIN
