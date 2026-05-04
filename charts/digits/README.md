# Digits Helm Chart

You almost certainly don't need this. The `docker compose` setup in `server/` is the intended deployment path and will get you running in about two minutes. This Helm chart exists because I run a homelab Kubernetes cluster and enjoy over-engineering things. It's a fun project, and part of the fun is deploying it like it's a real production service with HA Postgres, distributed tracing, and continuous profiling. Is any of that necessary for a three-phone network? Absolutely not. But here we are.

If you do happen to have a k8s cluster lying around and want to deploy digits there, this chart handles:

- signald and admind Deployments with security-hardened pod specs
- ClusterIP Services (with optional metrics ports)
- Ingress resources for external access
- CNPG PostgreSQL Clusters (userdb + admindb) with S3-compatible backups
- OpenTelemetry tracing, Pyroscope profiling, and Prometheus ServiceMonitors

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

admind:
  env:
    ADMIN_STATS_URL: "http://signald.digits.svc.cluster.local:8080/internal/stats"
  envFrom:
    - secretRef:
        name: digits-secrets
```

The `digits-secrets` Secret is created out-of-band (kubectl or sealed-secrets). It must contain at minimum `ADMIN_SECRET` for cross-service auth.

### CNPG (PostgreSQL)

Requires the [CloudNativePG operator](https://cloudnative-pg.io/) installed in the cluster.

```yaml
cnpg:
  enabled: true
  userdb:
    instances: 2
    size: 10Gi
    storageClass: longhorn  # your StorageClass
  admindb:
    instances: 2
    size: 5Gi
    storageClass: longhorn
  backup:
    enabled: true
    destinationPath: s3://cnpg-backups
    endpointURL: http://minio.minio.svc.cluster.local:9000
    s3CredentialsSecret: minio-cnpg-credentials
```

When enabled, the chart creates CNPG Cluster CRs. The operator generates Secrets (`<release>-userdb-app`, `<release>-admindb-app`) containing connection URIs that the deployments consume automatically.

### Ingress

```yaml
ingress:
  enabled: true
  className: traefik  # or nginx, etc.
  signald:
    host: app.example.com
  admind:
    host: admin.example.com
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
  admindMetricsPort: 9092
  serviceMonitor:
    enabled: true
    interval: 30s
```

When enabled, the deployments expose Prometheus metrics on dedicated ports and the chart creates ServiceMonitor resources (requires prometheus-operator/kube-prometheus-stack).

### Image tags

By default, image tags derive from `Chart.yaml`'s `appVersion` (prefixed with `v`). Override per-service:

```yaml
signald:
  image:
    tag: v1.57.0
admind:
  image:
    tag: v1.57.0
```

## Full values reference

See `values.yaml` for all available fields with defaults.
