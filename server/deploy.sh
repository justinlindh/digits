#!/usr/bin/env bash
# deploy.sh — Rebuild and restart Digits production services
# Usage: ./deploy.sh [service...]  (default: signald)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

ENV_FILE=".env.prod"
COMPOSE_PROJECT="digits-prod"
COMPOSE_FILE="docker-compose.prod.yml"

[[ -f "$ENV_FILE" ]] || { echo "ERROR: $ENV_FILE not found in $SCRIPT_DIR" >&2; exit 1; }

SERVICES=("${@:-signald}")

echo "==> Building ${SERVICES[*]}..."
docker compose -p "$COMPOSE_PROJECT" -f "$COMPOSE_FILE" --env-file "$ENV_FILE" build "${SERVICES[@]}"

echo "==> Restarting ${SERVICES[*]}..."
docker compose -p "$COMPOSE_PROJECT" -f "$COMPOSE_FILE" --env-file "$ENV_FILE" up -d "${SERVICES[@]}"

echo "==> Waiting for health..."
sleep 5
docker compose -p "$COMPOSE_PROJECT" -f "$COMPOSE_FILE" --env-file "$ENV_FILE" ps

echo "==> Done."
