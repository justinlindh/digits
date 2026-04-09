# Valkey Cache Integration Plan

Status: **deferred** -- not needed at current scale, documented here for future reference.

## Why this exists

Several server operations hit Postgres for data that is ephemeral or read-heavy:

| Area | Current behavior | Frequency |
|------|-----------------|-----------|
| LastSeen | `UPDATE devices SET last_seen_at = NOW()` per WebSocket pong | Every 30s per connected device |
| Session validation | SELECT + UPDATE (refresh TTL) per authenticated HTTP request | Every page load |
| Magic links | INSERT + atomic UPDATE, hourly cleanup goroutine | Per login attempt, 15min TTL |
| Pairing codes | Upsert on device row, hourly cleanup goroutine | Per pairing attempt, 10min TTL |

A Valkey (Redis-compatible) cache could offload these hot paths while keeping Postgres as the source of truth.

## Why we're not doing it yet

With a handful of phones and a few users, Postgres handles all of this without breaking a sweat. The write volume is negligible and latency is not a concern. Adding Valkey would mean:

- A new container to run and monitor
- A new failure mode (cache availability, invalidation bugs)
- More code to maintain for no measurable user-facing improvement

**Revisit when:** device count grows past ~20, concurrent web users grow past ~50, or Postgres connection pool pressure becomes observable.

## Design (if/when we proceed)

### Library

`github.com/redis/go-redis/v9` -- most widely used Go Redis client, Valkey wire-compatible, supports GETDEL and pipelining.

### New package: `server/internal/cache/`

```
cache.go          -- Cache struct (wraps *redis.Client), New(), Available(), Close()
lastseen.go       -- BufferLastSeen(), FlushLastSeen()
sessions.go       -- GetSession(), SetSession(), InvalidateSession()
magiclinks.go     -- StoreMagicLink(), ConsumeMagicLink()
pairingcodes.go   -- SetPairingCode(), GetPairingHardwareID(), DeletePairingCode()
```

### Core design

```go
type Cache struct {
    rdb       *redis.Client
    available atomic.Bool
}
```

- `New(addr string)` connects and pings. If ping fails, logs a warning and sets `available = false` (does not fatal).
- Background goroutine pings every 10s, flips `available` on failure/recovery.
- Every public method checks `Available()` first. If false, returns a sentinel indicating "use Postgres." Callers never need to know about Valkey internals.

### Configuration

Single env var: `SIGNALD_VALKEY_URL` (e.g., `valkey:6379`). Empty or unset = Valkey disabled, pure Postgres behavior.

### Docker Compose

Add to both `docker-compose.yml` and `docker-compose.prod.yml`:

```yaml
valkey:
  image: valkey/valkey:8-alpine
  restart: unless-stopped
  volumes:
    - valkey_data:/data
  healthcheck:
    test: ["CMD", "valkey-cli", "ping"]
    interval: 5s
    timeout: 3s
    retries: 5
```

No external port exposure -- internal Docker networking only.

### Migration 1: LastSeen buffering

- `HSET lastseen <hardwareID> <unix_timestamp>` -- single hash key holds all pending updates
- Flush goroutine (every 60s): atomically RENAME the hash, HGETALL the renamed copy, batch UPDATE Postgres, DEL
- On disconnect, also write directly to Postgres so the final timestamp is immediately visible
- Fallback: if cache unavailable, call `deviceStore.TouchLastSeen()` directly (current behavior)

**Files:** new `cache/lastseen.go`, edit `web/handler.go` (3 TouchLastSeen call sites), edit `main.go` (flush goroutine)

### Migration 2: Magic links

- Key: `magiclink:{tokenHash}`, Value: email, TTL: 15min
- `ConsumeMagicLink` uses `GETDEL` for atomic single-use consumption
- When Valkey is available, magic links are Valkey-only (no Postgres write). If Valkey restarts, users just request a new link.
- Fallback: existing Postgres path when Valkey unavailable

**Files:** new `cache/magiclinks.go`, edit `auth/store.go` (CreateMagicLink, ValidateMagicLink)

### Migration 3: Pairing codes

- Key: `pairing:{code}`, Value: hardwareID, TTL: 10min
- Postgres remains source of truth (ClaimDevice modifies the device row)
- Valkey serves as a fast lookup index; GenerateCode/RegenerateCode dual-write
- Fallback: existing Postgres path when Valkey unavailable

**Files:** new `cache/pairingcodes.go`, edit `pairing/pairing.go`

### Migration 4: Session caching

- Key: `session:{tokenHash}`, Value: JSON-encoded Session, TTL: 5min
- Cache hit skips both the SELECT and the RefreshSession UPDATE
- On logout (DeleteSession): delete from cache, then from Postgres
- 5min cache TTL means session refresh writes happen at most once per 5min instead of every request

**Files:** new `cache/sessions.go`, edit `auth/store.go`, edit `auth/middleware.go`

### Cleanup goroutine changes

After full integration:
- Keep session cleanup in Postgres (hourly)
- Reduce magic link cleanup to daily (fallback only)
- Reduce pairing code cleanup to every 6 hours
- Add LastSeen flush goroutine (every 60s)

### Fallback / rollback

Three safety layers:
1. **Env var off**: unset `SIGNALD_VALKEY_URL` and the server runs exactly as today
2. **Runtime degradation**: if Valkey crashes, health monitor sets `available = false` within 10s, all paths fall through to Postgres
3. **Data safety**: Postgres is source of truth for everything except in-flight magic links

### Testing

- Unit tests with `github.com/alicebob/miniredis/v2` (in-process Redis, no Docker needed)
- Fallback tests: Cache with `available = false`, verify Postgres paths run
- Manual: docker compose up with Valkey, verify operation; stop Valkey, verify graceful degradation
