# Load Testing

A Go load test tool at `tools/loadtest/` simulates thousands of devices connecting to signald and placing calls. It exercises the full e2e path: Traefik ingress, WebSocket upgrade, device auth (token hash lookup in Postgres), Redis presence updates, call authorization (CanCall query), call tracking, TURN credential generation, and signaling relay.

## Building

```bash
cd tools/loadtest
go build -o loadtest .
```

## Usage

```bash
./loadtest \
  --db "postgresql://user:pass@host:port/digits?sslmode=disable" \
  --server "ws://traefik-ip/ws" \
  --host "app.digits.family" \
  --devices 10000 \
  --ramp 500 \
  --call-rate 100 \
  --call-duration 3s \
  --duration 120s
```

The tool seeds test data (a user, household, lines, and paired devices with valid tokens) into Postgres, ramps up WebSocket connections, generates calls between random device pairs, then cleans up all test data on exit.

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--db` | (required) | Postgres connection string for seeding test data |
| `--server` | `ws://localhost:8443/ws` | signald WebSocket URL |
| `--host` | (none) | Host header for WebSocket upgrade (needed when connecting through a reverse proxy) |
| `--devices` | 1000 | Number of simulated devices |
| `--ramp` | 100 | Connections per second during ramp-up |
| `--call-rate` | 10 | Calls per second after ramp-up |
| `--call-duration` | 3s | Average call hold time before hangup |
| `--duration` | 60s | How long to run after ramp-up completes |
| `--seed-only` | false | Seed test data and exit (useful for inspecting the DB) |
| `--clean-only` | false | Delete test data and exit |

### Database access

The tool needs direct Postgres access for seeding. For k8s deployments, create a temporary NodePort:

```bash
kubectl -n digits apply -f - <<'EOF'
apiVersion: v1
kind: Service
metadata:
  name: userdb-loadtest
  namespace: digits
spec:
  type: NodePort
  selector:
    cnpg.io/cluster: digits-userdb
    role: primary
  ports:
    - port: 5432
      targetPort: 5432
      nodePort: 30432
EOF
```

Delete it after testing:

```bash
kubectl -n digits delete svc userdb-loadtest
```

### WebSocket rate limit

signald rate-limits WebSocket upgrades to 30/min per IP by default. For load testing from a single host, set `SIGNALD_WS_RATE_LIMIT` to a higher value:

```yaml
# In Helm values (revert after testing)
signald:
  env:
    SIGNALD_WS_RATE_LIMIT: "10000"
```

## What it tests

Each phase exercises different layers:

**Ramp-up (connection phase):**
- Traefik L7 ingress and WebSocket upgrade
- signald WebSocket rate limiter
- Device token authentication (SHA-256 hash lookup in Postgres)
- Redis device presence registration
- Hub connection management (goroutines, send channels)

**Call phase:**
- Call authorization (`CanCall` query: household membership + link check in Postgres)
- Call tracker state (Redis for cross-pod, in-memory for local)
- SDP and ICE message relay through the signaling hub
- TURN credential generation (HMAC-SHA1)
- Call metadata INSERT into Postgres

**Cleanup:**
- Bulk DELETE of test lines, devices, households, users
- No test data persists after the tool exits

## Results

Tested on a homelab Talos k8s cluster built from consumer hardware. Nothing special: refurbished mini PCs and a repurposed laptop.

### Cluster hardware

| Node | Role | CPU | RAM | Hardware |
|------|------|-----|-----|----------|
| cp-1 | control plane | 2 cores | 4 GB | |
| cp-2 | control plane | 2 cores | 4 GB | |
| cp-3 | control plane | 4 cores | 4 GB | |
| w-1 | worker | 6 cores | 10 GB | |
| w-2 | worker | 6 cores | 24 GB | |
| w-3 | worker | 16 cores | 16 GB | |
| w-4 | worker | 16 cores | 48 GB | |

### Service configuration

| Service | Replicas | Resources |
|---------|----------|-----------|
| signald | 3 | 100m CPU request, 2Gi memory limit |
| Redis Sentinel | 3 | 50m CPU request, 128Mi memory limit |
| CNPG Postgres | 2 | 100m CPU request, 512Mi memory limit |
| coturn | 2 | 10m CPU request, 128Mi memory limit |
| Traefik | 1 | Cluster ingress controller |

### Test results

| Devices | Call rate | Duration | Connected | Calls | Answered | Failed | Notes |
|---------|-----------|----------|-----------|-------|----------|--------|-------|
| 1,000 | 20/s | 60s | 1,000 (100%) | 1,199 | 1,197 | 0 | Baseline |
| 10,000 | 100/s | 120s | 10,000 (100%) | 11,999 | 11,989 | 0 | Zero failures |
| 50,000 | 200/s | 120s | 28,225 (56%) | 24,000 | 23,980 | 0 | Client-side ephemeral port exhaustion at ~28k |

At 10k devices, the cluster was barely loaded. At 28k (the client-side limit from a single host), signald replicas peaked at ~560Mi RAM and ~350m CPU each. Two worker nodes hit 88-100% CPU. Zero connections or calls were dropped at any level.

### Peak resource usage (28k devices, 200 calls/s)

| Component | CPU | Memory | Notes |
|-----------|-----|--------|-------|
| signald (per replica) | 350m | 560Mi | ~9.4k connections each |
| Redis | 31m | 57Mi | Unchanged from baseline |
| Postgres | 7m | 155Mi | 24k CanCall queries + call metadata writes |
| coturn | 1m | 7Mi | Not exercised (no real TURN allocations) |
| w-1 (busiest node) | 100% | 5.6Gi | |
| w-2 | 88% | 8Gi | |

### Observed bottlenecks (in order)

1. **Client ephemeral port range.** ~28k ports from a single source IP (Linux default 32768-60999). Not a server limitation. Widen with `sysctl net.ipv4.ip_local_port_range="1024 65535"` or test from multiple hosts.
2. **WebSocket rate limiter.** 30/min per IP by default. Intentional DoS protection. Configurable via `SIGNALD_WS_RATE_LIMIT` env var.
3. **signald memory.** ~20KB per WebSocket connection. At 2Gi limit per replica, theoretical max is ~100k connections per replica, or ~300k across 3 replicas.
4. **Postgres max_connections.** Default 100. Bump via CNPG `parameters.max_connections` for high-concurrency tests.

### Scaling projections

Based on observed resource usage per connection and call:

| Target | Replicas | Memory/replica | Estimated cluster requirement |
|--------|----------|---------------|------------------------------|
| 10k devices, 100 calls/s | 3 | 200Mi | Any 3-node cluster |
| 50k devices, 500 calls/s | 5 | 1Gi | 5 nodes, 4 cores each |
| 200k devices, 1000 calls/s | 10 | 4Gi | 10 nodes, 8 cores each |

These projections assume signaling load only (no WebRTC media). Real-world TURN relay adds ~100kbps per relayed call, but most calls are peer-to-peer.
