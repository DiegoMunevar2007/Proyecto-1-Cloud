#!/usr/bin/env bash
# Arranque de Web Server: instala Docker, clona el repositorio y levanta la
# API y el proxy inverso con la configuración del entorno cloud.
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
MOOC_DOMAIN=$(get_meta mooc-domain)
NEXT_PUBLIC_API_URL=https://$(get_meta mooc-domain)
EOF
chmod 600 "${APP_DIR}/.env"

# TLS: con IP se usa el certificado interno de Caddy (autofirmado); con un
# nombre DNS, Caddy obtiene automáticamente un certificado de Let's Encrypt.
DOMAIN="$(get_meta mooc-domain)"
if [[ "${DOMAIN}" =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}$ ]]; then
  TLS_DIRECTIVE="tls internal"
else
  TLS_DIRECTIVE=""
fi

# Caddyfile generado según si se despliega el frontend existente.
if [[ "${COMPOSE_SERVICES}" == *frontend* ]]; then
  cat > "${APP_DIR}/IaC/deploy/Caddyfile" <<'CADDY'
{
	default_sni {$MOOC_DOMAIN}
}

{$MOOC_DOMAIN} {
	TLS_DIRECTIVE_PLACEHOLDER
	encode gzip

	@api path /api/* /health /openapi.json
	handle @api {
		reverse_proxy backend:8080
	}

	handle {
		reverse_proxy frontend:3000
	}
}
CADDY
else
  cat > "${APP_DIR}/IaC/deploy/Caddyfile" <<'CADDY'
{
	default_sni {$MOOC_DOMAIN}
}

{$MOOC_DOMAIN} {
	TLS_DIRECTIVE_PLACEHOLDER
	encode gzip
	reverse_proxy backend:8080
}
CADDY
fi
sed -i "s|TLS_DIRECTIVE_PLACEHOLDER|${TLS_DIRECTIVE}|" "${APP_DIR}/IaC/deploy/Caddyfile"

echo "==> [${ROLE}] levantando contenedores: ${COMPOSE_SERVICES}"
cd "${APP_DIR}"
# shellcheck disable=SC2086
docker compose --project-directory "${APP_DIR}" -f IaC/deploy/docker-compose.cloud.yml up -d --build ${COMPOSE_SERVICES}

echo "==> [${ROLE}] arranque completado"
