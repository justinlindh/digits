# Metrics

`signald` exposes a Prometheus `/metrics` endpoint on a
**separate listener** from public traffic. Public-facing requests cannot
reach the metrics endpoint, and metrics requests are not authenticated by
the operator (the listener is expected to be private; binding it to
`127.0.0.1` is supported for single-node Docker deployments and is the
recommended default for a host-network compose setup).

The metric design is bound by the digits anti-surveillance pitch (see
`docs/mission.md` and `docs/why-digits.md`). Reviewers should treat any
new label as a privacy decision, not a routine refactor.

## Listener config

| Variable | Default | Notes |
| --- | --- | --- |
| `SIGNALD_METRICS_ADDR` | `:9091` | Empty disables the listener. Bind to `127.0.0.1:9091` if the host network is shared with other services. |

The Prometheus server should scrape these as static targets. There is
deliberately no auth on the listener: the listener is not on the public
listener's port, it is not behind the public TLS terminator, and a leaked
metric value cannot be turned into a privacy incident the way a leaked
session cookie can.

## Metrics collected

All metric names are prefixed with `digits_signald_`.

### HTTP

- `digits_<svc>_http_requests_total{route, method, status}` (counter):
  count of HTTP requests handled. `route` is a coarse route group (e.g.
  `/phones/{number}`) chosen by the static `RouteOf` mapper in
  `internal/metrics`. Path components carrying user identifiers (phone
  numbers, call IDs, magic-link tokens) are collapsed into the template,
  not echoed into the label. Unknown paths collapse to `other`.
- `digits_<svc>_http_request_duration_seconds{route, method, status}`
  (histogram): wall-clock duration of HTTP responses, with the same label
  set as the counter and Prometheus default buckets.

### Live state (signald only)

- `digits_signald_active_devices_current` (gauge): currently connected
  phones, sampled at scrape time from `Hub.OnlineNumbers()`. Count only;
  no identifiers are recorded.
- `digits_signald_active_calls_current` (gauge): currently active calls,
  sampled from `Tracker.Active()`. Count only; no participants.

### Signaling errors (signald only)

- `digits_signald_signaling_errors_total{category}` (counter): signaling
  errors observed by the relay. `category` is a closed set defined in code
  (see `internal/metrics/metrics.go`):
  - `peer_unreachable`: a CALL was sent to a phone not connected.
  - `auth_failed`: the call authorizer denied the call.
  - `call_setup_failed`: the tracker failed to record a call initiation.
  - `invalid_message`: the relay received a malformed or out-of-context
    message (e.g. `ICE_RESTART` with no active call).
  - `relay_delivery`: a forward to a peer's WebSocket failed.
  - `turn_alloc_failed`, `ice_timeout`: reserved for future use.
  - Any value not in the closed set collapses to `other`. The relay
    package cannot construct an arbitrary category; this is enforced by
    the helper in `internal/metrics`.

No peer identity, phone number, call ID, conference ID, or content is
included in any signaling-error label.

### Build info

- `digits_<svc>_build_info{version, commit}` (gauge, always 1): allows a
  dashboard to display the running build. The version comes from
  `internal/version`, populated at build time via `-ldflags`.

### Process and runtime

`promhttp` registers the standard `process_*` and `go_*` collectors.
This includes goroutine count, GC pause, RSS, FD count, and the like.
None of these expose user data.

## What is NEVER collected

- Per-user request counts, per-user latency, per-user anything.
- Per-call duration, per-call participants, per-call routing.
- Caller or callee phone numbers in any form.
- IP addresses (even hashed). Correlation makes a hashed IP equivalent
  to plaintext IP for an attacker who can guess a few candidates.
- Geographic data, device locations, or anything derivable from same.
- Contact-graph data (who can call whom).
- Magic-link emails, OAuth identities, or session tokens.
- Free-form text from user content.

If you are tempted to add a new label, ask: "if a parent worried about
their kid's privacy saw this metric on a public dashboard, would they be
uncomfortable?" If yes, do not add it.

## Where to scrape

The Prometheus server in the homelab cluster scrapes the docker prod
deployment as static targets via `additionalScrapeConfigs`. The k8s
shadow stack (in the `digits` namespace) exposes the same metrics ports
on the Pod, picked up by a `ServiceMonitor` for the long-term path. Both
can run simultaneously; Prometheus distinguishes them via the `instance`
label.

See the `homelab-k8s` repo for the actual scrape configuration:

- `helm/kube-prometheus-stack-values.yaml` for the docker static target.
- `manifests/digits/servicemonitor-*.yaml` for the in-cluster scrape.
- `manifests/grafana-dashboard-digits-server.yaml` for the dashboard.
