# Line renumber rollout

Line renumber writes are frozen by the PostgreSQL `lines_renumber_gate` trigger after migration. The gate is durable, defaults to disabled, and applies to every writer, including server versions that predate the current renumber code. Calls, registration, settings, and other ordinary service remain available while the gate is disabled.

## Rolling deployment

1. Deploy the migration and current server normally. Do not enable renumber during the mixed-version period.
2. Retire every legacy server process, open WebSocket, and Redis consumer that predates identity-bound signaling. Confirm that no legacy process can reconnect or resume consuming.
3. Enable renumber manually:

```sql
BEGIN;
UPDATE renumber_control
SET enabled = TRUE, updated_at = NOW()
WHERE singleton;
COMMIT;
```

The row update is the activation boundary. It also latches `identity_cutover = TRUE`. That identity cutover never returns to false when the write gate is frozen, so zero-line-ID sockets, presence, and envelopes remain ineligible after first activation. The trigger holds a shared row lock for each in-flight line-number update, so activation or later disable waits for those transactions to finish.

## Freeze and rollback restriction

Freeze new renumbers before rollback:

```sql
BEGIN;
UPDATE renumber_control
SET enabled = FALSE, updated_at = NOW()
WHERE singleton;
COMMIT;
```

The committed update is the freeze boundary. A rollback to a server version without immutable line identity, connection generation fencing, and identity-bound Redis envelopes is not safe after any renumber has completed. Disabling the gate prevents additional number changes, but it does not make legacy sockets or consumers safe for numbers already changed or reused. Before rolling back application code, drain current and legacy sockets and Redis consumers, and resolve the already-renumbered device state operationally. Do not drop the trigger or `renumber_control` table during an application rollback.

## Uncertain in-flight guard recovery

Every process with identity-bound work records a token in `renumber_inflight_guards` before acquiring the shared PostgreSQL fence. Normal callback completion deletes its token in the lock-owning transaction. If that PostgreSQL session is lost or cleanup cannot be confirmed, the token remains and all renumber attempts fail closed. Ordinary service can continue, but number changes remain unavailable.

Recovery is manual and token-specific:

1. Freeze renumber writes by setting `renumber_control.enabled = FALSE`.
2. Snapshot the exact token set that exists at the start of recovery.
3. Retire or restart, using ordinary rolling replacement, every server process that could own any captured token. Verify that their callbacks, WebSockets, and Redis consumers have stopped.
4. Delete only the captured tokens. Never blanket-delete the table, and never delete tokens created after the snapshot.
5. Re-enable renumber only after the normal activation checks pass.

The absence of a PostgreSQL session is not proof that callback effects stopped. Do not clear a token based only on `pg_stat_activity` or advisory-lock absence. This procedure was documented only. No production rollout or recovery was performed.
