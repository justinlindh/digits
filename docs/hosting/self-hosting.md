# Self-Hosting Digits

This guide walks you through running your own Digits signaling server. If you can run Docker, you can run this.

---

## Prerequisites

- **Linux server** -- any x86_64 VPS or dedicated box. 1 GB RAM minimum, 2 GB recommended.
- **Docker Engine 24+** -- [install docs](https://docs.docker.com/engine/install/)
- **Docker Compose v2** -- bundled with Docker Desktop; for Linux: `apt install docker-compose-plugin`
- **Domain name** -- pointed at your server's public IP (A record). Caddy handles TLS automatically.
- **SMTP credentials** -- for sending magic-link login emails. Any provider works (Postmark, Mailgun, Gmail SMTP, etc.)
- **Google OAuth (optional)** -- if you want Google sign-in alongside magic links
- **TURN server (optional)** -- required if phones need to call across different networks behind NAT. See [TURN / NAT Traversal](#turn--nat-traversal).

---

## Quick Start

```bash
# 1. Clone the repo
git clone https://github.com/justinlindh/digits.git
cd digits/server

# 2. Copy and edit the environment file
cp .env.example .env
$EDITOR .env   # fill in BASE_URL, SMTP credentials, secrets

# 3. Update the Caddyfile with your domain
$EDITOR docker/Caddyfile
# Replace the https:// block to use your domain, e.g.:
#   https://digits.example.com { ... }

# 4. Start everything
docker compose up -d

# 5. Verify services are up
docker compose ps
docker compose logs --follow
```

Once running, the app is at `https://your-domain.com`.

---

## Architecture

```
                        Internet
                            |
                       +----v----+
                       |  Caddy  |  :443  (TLS termination, reverse proxy)
                       +----+----+
                            |
                     +------v------+
                     |   signald   |
                     |   :8080     |
                     |             |
                     | WebRTC sig  |
                     | Auth/magic  |
                     | Web app     |
                     +------+------+
                            |
                     +------v------+
                     |   user-db   |
                     |  PostgreSQL |
                     |             |
                     | users,      |
                     | households, |
                     | lines,      |
                     | calls       |
                     +-------------+
```

Caddy terminates TLS and proxies to signald. signald is the single Go binary that handles everything: WebSocket signaling, authentication, the web dashboard, and the REST API. PostgreSQL stores all persistent state.

Voice audio never passes through the server. Calls are peer-to-peer WebRTC sessions between phones; the server only brokers the signaling handshake.

---

## Environment Variables

All configuration lives in `server/.env`. Copy `server/.env.example` and fill it in.

### Required

| Variable | Description |
|---|---|
| `BASE_URL` | Public URL of your server, e.g. `https://digits.example.com` |
| `ADMIN_SECRET` | Secret protecting the internal stats endpoint. Generate with `openssl rand -hex 32`. |
| `SMTP_HOST` | SMTP server hostname |
| `SMTP_PORT` | SMTP port (usually 587 for STARTTLS) |
| `SMTP_USER` | SMTP username |
| `SMTP_PASS` | SMTP password |
| `SMTP_FROM` | From address for outbound email |

### Optional

| Variable | Description |
|---|---|
| `COOKIE_DOMAIN` | Cookie domain for subdomain sharing (e.g. `.digits.example.com`). Leave blank if not using subdomains. |
| `GOOGLE_CLIENT_ID` | Google OAuth client ID |
| `GOOGLE_CLIENT_SECRET` | Google OAuth client secret |
| `GOOGLE_REDIRECT_URL` | OAuth callback URL, e.g. `https://digits.example.com/auth/google/callback` |
| `SIGNALD_TURN_ENABLED` | Set to `true` to enable TURN credential generation. See [TURN / NAT Traversal](#turn--nat-traversal). |
| `SIGNALD_TURN_SECRET` | Shared secret for HMAC-SHA1 TURN credentials. Must match your TURN server's `static-auth-secret`. |
| `SIGNALD_TURN_DOMAIN` | TURN server domain, e.g. `turn.example.com` |

---

## Production TLS

For a production deployment with automatic HTTPS via Let's Encrypt, use the included `Caddyfile.production` template instead of the default dev Caddyfile.

1. **Copy the production Caddyfile:**
   ```bash
   cp docker/Caddyfile.production docker/Caddyfile
   ```

2. **Set the `DOMAIN` environment variable** before starting (or add it to your `.env`):
   ```bash
   export DOMAIN=digits.example.com
   ```
   Caddy reads `{$DOMAIN}` at startup and uses it as the virtual host name.

3. **Ensure ports 80 and 443 are open** on your server firewall. Caddy needs port 80 for the ACME HTTP-01 challenge to obtain the certificate, and port 443 for HTTPS traffic:
   ```bash
   # ufw example
   ufw allow 80/tcp
   ufw allow 443/tcp
   ```

4. **Update `docker-compose.yml` ports** for the `caddy` service to expose the standard ports instead of just `8443`:
   ```yaml
   ports:
     - "80:80"
     - "443:443"
   ```

Then start normally:
```bash
DOMAIN=digits.example.com docker compose up -d
```

Caddy will obtain and renew a Let's Encrypt certificate automatically.

---

## TURN / NAT Traversal

WebRTC tries to establish a direct peer-to-peer connection between two phones. When both phones are on the same local network, this works without any extra infrastructure. When phones are behind different NATs (the common case for a distributed family network), they need a TURN relay to connect.

Without TURN, calls between phones on different networks will fail to connect. If all your phones are on the same LAN, you can skip this.

signald generates time-limited HMAC-SHA1 credentials for the TURN server. You supply the shared secret and domain; the phones receive fresh credentials with each call setup.

### Setting up coturn

[coturn](https://github.com/coturn/coturn) is the standard open-source TURN server. It can run on the same box as signald or on a separate machine.

```bash
# Install
apt install coturn

# /etc/turnserver.conf
realm=digits.example.com
listening-port=3478
tls-listening-port=5349
use-auth-secret
static-auth-secret=YOUR_SECRET_HERE    # must match SIGNALD_TURN_SECRET
cert=/etc/letsencrypt/live/turn.example.com/fullchain.pem
pkey=/etc/letsencrypt/live/turn.example.com/privkey.pem
no-cli
```

Then set the matching env vars in your `.env`:

```
SIGNALD_TURN_ENABLED=true
SIGNALD_TURN_SECRET=YOUR_SECRET_HERE
SIGNALD_TURN_DOMAIN=turn.example.com
```

Open UDP ports 3478 and 49152-65535 (the default relay port range) on the TURN server's firewall.

---

## Phone Setup

Each Digits phone is a Raspberry Pi Zero 2 W running `digitsd`. See the [build guide](https://digits.family/build) for hardware assembly.

### Point the Pi at Your Server

`digitsd` takes a `-signald` flag with the WebSocket URL of your server:

```bash
# Edit the digitsd systemd unit on the Pi
# Default: ws://localhost:8443/ws
# Change to: wss://digits.example.com/ws

sudo nano /etc/systemd/system/digitsd.service
# Update: ExecStart=... -signald wss://digits.example.com/ws ...

sudo systemctl daemon-reload
sudo systemctl restart digitsd
```

If your server uses a self-signed cert (dev only), add `-insecure` to skip TLS verification. Do not do this in production.

### Pairing Flow

1. In the web app, complete onboarding to create a household, then add a line on `/phones`.
2. The system generates a pairing code.
3. On the Pi, the pairing code is exchanged automatically on first connect.
4. Once paired, the phone is ready to dial.

---

## Backup

Run this on a cron or before any upgrade.

```bash
docker compose exec user-db pg_dump -U digits digits > backup-$(date +%Y%m%d).sql
```

Restore:

```bash
docker compose exec -T user-db psql -U digits digits < backup-20260101.sql
```

Store backups off-server. S3, rsync to another machine, whatever you have.

---

## Updating

```bash
cd digits

# Pull latest
git pull

# Rebuild and restart
cd server
docker compose build
docker compose up -d

# Verify
docker compose ps
docker compose logs --tail=50
```

Caddy handles zero-downtime for its own config reloads. signald will have a brief restart during `up -d`.

---

## Kubernetes

Docker Compose handles the vast majority of deployments. If you have a Kubernetes cluster and want to deploy there, there is a [Helm chart](../../charts/digits/) that supports CNPG PostgreSQL, Redis Sentinel for multi-replica signaling, OpenTelemetry tracing, Pyroscope profiling, and Prometheus metrics.

See the [Helm chart README](../../charts/digits/README.md) for installation and configuration.

---

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| Magic link email never arrives | SMTP misconfigured | Check `SMTP_HOST/PORT/USER/PASS`. Run `docker compose logs signald` and look for mail errors. Test with `swaks` or your provider's SMTP tester. |
| "Link expired" on login | Token TTL passed (15 min) | Request a new magic link. If this happens constantly, check server clock (`timedatectl`). |
| Phone won't connect | Wrong WebSocket URL or TLS error | Verify `-signald` flag on the Pi points to `wss://your-domain.com/ws`. Check `journalctl -u digitsd` on the Pi. |
| Calls fail across networks | No TURN server | Set up coturn and configure `SIGNALD_TURN_*` env vars. See [TURN / NAT Traversal](#turn--nat-traversal). |
| Google OAuth "redirect_uri mismatch" | Callback URL not registered | Add `https://your-domain.com/auth/google/callback` to authorized redirect URIs in Google Cloud Console. Verify `GOOGLE_REDIRECT_URL` in `.env` matches exactly. |
| Caddy returns 502 | Backend not ready | `docker compose ps` -- check signald is healthy. `docker compose logs caddy` for upstream errors. |
| Database migration fails on startup | Dirty schema state | Check `docker compose logs signald` for migration errors. Usually safe to re-run after fixing the underlying issue. |

---

## Hardware Requirements

Voice calls are low-bandwidth and the server is stateless between calls.

| Resource | Minimum | Notes |
|---|---|---|
| CPU | 1 vCPU | A $5/mo VPS handles dozens of simultaneous calls |
| RAM | 1 GB | 2 GB comfortable; Postgres + Go are lean |
| Storage | 10 GB | 1 GB is plenty for years of data; extra for logs and backups |
| Bandwidth | ~100 kbps per active call | WebRTC audio only, no video |
| OS | Linux x86_64 | Tested on Debian 12, Ubuntu 22.04+ |

A Raspberry Pi 4 (2 GB) works fine as a local server on a home network. For remote access you need a VPS with a stable IP and a domain.
