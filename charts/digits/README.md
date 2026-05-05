# Digits Helm Chart

You almost certainly don't need this. The `docker compose` setup in `server/` is the intended deployment path and will get you running in about two minutes. This Helm chart exists because I run a homelab Kubernetes cluster and enjoy over-engineering things. It's a fun project, and part of the fun is deploying it like it's a real production service with HA Postgres, distributed tracing, and continuous profiling. Is any of that necessary for a three-phone network? Absolutely not. But here we are.

If you do happen to have a k8s cluster lying around and want to deploy digits there, this chart handles:

- signald Deployment with security-hardened pod spec
- ClusterIP Service (with optional metrics port)
- Ingress resource for external access
- CNPG PostgreSQL Cluster (userdb) with S3-compatible backups
- OpenTelemetry tracing, Pyroscope profiling, and Prometheus ServiceMonitor

## Install

```bash
helm install digits ./charts/digits -n digits --create-namespace -f my-values.yaml
```

## Configuration

All features are off by default. Enable what your cluster provides.

### Required values

```yaml
signald:
  env:
    BASE_URL: "https://app.example.com"
    COOKIE_DOMAIN: ".example.com"
    GOOGLE_REDIRECT_URL: "https://app.example.com/auth/google/callback"
    SMTP_FROM: "noreply@example.com"
  envFrom:
    - secretRef:
        name: digits-secrets  # must contain ADMIN_SECRET, SMTP_*, GOOGLE_*, SIGNALD_TURN_*
```

The `digits-secrets` Secret is created out-of-band (kubectl or sealed-secrets). It must contain at minimum `ADMIN_SECRET` for the internal stats API.

### CNPG (PostgreSQL)

Requires the [CloudNativePG operator](https://cloudnative-pg.io/) installed in the cluster.

```yaml
cnpg:
  enabled: true
  userdb:
    instances: 2
    size: 10Gi
    storageClass: longhorn  # your StorageClass
  backup:
    enabled: true
    destinationPath: s3://cnpg-backups
    endpointURL: http://minio.minio.svc.cluster.local:9000
    s3CredentialsSecret: minio-cnpg-credentials
```

When enabled, the chart creates a CNPG Cluster CR. The operator generates a Secret (`<release>-userdb-app`) containing the connection URI that the deployment consumes automatically.

### Ingress

```yaml
ingress:
  enabled: true
  className: traefik  # or nginx, etc.
  signald:
    host: app.example.com
```

TLS is expected to terminate upstream (load balancer or reverse proxy). The chart does not manage certificates.

### Observability

```yaml
observability:
  enabled: true
  otelEndpoint: "otel-collector.observability.svc.cluster.local:4317"
  otelProtocol: "grpc"
  pyroscopeEndpoint: "http://pyroscope.observability.svc.cluster.local:4040"
  signaldMetricsPort: 9091
  serviceMonitor:
    enabled: true
    interval: 30s
```

When enabled, the deployment exposes Prometheus metrics on a dedicated port and the chart creates a ServiceMonitor resource (requires prometheus-operator/kube-prometheus-stack).

### Multi-replica signaling (Redis)

Single-replica is the default. To run more than one signald pod, configure a
Redis instance the pods can share so cross-pod calls reach the right device:

```yaml
signald:
  replicas: 2

redis:
  url: "redis://redis.redis.svc.cluster.local:6379"
```

When `redis.url` is empty, signaling is local-only and replica counts above 1
will silently drop calls whose target is on a different pod. For passwords or
rotating credentials, leave `redis.url` empty and inject `REDIS_URL` via
`signald.envFrom` referencing your own Secret.

### Image tags

By default, image tags derive from `Chart.yaml`'s `appVersion` (prefixed with `v`). Override:

```yaml
signald:
  image:
    tag: v1.57.0
```

## Full values reference

See `values.yaml` for all available fields with defaults.
