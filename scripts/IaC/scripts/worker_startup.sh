#!/usr/bin/env bash
# Arranque de Worker Server: instala Docker, clona el repositorio y levanta
# Redis (cola asynq), ClamAV y los workers con la configuración cloud.
set -euo pipefail
exec > >(tee -a /var/log/mooc-startup.log) 2>&1
export PATH="${PATH}:/usr/lib/google-cloud-sdk/bin:/snap/bin"

META_BASE="http://metadata.google.internal/computeMetadata/v1/instance/attributes"

get_meta() {
  curl -fsS -H "Metadata-Flavor: Google" "${META_BASE}/$1" 2>/dev/null || true
}

PROJECT_ID="$(get_meta mooc-project-id)"
ROLE="$(get_meta mooc-role)"
APP_DIR="$(get_meta mooc-app-dir)"
REPO_URL="$(get_meta mooc-repo-url)"
REPO_BRANCH="$(get_meta mooc-repo-branch)"
COMPOSE_SERVICES="$(get_meta mooc-compose-services)"

echo "==> [${ROLE}] instalando dependencias base"
export DEBIAN_FRONTEND=noninteractive
apt-get update -y
apt-get install -y ca-certificates curl gnupg git

if ! command -v docker >/dev/null 2>&1; then
  echo "==> [${ROLE}] instalando Docker"
  curl -fsSL https://get.docker.com | sh
fi
systemctl enable --now docker

echo "==> [${ROLE}] clonando repositorio"
mkdir -p "$(dirname "${APP_DIR}")"
if [ -d "${APP_DIR}/.git" ]; then
  git -C "${APP_DIR}" fetch --depth 1 origin "${REPO_BRANCH}"
  git -C "${APP_DIR}" reset --hard "origin/${REPO_BRANCH}"
else
  git clone --depth 1 --branch "${REPO_BRANCH}" "${REPO_URL}" "${APP_DIR}"
fi

secret() {
  local name="$1"
  [ -n "${name}" ] || return 0
  gcloud secrets versions access latest --secret="${name}" --project="${PROJECT_ID}" 2>/dev/null || true
}

echo "==> [${ROLE}] resolviendo secretos"
POSTGRES_PASSWORD="$(secret "$(get_meta mooc-secret-postgres-password)")"
REDIS_PASSWORD="$(secret "$(get_meta mooc-secret-redis-password)")"
JWT_SECRET="$(secret "$(get_meta mooc-secret-jwt)")"
S3_ACCESS_KEY="$(secret "$(get_meta mooc-secret-s3-access)")"
S3_SECRET_KEY="$(secret "$(get_meta mooc-secret-s3-secret)")"

echo "==> [${ROLE}] escribiendo configuración de entorno"
cat > "${APP_DIR}/.env" <<EOF
POSTGRES_HOST=$(get_meta mooc-postgres-host)
POSTGRES_PORT=$(get_meta mooc-postgres-port)
POSTGRES_USER=$(get_meta mooc-postgres-user)
POSTGRES_PASSWORD=${POSTGRES_PASSWORD}
POSTGRES_DB=$(get_meta mooc-postgres-db)
POSTGRES_SSLMODE=$(get_meta mooc-postgres-sslmode)
REDIS_ADDR=$(get_meta mooc-redis-host):$(get_meta mooc-redis-port)
REDIS_PASSWORD=${REDIS_PASSWORD}
JWT_SECRET=${JWT_SECRET}
S3_ENDPOINT=$(get_meta mooc-s3-endpoint)
S3_PUBLIC_ENDPOINT=$(get_meta mooc-s3-public-endpoint)
S3_ACCESS_KEY=${S3_ACCESS_KEY}
S3_SECRET_KEY=${S3_SECRET_KEY}
S3_USE_SSL=$(get_meta mooc-s3-use-ssl)
S3_REGION=$(get_meta mooc-s3-region)
S3_BUCKET_ORIGINALS=$(get_meta mooc-bucket-originals)
S3_BUCKET_HLS=$(get_meta mooc-bucket-hls)
S3_BUCKET_PUBLIC=$(get_meta mooc-bucket-public)
CDN_BASE_URL=
SMTP_HOST=mailpit
SMTP_PORT=1025
SMTP_USER=prueba@test.com
CLAMAV_HOST=clamav:3310
WORKER_CONCURRENCY=$(get_meta mooc-worker-concurrency)
MOOC_DOMAIN=
NEXT_PUBLIC_API_URL=
EOF
chmod 600 "${APP_DIR}/.env"

echo "==> [${ROLE}] preparando swap"
# 2 GiB de swap en el Worker Server. ClamAV necesita ~913 MB para su base de
# firmas en una VM de 1976 MB que además corre Redis y el worker: sin swap ese
# pico de arranque es letal y el OOM killer se lleva un proceso
# (ver docs/entrega2/evidencia/). El swap no arregla el dimensionamiento, evita
# que el pico mortal.
fallocate -l 2G /swapfile
chmod 600 /swapfile
mkswap /swapfile >/dev/null
swapon /swapfile
grep -q '^/swapfile' /etc/fstab || echo '/swapfile none swap sw 0 0' >> /etc/fstab
free -m | awk '/^Mem:|^Swap:/{print "    " $0}'

echo "==> [${ROLE}] levantando contenedores: ${COMPOSE_SERVICES}"
cd "${APP_DIR}"
# shellcheck disable=SC2086
docker compose --project-directory "${APP_DIR}" -f scripts/IaC/deploy/docker-compose.cloud.yml up -d --build ${COMPOSE_SERVICES}

# Telemetría del worker. El agente reporta las métricas del host y scrapea el
# /metrics del worker, que publica el conteo de ffmpeg en curso.
if [[ "$(get_meta mooc-install-ops-agent)" == "true" ]]; then
	echo "==> [${ROLE}] instalando Ops Agent"
	bash "${APP_DIR}/scripts/IaC/scripts/install_ops_agent.sh" \
		"$(get_meta mooc-metrics-target)" \
		"$(get_meta mooc-metrics-scrape-interval)"
fi

echo "==> [${ROLE}] arranque completado"
