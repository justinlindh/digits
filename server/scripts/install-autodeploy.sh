#!/usr/bin/env bash
# install-autodeploy.sh - one-shot installer for the autodeploy pipeline.
# Safe to re-run. Leaves existing config/state files in place.
#
# Required env (on first install):
#   GHCR_TOKEN  Fine-grained PAT with read:packages on justinlindh/digits
#   SMTP_PASS   Password for the SMTP relay user
#
# Optional:
#   AUTODEPLOY_INITIAL_TAG  Seed state if /healthz is unreachable (e.g. server/v1.9.0)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
SERVER_DIR="$REPO_DIR/server"

if [[ $EUID -ne 0 ]]; then
  echo "ERROR: must run as root (needs /etc, /var/lib, systemctl)" >&2
  exit 1
fi

echo "==> Building autodeploy binary"
(cd "$SERVER_DIR" && go build -o /usr/local/bin/autodeploy ./cmd/autodeploy/)
chmod 755 /usr/local/bin/autodeploy

echo "==> Creating runtime directories"
install -d -m 0750 -o root -g root /etc/digits-autodeploy
install -d -m 0700 -o root -g root /var/lib/digits-autodeploy

CONFIG=/etc/digits-autodeploy/config.env
if [[ ! -f "$CONFIG" ]]; then
  echo "==> Installing config template at $CONFIG"
  SMTP_HOST_DEFAULT="$(grep -E '^SMTP_HOST=' "$SERVER_DIR/.env.prod" 2>/dev/null | cut -d= -f2- || true)"
  SMTP_PORT_DEFAULT="$(grep -E '^SMTP_PORT=' "$SERVER_DIR/.env.prod" 2>/dev/null | cut -d= -f2- || echo 587)"
  SMTP_USER_DEFAULT="$(grep -E '^SMTP_USER=' "$SERVER_DIR/.env.prod" 2>/dev/null | cut -d= -f2- || true)"
  SMTP_FROM_DEFAULT="$(grep -E '^SMTP_FROM=' "$SERVER_DIR/.env.prod" 2>/dev/null | cut -d= -f2- || echo noreply@digits.family)"
  cat > "$CONFIG" <<EOF
REPO=justinlindh/digits
TAG_PREFIX=server/v
COMPOSE_DIR=$SERVER_DIR
COMPOSE_FILE=docker-compose.prod.yml
COMPOSE_PROJECT=digits-prod
COMPOSE_ENV_FILE=$SERVER_DIR/.env.prod
SERVICES=signald admind
HEALTH_URLS=http://localhost:8090/healthz http://localhost:9094/healthz
GHCR_USERNAME=justinlindh
# Optional: path to a read-only GHCR PAT. Only needed if the images are
# private; for public packages docker pull works unauthenticated and
# autodeploy skips the docker login step entirely.
# GHCR_TOKEN_FILE=/etc/digits-autodeploy/ghcr_token
# Optional: path to a read-only GitHub PAT for the releases API. With this set,
# the rate limit goes from 60/hr (unauthenticated) to 5000/hr. Not required.
# GITHUB_TOKEN_FILE=/etc/digits-autodeploy/github_token
STATE_FILE=/var/lib/digits-autodeploy/state.json
SMTP_HOST=${SMTP_HOST_DEFAULT:-CHANGEME}
SMTP_PORT=${SMTP_PORT_DEFAULT}
SMTP_USER=${SMTP_USER_DEFAULT:-CHANGEME}
SMTP_PASS_FILE=/etc/digits-autodeploy/smtp_pass
SMTP_FROM=${SMTP_FROM_DEFAULT}
ALERT_TO=justinlindh@gmail.com
HEALTH_TIMEOUT=90s
REVERT_HEALTH_TIMEOUT=60s
EMAIL_DEBOUNCE=30m
POLL_INTERVAL=60s
EOF
  chmod 600 "$CONFIG"
  echo "   Review $CONFIG and edit any CHANGEME values."
else
  echo "==> Config already present at $CONFIG, leaving untouched"
fi

if [[ -n "${GHCR_TOKEN:-}" ]]; then
  echo "==> Writing GHCR token"
  printf '%s' "$GHCR_TOKEN" > /etc/digits-autodeploy/ghcr_token
  chmod 600 /etc/digits-autodeploy/ghcr_token
fi
if [[ -n "${SMTP_PASS:-}" ]]; then
  echo "==> Writing SMTP password"
  printf '%s' "$SMTP_PASS" > /etc/digits-autodeploy/smtp_pass
  chmod 600 /etc/digits-autodeploy/smtp_pass
fi

echo "==> Installing systemd units"
install -m 0644 "$REPO_DIR/systemd/digits-autodeploy.service" /etc/systemd/system/
install -m 0644 "$REPO_DIR/systemd/digits-autodeploy.timer" /etc/systemd/system/
systemctl daemon-reload

STATE=/var/lib/digits-autodeploy/state.json
if [[ ! -f "$STATE" ]]; then
  echo "==> Seeding state file"
  TAG=""
  if [[ -n "${AUTODEPLOY_INITIAL_TAG:-}" ]]; then
    TAG="$AUTODEPLOY_INITIAL_TAG"
  else
    HEALTHZ_URL=http://localhost:8090/healthz
    # Split the fetch and the JSON parse so a failure points at the culprit
    # instead of a single opaque "could not determine version" error.
    if ! HEALTHZ_JSON="$(curl -fsS --max-time 3 "$HEALTHZ_URL" 2>&1)"; then
      echo "ERROR: curl $HEALTHZ_URL failed: $HEALTHZ_JSON" >&2
      echo "       Is signald running? Is port 8090 reachable?" >&2
      echo "       Re-run with AUTODEPLOY_INITIAL_TAG=server/vX.Y.Z to skip this probe." >&2
      exit 3
    fi
    if ! command -v python3 >/dev/null 2>&1; then
      echo "ERROR: python3 not found (needed to parse /healthz JSON)." >&2
      echo "       Install python3 or re-run with AUTODEPLOY_INITIAL_TAG=server/vX.Y.Z." >&2
      exit 3
    fi
    if ! VER="$(printf '%s' "$HEALTHZ_JSON" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("version",""))' 2>/dev/null)" || [[ -z "$VER" ]]; then
      echo "ERROR: /healthz response did not contain a version field:" >&2
      echo "       $HEALTHZ_JSON" >&2
      echo "       Re-run with AUTODEPLOY_INITIAL_TAG=server/vX.Y.Z." >&2
      exit 3
    fi
    TAG="server/v$VER"
  fi
  NOW="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  cat > "$STATE" <<EOF
{
  "last_deployed_tag": "$TAG",
  "last_deployed_at": "$NOW",
  "last_attempt_tag": "$TAG",
  "last_attempt_status": "success",
  "last_attempt_at": "$NOW"
}
EOF
  chmod 600 "$STATE"
  echo "   Seeded $STATE with tag=$TAG"
else
  echo "==> State already present, leaving untouched"
fi

echo "==> Running --dry-run self-test"
/usr/local/bin/autodeploy --config "$CONFIG" --dry-run

echo "==> Enabling timer"
systemctl enable --now digits-autodeploy.timer
systemctl list-timers digits-autodeploy.timer --no-pager

echo "==> Install complete."
