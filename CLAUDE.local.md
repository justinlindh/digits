# CLAUDE.local.md — Local Agent Context

> **NOT committed to git.** Contains deployment targets, infrastructure details,
> and local machine specifics. Update this file as infrastructure changes.

---

## Infrastructure

| Host | IP | Role |
|------|----|------|
| GPU box | `192.168.1.199` | Production server (signald, admind, Postgres, coturn, site) |
| Pi Zero 2 W | `192.168.2.142` (WiFi) / `10.12.194.1` (USB gadget) | Phone hardware running digitsd |
| Agent VM | `192.168.6.34` | Cross-compile builds, TTS clone server |

SSH access:
- GPU box: `ssh justin@192.168.1.199`
- Pi: `ssh digits@192.168.2.142`
- VM: `ssh justin@192.168.6.34`

---

## Production Deployment (GPU box)

**All server deploys go through `deploy.sh` — never run docker compose manually.**

```bash
# Deploy signald (default):
ssh justin@192.168.1.199 'cd ~/src/digits/server && ./deploy.sh'

# Deploy specific services:
ssh justin@192.168.1.199 'cd ~/src/digits/server && ./deploy.sh signald admind'

# View logs:
ssh justin@192.168.1.199 'docker logs -f digits-prod-signald-1 --tail 50'
ssh justin@192.168.1.199 'docker logs -f digits-prod-admind-1 --tail 50'

# Check service health:
ssh justin@192.168.1.199 'docker compose -p digits-prod -f ~/src/digits/server/docker-compose.prod.yml ps'
```

**Why deploy.sh only:** It sets the correct project name (`digits-prod`), env file (`.env.prod`),
and compose file. Running docker compose directly with wrong args creates duplicate containers.

**Env file location (on GPU box):** `~/src/digits/server/.env.prod` — never committed, contains secrets.

**TURN server (coturn, separate from digits):**
```bash
ssh justin@192.168.1.199 'cd ~/src/coturn && docker compose up -d'
```

---

## Public Domain & Routing

- **`digits.family`** — Cloudflare proxied → NPM (Nginx Proxy Manager) on home network → GPU box
  - `https://digits.family` → site (static, future)
  - `https://app.digits.family` → signald port 8090 (Google OAuth redirect URL uses this)
  - `https://digits.family/admin` → admind port 9094
- **`turn.digits.family`** — DNS-only (grey cloud, NOT proxied) → `68.224.37.131` (home WAN IP)
  - UDP 3478 + TCP 3478 + relay ports 49152-49252 UDP forwarded in router

---

## Pi Phone Deployment

Build (cross-compile on dev machine or VM, NOT on Pi — OOM on 512MB):
```bash
cd ~/src/digits/pi/digitsd
GOOS=linux GOARCH=arm64 CGO_ENABLED=1 CC=aarch64-linux-gnu-gcc go build -o digitsd ./cmd/digitsd/
```

Deploy (must stop service first — binary is locked while running):
```bash
ssh digits@192.168.2.142 'sudo systemctl stop digitsd'
scp pi/digitsd/digitsd digits@192.168.2.142:~/digits/digitsd/digitsd
ssh digits@192.168.2.142 'sudo systemctl start digitsd'
```

Verify:
```bash
ssh digits@192.168.2.142 'journalctl -u digitsd --no-pager -n 30'
# Expected: tones loaded, "POST: PASS", "digitsd ready"
```

---

## Gitea Remote

Primary remote for this repo:
```
ssh://git@192.168.1.199:2222/dumbot/digits.git
```

Push to gitea by default. GitHub (`git@github.com:justinlindh/digits.git`) is a public mirror.
**No direct pushes to main on GitHub** — PRs required. Gitea has no branch protection.

For Gitea API calls (issues, PRs, releases): use `mcporter` — direct curl gets Cloudflare 403.

---

## Signaling Server Config

signald uses **environment variables only** — CLI flags are silently ignored.

| Var | Production Value | Notes |
|-----|-----------------|-------|
| `SIGNALD_ADDR` | `:8080` | Internal; Caddy/NPM handles external TLS |
| `DATABASE_URL` | postgres on `user-db` container | Migrations run automatically on startup |
| `BASE_URL` | `https://app.digits.family` | |
| `COOKIE_DOMAIN` | `.digits.family` | Leading dot covers all subdomains |
| `GOOGLE_REDIRECT_URL` | `https://app.digits.family/auth/google/callback` | Must match OAuth console |
| `SIGNALD_TURN_ENABLED` | `true` | Set in .env.prod |
| `SIGNALD_TURN_DOMAIN` | `turn.digits.family` | |

---

## TTS / Voice Clone

- **GPU box** (`192.168.1.199:8880`): qwen3-tts preset speakers (CJK-native, Asian-accented English)
- **VM** (`127.0.0.1:8892`): qwen3-tts voice clone server — requires `ref_text` matching ref audio exactly
- **Script:** `scripts/local-tts.sh` — routes "justin" speaker → clone server, others → GPU box
- **Bell woman voice:** Cloned from `pi/tones/bell-woman-ref.mp3`. Bandpass filtered 300-3400Hz (telephone).
- **Pairing voice WAVs:** `pi/tones/pairing/` (12 files)

---

## Git Workflow

- **Feature branches off current phase branch** — not off main
- **Current phase:** Phase 8 (production)
- **Git worktrees** for parallel Claude Code runs: `.worktrees/`
- Commit message convention: `type(scope): description` where scope is one of:
  `pi`, `digitsd`, `firmware`, `server`, `image`, `docs`, `ci`

---

## Consumer Website (digits.family)

**Separate repo:** `~/src/digits-site` (local) / `ssh://git@192.168.1.199:2222/dumbot/digits-site.git`

Astro + Tailwind static site. Built into a Docker image served by nginx.

**Local dev:**
```bash
cd ~/src/digits-site
npm run dev       # dev server
npm run build     # static build to dist/
```

**Deploy to GPU box:**
```bash
# Push changes to Gitea first, then on GPU box:
ssh justin@192.168.1.199 'cd ~/src/digits-site && git pull && docker compose up -d --build'
```

- Container name: `digits-landing`, port `8091:80`
- NPM routes `digits.family` (root) → GPU box port 8091
- No `.env` needed — fully static, no secrets

**Style rules:**
- No em dashes anywhere (AI writing tell)
- No profanity (consumer-facing)
- Warm retro aesthetic: paper tones, pixel font accents, CRT scanline effect
- Nav links to `https://digits.family/app` (the signald web UI)

---

## Phone Devices

Both phones: user `dev`, password `digits`. SSH key auth not configured; use `sshpass -p digits ssh dev@<ip>`.

| Device | Hostname | IP | SSH | digitsd |
|--------|----------|----|-----|---------|
| Phone 1 | `digits-a808` | `192.168.2.142` | `ssh dev@192.168.2.142` — **password auth may be disabled, VM key not yet authorized; use USB gadget `10.12.194.1` as fallback** | running |
| Phone 2 | `digits-1c09` | `192.168.2.228` | `ssh dev@192.168.2.228` (VM key authorized ✅) | running |

Quick checks:
```bash
ssh dev@192.168.2.228 'journalctl -u digitsd --no-pager -n 20'
ssh dev@192.168.2.228 'sudo systemctl restart digitsd'
```

Deploy digitsd to a phone (stop → scp → start):
```bash
TARGET=dev@192.168.2.228   # or 192.168.2.142
ssh $TARGET 'sudo systemctl stop digitsd'
scp pi/digitsd/digitsd $TARGET:~/digits/digitsd/digitsd
ssh $TARGET 'sudo systemctl start digitsd'
```
