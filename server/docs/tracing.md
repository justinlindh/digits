# Tracing and Continuous Profiling

`signald` emits OpenTelemetry traces and Pyroscope continuous
profiles to a private observability stack. The stack lives in the
homelab Kubernetes cluster (Tempo for traces, Pyroscope for profiles,
Grafana Loki for logs); none of it is internet-reachable.

The data plane mirrors the metric design in `metrics.md`: every span
attribute, every event, every label is a privacy decision, not a routine
instrumentation toggle. Reviewers should treat any new attribute key
here as a privacy review, not a refactor.

## Endpoints

| Variable | Purpose | Default |
| --- | --- | --- |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OTLP collector host:port. Empty disables the trace exporter. | unset (off) |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | `grpc` (default) or `http`. | `grpc` |
| `OTEL_EXPORTER_OTLP_INSECURE` | `false` to require TLS. The cluster collector is on the cluster network, so the default is true. | `true` |
| `OTEL_TRACES_SAMPLER_ARG` | Head-based sample ratio, 0..1. | `1.0` |
| `OTEL_RESOURCE_ATTRIBUTES` | k=v[,k=v...] resource attributes. The k8s deployment uses this to set `cluster=homelab` and `service.namespace=digits`. | unset |
| `OTEL_SERVICE_NAME` | Override service name. Code-supplied (`signald`) is the preferred path. | unset |
| `PYROSCOPE_SERVER_ADDRESS` | Pyroscope HTTP ingest URL. Empty disables. | unset (off) |
| `PYROSCOPE_AUTH_TOKEN` | Bearer token for Grafana Cloud Pyroscope. Empty for in-cluster (no auth). | unset |
| `PYROSCOPE_TENANT_ID` | `X-Scope-OrgID` for multi-tenant Pyroscope. | unset |
| `DEPLOYMENT_ENV` | Operator-supplied environment tag (e.g. `k8s`, `docker`). | unset |

The W3C Trace Context propagator is installed unconditionally, so a
process running with no exporter still propagates inbound `traceparent`
headers through the request lifecycle. Turning the exporter on later
does not require any other change.

## What is collected

### HTTP server spans

One span per inbound request, emitted by `internal/tracing.HTTPServerMiddleware`.
Attributes:

- `http.route`: bucketed route from `internal/metrics.RouteOf`.
  Path components carrying user identifiers (phone numbers, call IDs,
  magic-link tokens, household IDs) collapse to template strings
  (`/phones/{number}`, `/api/call/{id}`, `/auth/magic/{token}`).
  Unknown paths collapse to `other`.
- `http.method`: `GET` / `POST` / etc.
- `http.status_code`: response status code.
- `server.address`: `Host` header (already a configured domain, never
  user-derived).

Span name shape: `<service>.http <route-bucket>` (e.g.
`signald.http /phones/{number}`).

The middleware deliberately does NOT use `otelhttp.NewHandler`. That
wrapper records `url.path`, `url.query`, and `url.full` as raw strings,
which would echo phone numbers and tokens into Tempo even with our
route override. We use the OTel API directly so the closed attribute
set is enforced at the integration boundary.

`/metrics`, `/healthz`, and `/static/*` are excluded from tracing
entirely. They are high-frequency, low-information requests that would
dominate the exporter without diagnostic value.

### Database spans

One span per SQL operation, emitted by `XSAM/otelsql` configured in
`internal/tracing.OpenSQLDB` and used by `internal/db.Open`.

Privacy: `otelsql` is configured with `SpanOptions.DisableQuery=true`
which suppresses `db.statement`. The default behavior would echo the
raw SQL into every span; for queries like
`SELECT id FROM users WHERE email = $1`, the surrounding query shape
plus a bound parameter makes the row identifiable even in placeholder
form. We disable the field outright. Operators who need to debug a
slow query can run `EXPLAIN` against the database directly.

What `otelsql` still records:

- `db.system: postgresql`
- `db.name`: the database from `DATABASE_URL` (`digits` in prod, `digits_test` under integration tests)
- operation kind (connection, query, prepare) and duration
- error attribute on failed calls

`OmitConnPrepare` and `OmitConnResetSession` are set to keep span volume
proportional to query volume.

### Process / runtime resource attributes

Applied to every span as part of the OTel resource:

- `service.name`: `signald`
- `service.version`: build version from `internal/version`
- `service.instance.id`: `os.Hostname()`. In k8s this is the pod name
  (e.g. `signald-7f44`); on the docker host this is the container ID.
  Neither maps back to a user.
- `host.name`: same.
- `service.commit`: short git commit hash.
- `process.runtime.*`: Go runtime version, name, description.
- whatever the operator sets via `OTEL_RESOURCE_ATTRIBUTES` (the k8s
  deployment uses this to set `cluster=homelab` and `service.namespace=digits`).

### Continuous profiles

Pushed by the Pyroscope Go SDK to `PYROSCOPE_SERVER_ADDRESS`. Profile
types: CPU, alloc objects, alloc space, inuse objects, inuse space,
goroutines. Mutex / block profiling is off by default and gated behind
explicit non-zero `MutexProfileFraction` / `BlockProfileRate` config.

Label set is closed:

- `service`: `signald` (set by the binary, not env)
- `version`: build version
- `hostname`: `os.Hostname()`
- `environment`: from `DEPLOYMENT_ENV` (operator-supplied)

The label-merge logic explicitly rejects operator overrides of
`service`, `version`, and `hostname`: a hostile manifest cannot claim
a profile is from a different binary or version. See
`internal/profiling/profiling_test.go::TestTagsAreClosedSet`.

### Log correlation

`internal/logging.traceContextHandler` decorates each `slog` record with
`trace_id` and `span_id` fields when the calling context carries a
recording span. The fields use the OTel canonical hex encoding (32 +
16 hex chars) so Loki's Tempo datasource link works without extra
mapping. Records logged outside a traced context get no extra fields:
the structured-log shape stays backwards-compatible.

`trace_id` and `span_id` are random 128- and 64-bit identifiers. They
do not encode user identity. The trace they refer to in Tempo carries
only the privacy-respecting attributes documented above.

## What is NEVER collected

This is the same closed-set discipline used in `metrics.md`, applied to
spans, span events, and profile labels:

- Per-user identity: `user_id`, `email`, `household_id`, line / phone
  number, OAuth subject, magic-link token, password hash, session
  cookie. None of these reach a span attribute or a profile label.
- Call identifiers that map back to participants. The route bucketer
  collapses `/api/call/{id}` so a Tempo trace cannot be cross-referenced
  with the `calls` table to identify who talked to whom.
- IP addresses (even hashed). The `server.address` attribute carries
  the configured service hostname (`signald.digits.svc.cluster.local`),
  not the client's IP.
- Geographic data, device locations, or anything derivable from same.
- Free-form user content: call SDP, audio, message bodies.
- Database query parameters: `db.statement` is suppressed across the
  board. Per-row events (`RowsNext`) are off.
- Outbound URL paths beyond the host: `url.full` and `url.path` are
  not emitted by either our server or client middleware. The host is
  recorded as `server.address`.

If you are tempted to add a new attribute, ask the same question
metrics.md asks: "if a parent worried about their kid's privacy saw
this attribute on a public dashboard, would they be uncomfortable?" If
yes, do not add it.

## Where it goes

The homelab cluster runs an OTel collector, Tempo, Pyroscope, and Loki
side by side. Routing:

- OTel collector receives OTLP gRPC on
  `otel-collector-opentelemetry-collector.observability.svc.cluster.local:4317`.
  It fans out to Tempo (traces) and the existing logs pipeline.
- Pyroscope server is on
  `pyroscope.observability.svc.cluster.local:4040`. The Go SDK pushes
  via HTTP.
- Loki is the existing aggregation target; the trace-id correlation
  works via Grafana's Tempo datasource using the `trace_id` field on
  log records.

The k8s deployment manifests in `homelab-k8s/manifests/digits/` set the
env vars; the docker prod deployment is unchanged for now, with the
exporter staying disabled until the k8s cutover.

## Tests

Privacy invariants are pinned by:

- `internal/tracing/tracing_test.go::TestHTTPServerMiddlewareNeverEchoesNumber`:
  hits PII-bearing paths and asserts no span name or attribute contains
  the raw segments.
- `internal/tracing/tracing_test.go::TestSpanAttrsForbidPII`: a wildcard
  unknown path collapses to the `other` route bucket, never echoing the
  raw URL.
- `internal/profiling/profiling_test.go::TestTagsAreClosedSet`: the
  reserved `service` / `version` / `hostname` profile labels cannot be
  overwritten by operator-supplied tags.
- `internal/profiling/profiling_test.go::TestNoPIIInTagSet`: the
  production `NewConfig` does not populate any user-shaped tag key.
- `internal/logging/logging_test.go::TestTraceContextHandlerNoPII`: the
  `trace_id` field is pure hex, with no non-hex characters that could
  smuggle in a user identifier.

The test for `metrics.RouteOf` (`internal/metrics/metrics_test.go::TestRouteOfNeverEchoesNumber`)
covers the same bucketer this package uses for span names and the
`http.route` attribute, so a regression on the metrics side trips both
test suites.
