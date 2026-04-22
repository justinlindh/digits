#!/usr/bin/env bash
# deploy.sh — Manual deploy of Digits production services.
# Default: pulls images from GHCR for the latest server/v* tag.
# Usage:   ./deploy.sh [--local-build] [service...]  (default service: signald)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

ENV_FILE=".env.prod"
COMPOSE_PROJECT="digits-prod"
COMPOSE_FILE="docker-compose.prod.yml"
LOCAL_BUILD=0

while [[ $# -gt 0 && "$1" == --* ]]; do
  case "$1" in
    --local-build) LOCAL_BUILD=1; shift ;;
    --) shift; break ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
done

[[ -f "$ENV_FILE" ]] || { echo "ERROR: $ENV_FILE not found in $SCRIPT_DIR" >&2; exit 1; }

SERVICES=("${@:-signald}")

git fetch --tags
export BUILD_VERSION
BUILD_VERSION="$(git describe --tags --match 'server/v*' --always 2>/dev/null | sed 's|^server/v||')"
export BUILD_COMMIT
BUILD_COMMIT="$(git rev-parse --short HEAD 2>/dev/null)"

COMPOSE_ARGS=(-p "$COMPOSE_PROJECT" -f "$COMPOSE_FILE" --env-file "$ENV_FILE")

if [[ "$LOCAL_BUILD" -eq 1 ]]; then
  echo "==> Local build mode (version=${BUILD_VERSION}, commit=${BUILD_COMMIT})"
  COMPOSE_ARGS+=(-f "docker-compose.local-build.yml")
  docker compose "${COMPOSE_ARGS[@]}" build "${SERVICES[@]}"
else
  echo "==> Pulling images from GHCR (version=${BUILD_VERSION})"
  docker compose "${COMPOSE_ARGS[@]}" pull "${SERVICES[@]}"
fi

echo "==> Restarting ${SERVICES[*]}..."
docker compose "${COMPOSE_ARGS[@]}" up -d --wait "${SERVICES[@]}"

echo "==> Status:"
docker compose "${COMPOSE_ARGS[@]}" ps
echo "==> Done."
