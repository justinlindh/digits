# Self-Hosting Digits

> A phone for real conversations. No screens. No surveillance. Just voice.

This guide walks you through running your own Digits server. If you can run Docker, you can run this.

---

## Prerequisites

- **Linux server** -- any x86_64 VPS or dedicated box. 1GB RAM minimum, 2GB recommended.
- **Docker Engine 24+** -- [install docs](https://docs.docker.com/engine/install/)
- **Docker Compose v2** -- bundled with Docker Desktop; for Linux: `apt install docker-compose-plugin`
- **Domain name** -- pointed at your server's public IP (A record). Caddy handles TLS automatically.
- **SMTP credentials** -- for sending magic-link login emails. Any provider works (Postmark, Mailgun, Gmail SMTP, etc.)
- **Google OAuth (optional)** -- if you want Google sign-in alongside magic links

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

Once running:
- **User app** → `https://your-domain.com`

---

## Production TLS

For a production deployment with automatic HTTPS via Let's Encrypt, use the included `Caddyfile.production` template instead of the default dev Caddyfile.

**Steps:**

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

Caddy will obtain and renew a Let's Encrypt certificate automatically. No certbot or manual renewal needed.

---

## Architecture

```
                        Internet
                            │
                       ┌────▼────┐
                       │  Caddy  │  :443  (TLS termination, reverse proxy)
                       └────┬────┘
                            │
                     ┌──────▼──────┐
                     │   signald   │
                     │   :8080     │
                     │             │
                     │ WebRTC sig  │
                     │ Auth/magic  │
                     │ User API    │
                     └──────┬──────┘
                            │
                     ┌──────▼──────┐
                     │   user-db   │
                     │  PostgreSQL │
                     │             │
                     │ users,      │
                     │ households, │
                     │ sessions    │
                     └─────────────┘
```

---

## Environment Variables

All configuration lives in `server/.env`. Copy `server/.env.example` and fill it in.

| Variable | Required | Description |
|---|---|---|
| `BASE_URL` | ✅ | Public URL of your server, e.g. `https://digits.example.com` |
| `ADMIN_SECRET` | ✅ | Secret protecting the internal stats endpoint (pick something random) |
| `SMTP_HOST` | ✅ | SMTP server hostname |
| `SMTP_PORT` | ✅ | SMTP port (usually 587 for STARTTLS) |
| `SMTP_USER` | ✅ | SMTP username |
| `SMTP_PASS` | ✅ | SMTP password |
| `SMTP_FROM` | ✅ | From address for outbound email |
| `GOOGLE_CLIENT_ID` | ☐ | Google OAuth client ID (optional) |
| `GOOGLE_CLIENT_SECRET` | ☐ | Google OAuth client secret (optional) |
| `GOOGLE_REDIRECT_URL` | ☐ | OAuth callback URL, e.g. `https://digits.example.com/auth/google/callback` |

---

## Phone Setup

Each Digits phone is a Raspberry Pi Zero 2 W running `digitsd`.

### Flash and Configure the Pi

See the [Pi setup README](../pi/README-mixer.md) for full hardware assembly. Once the Pi is running:

### Point the Pi at Your Server

`digitsd` takes a `-signald` flag with the WebSocket URL of your server:

```bash
# Edit the digitsd systemd unit or launch script on the Pi
# Default: ws://localhost:8443/ws
# Change to: wss://digits.example.com/ws

sudo nano /etc/systemd/system/digitsd.service
# Update: ExecStart=... -signald wss://digits.example.com/ws ...

sudo systemctl daemon-reload
sudo systemctl restart digitsd
```

If your server uses a self-signed cert (dev only), add `-insecure` to skip TLS verification. Don't do this in production.

### Pairing Flow

1. In the user app, complete onboarding to create a household, then add a line on `/phones`.
2. The system generates a pairing code.
3. On the Pi, the pairing code is exchanged automatically on first connect -- no screen needed.
4. Once paired, the phone registers itself and is ready to dial.

---

## Backup

Run this on a cron or before any upgrade.

```bash
# Backup user data (users, households, sessions)
docker compose exec user-db pg_dump -U digits digits > backup-user-$(date +%Y%m%d).sql
```

Restore:

```bash
docker compose exec -T user-db psql -U digits digits < backup-user-20260101.sql
```

Store backups off-server. S3, rsync to another machine, whatever you've got.

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

# Check nothing's broken
docker compose ps
docker compose logs --tail=50
```

Caddy handles zero-downtime for its own config reloads. The Go services will have a brief restart during `up -d`.

---

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| Magic link email never arrives | SMTP misconfigured | Check `SMTP_HOST/PORT/USER/PASS`. Run `docker compose logs signald` and look for mail errors. Test with `swaks` or your provider's SMTP tester. |
| "Link expired" on login | Token TTL passed (15 min) | Request a new magic link. If this happens constantly, check server clock (`timedatectl`). |
| Phone won't connect | Wrong WebSocket URL or TLS error | Verify `-signald` flag on the Pi points to `wss://your-domain.com/ws`. Check `journalctl -u digitsd` on the Pi. |
| Google OAuth "redirect_uri mismatch" | Callback URL not registered | Add `https://your-domain.com/auth/google/callback` to authorized redirect URIs in Google Cloud Console. Verify `GOOGLE_REDIRECT_URL` in `.env` matches exactly. |
| Caddy returns 502 | Backend not ready | `docker compose ps` -- check signald is healthy. `docker compose logs caddy` for upstream errors. |
| Database migration fails on startup | Dirty schema state | Check `docker compose logs signald` for migration errors. Usually safe to re-run after fixing the underlying issue. |

---

## Hardware Requirements

This is not a demanding service. Voice calls are low-bandwidth and the server is stateless between calls.

| Resource | Minimum | Notes |
|---|---|---|
| CPU | 1 vCPU | Even a $5/mo VPS handles dozens of simultaneous calls |
| RAM | 1GB | 2GB comfortable; Postgres + Go services are lean |
| Storage | 10GB | 1GB is plenty for years of data; extra for logs/backups |
| Bandwidth | ~100kbps per active call | WebRTC audio only, no video |
| OS | Linux x86_64 | Tested on Debian 12, Ubuntu 22.04+ |

A Raspberry Pi 4 (2GB) works fine as a local server on a home network. For remote/internet access you'll want a VPS with a stable IP and a domain.

**Recommended VPS specs for a small family/community deployment:**
- 2 vCPU, 2GB RAM, 20GB SSD
- Any provider: Hetzner, Linode, DigitalOcean, Vultr

---

## Source

`https://github.com/justinlindh/digits`
