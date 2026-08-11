# OpenTelemetry

buoy exports traces, metrics, and logs to any OTLP-compatible backend (Grafana Cloud, OTel Collector, etc.). All three signals are disabled when no OTLP endpoint is configured.

Configuration is done entirely through [standard OpenTelemetry environment variables](https://opentelemetry.io/docs/languages/sdk-configuration/otlp-exporter/). No buoy-specific config keys or flags needed.

## Quick start

```bash
OTEL_EXPORTER_OTLP_ENDPOINT=http://collector:4318 ./buoy run
```

That's it. buoy automatically initializes traces, metrics, and logs when `OTEL_EXPORTER_OTLP_ENDPOINT` is set.

## Common env vars

| Variable | Default | Description |
|----------|---------|-------------|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | _(empty)_ | OTLP collector address. When set, telemetry is enabled. |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | `grpc` | Transport: `grpc` or `http/protobuf` |
| `OTEL_EXPORTER_OTLP_INSECURE` | `false` | Set to `true` to skip TLS |
| `OTEL_SERVICE_NAME` | _(auto)_ | Override `service.name` resource attribute (defaults to `buoy` from code) |
| `OTEL_RESOURCE_ATTRIBUTES` | - | Additional resource attributes. Recommended in Docker: set `host.name=<host>` so each deployment is identifiable. |

> [!NOTE]
> The OTel SDK auto-reads all `OTEL_*` env vars. Endpoint, TLS, headers, compression, timeouts - everything is configured the standard way. See the [OTel SDK docs](https://opentelemetry.io/docs/languages/sdk-configuration/otlp-exporter/) for the full list.

- **Traces** - every backup cycle is a trace with child spans for each phase: container stop, restic backup (per mount), container start, hooks, retention, dependency waits.
- **Metrics** - all metrics below. Go runtime metrics (GC, memory) included automatically.
- **Log correlation** - existing slog output is bridged to OTLP. Every log line inside a span gets `trace_id` and `span_id`. Click a span in Tempo → see the logs.

## Metrics

All metrics share the instrumentation scope `buoy`. When ingested by Prometheus via remote write, dots become underscores and units are appended as suffixes.

### `buoy.backup.duration`

| Field | Value |
|---|---|
| Type | Float64Histogram |
| Unit | `s` |
| Buckets | 1, 5, 10, 30, 60, 120, 300, 600, 1800, 3600 |
| Description | Duration of restic backup per mount |

**Attributes:** `container` (string), `service` (string), `project` (string), `repo` (string), `mount` (string), `success` (bool)

### `buoy.backup.runs`

| Field | Value |
|---|---|
| Type | Int64Counter |
| Unit | `{run}` |
| Description | Total number of completed backup runs (one per container per cycle) |

**Attributes:** `container` (string), `service` (string), `project` (string), `mounts` (int), `success` (bool)

### `buoy.backup.last_success`

| Field | Value |
|---|---|
| Type | Int64ObservableGauge |
| Unit | `s` |
| Description | Unix timestamp of last successful backup per container |

**Attributes:** `container` (string), `service` (string), `project` (string)

### `buoy.backup.last_duration`

| Field | Value |
|---|---|
| Type | Float64Gauge |
| Unit | `s` |
| Description | Duration of the last completed backup run per container (updated after every run, so per-run history is visible as steps) |

**Attributes:** `container` (string), `service` (string), `project` (string), `success` (bool)

### `buoy.containers.active`

| Field | Value |
|---|---|
| Type | Int64ObservableGauge |
| Unit | `{container}` |
| Description | Number of containers currently discovered and scheduled for backup |

### `buoy.container.stop.duration`

| Field | Value |
|---|---|
| Type | Float64Histogram |
| Unit | `s` |
| Buckets | 0.1, 0.5, 1, 2, 5, 10, 30, 60, 120 |
| Description | Duration of container stop operations |

**Attributes:** `container` (string), `service` (string), `project` (string), `success` (bool)

### `buoy.container.start.duration`

| Field | Value |
|---|---|
| Type | Float64Histogram |
| Unit | `s` |
| Buckets | 0.1, 0.5, 1, 2, 5, 10, 30, 60, 120 |
| Description | Duration of container start operations |

**Attributes:** `container` (string), `service` (string), `project` (string), `success` (bool)

### `buoy.hook.duration`

| Field | Value |
|---|---|
| Type | Float64Histogram |
| Unit | `s` |
| Buckets | 0.1, 0.5, 1, 2, 5, 10, 30, 60, 120 |
| Description | Duration of hook command execution |

**Attributes:** `container` (string), `service` (string), `project` (string), `type` (`pre` / `post`), `target` (`host` / `container`), `success` (bool)

### `buoy.retention.duration`

| Field | Value |
|---|---|
| Type | Float64Histogram |
| Unit | `s` |
| Buckets | 1, 5, 10, 30, 60, 120, 300, 600, 1800 |
| Description | Duration of retention operations (forget/prune) |

**Attributes:** `container` (string), `service` (string), `project` (string), `repo` (string)

### `buoy.check.duration`

| Field | Value |
|---|---|
| Type | Float64Histogram |
| Unit | `s` |
| Buckets | 1, 5, 10, 30, 60, 120, 300, 600, 1800, 3600 |
| Description | Duration of restic repository check operations |

## Local development

### otel-desktop-viewer

[otel-desktop-viewer](https://github.com/CtrlSpice/otel-desktop-viewer) is a lightweight desktop app that visualizes OTLP traces and metrics.

```yaml
# compose.yaml
services:
  otel-desktop-viewer:
    image: ghcr.io/ctrlspice/otel-desktop-viewer:latest
    ports:
      - 4317:4317   # OTLP gRPC
      - 4318:4318   # OTLP HTTP
      - 8000:8000   # Web UI
```

```bash
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 ./buoy run
```

Open `http://localhost:8000` to see traces.

### Grafana / Tempo / Loki

For local development, `otel-lgtm` bundles Grafana, Tempo, Loki, and Prometheus into a single container:

```yaml
# compose.yaml
services:
  lgtm:
    image: grafana/otel-lgtm:latest
    ports:
      - 3000:3000   # Grafana
      - 4317:4317   # OTLP gRPC
      - 4318:4318   # OTLP HTTP
    volumes:
      - lgtm_data:/data

volumes:
  lgtm_data:
```

> [!WARNING]
> `otel-lgtm` is a **development image** - not suitable for production.

```bash
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317 OTEL_EXPORTER_OTLP_INSECURE=true ./buoy run
```

Grafana is at `http://localhost:3000`. Traces in Tempo, logs in Loki, metrics in Prometheus - all pre-wired.

## Production deployment

For production, run separate services:

```yaml
# compose.yaml
services:
  collector:
    image: otel/opentelemetry-collector-contrib:0.157.0
    command: ["--config=/etc/otel/config.yaml"]
    ports:
      - 4317:4317   # OTLP gRPC
      - 4318:4318   # OTLP HTTP
    volumes:
      - ./collector.yaml:/etc/otel/config.yaml:ro

  tempo:
    image: grafana/tempo:latest
    command: ["-config.file=/etc/tempo.yaml"]
    volumes:
      - ./tempo.yaml:/etc/tempo.yaml:ro
      - tempo_data:/tmp/tempo

  loki:
    image: grafana/loki:latest
    volumes:
      - loki_data:/loki

  prometheus:
    image: prom/prometheus:latest
    volumes:
      - ./prometheus.yml:/etc/prometheus/prometheus.yml:ro
      - prometheus_data:/prometheus

  grafana:
    image: grafana/grafana:latest
    ports:
      - 3000:3000
    volumes:
      - grafana_data:/var/lib/grafana

volumes:
  tempo_data:
  loki_data:
  prometheus_data:
  grafana_data:
```

Collector config (`collector.yaml`):

```yaml
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317
      http:
        endpoint: 0.0.0.0:4318

exporters:
  otlp/tempo:
    endpoint: tempo:4317
    tls:
      insecure: true
  loki:
    endpoint: http://loki:3100/loki/api/v1/push
  prometheusremotewrite:
    endpoint: http://prometheus:9090/api/v1/write
    resource_to_telemetry_conversion:
      enabled: true

service:
  pipelines:
    traces:
      receivers: [otlp]
      exporters: [otlp/tempo]
    logs:
      receivers: [otlp]
      exporters: [loki]
    metrics:
      receivers: [otlp]
      exporters: [prometheusremotewrite]
```

Then point buoy at the collector:

```bash
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317 OTEL_EXPORTER_OTLP_INSECURE=true ./buoy run
```

Add Tempo (`http://tempo:3200`), Loki (`http://loki:3100`), and Prometheus (`http://prometheus:9090`) as data sources in Grafana at `http://localhost:3000`.

## Span names

| Span                       | Context                                   |
| -------------------------- | ----------------------------------------- |
| `buoy.schedule.run`        | Cron trigger wrapping full backup cycle   |
| `buoy.backup`              | Standalone container backup               |
| `buoy.stack.backup`        | Compose stack batch backup                |
| `buoy.container.stop`      | Container stop (standalone or stack)      |
| `buoy.container.start`     | Container start + health check wait       |
| `buoy.restic.backup`       | Per-mount restic invocation               |
| `buoy.hook.pre.host` / `.exec` | Pre-backup hook execution             |
| `buoy.hook.post.host` / `.exec` | Post-backup hook execution           |
| `buoy.container.wait`      | Docker event polling (health/dependency)  |
| `buoy.startup_scan`        | Initial container discovery               |
| `buoy.resync`              | Periodic label resync                     |
| `buoy.check`               | Periodic restic check                     |

Hooks only appear when configured - no empty spans when no labels are set.

## PromQL examples

All metric names use underscores in Prometheus (OTel dots → underscores, unit suffix appended). Use `sum by(container)`, `sum by(project)`, or `sum by(repo)` to slice per entity. For counters (backup runs), use `offset 24h` subtraction instead of `increase` -- it works instantly without extrapolation on fresh data.

| Panel | PromQL |
|---|---|
| Containers active | `buoy_containers_active` |
| Per-container duration | `buoy_backup_last_duration_seconds{...}` (gauge — one step per run) |
| Successful runs (24h) | `sum(increase(buoy_backup_runs_total{success="true"}[$__range]))` |
| Failed runs (24h) | `sum(increase(buoy_backup_runs_total{success="false"}[$__range]))` |
| Error rate (1h) | `sum(rate(buoy_backup_runs_total{success="false"}[1h])) / clamp_min(sum(rate(buoy_backup_runs_total[1h])), 1)` |
| Healthy containers | `count(buoy_backup_last_success_seconds > (time() - 86400))` |
| Stale containers | `count(buoy_backup_last_success_seconds < (time() - 86400))` |
| Backup duration by container | `sum by(container)(buoy_backup_duration_seconds_sum) / sum by(container)(buoy_backup_duration_seconds_count)` |
| Backup duration by project | `sum by(project)(buoy_backup_duration_seconds_sum) - sum by(project)(buoy_backup_duration_seconds_sum offset 24h or vector(0))` |
| Backup duration by repo | `sum by(repo)(buoy_backup_duration_seconds_sum) / sum by(repo)(buoy_backup_duration_seconds_count)` |
| Container stop by container | `sum by(container)(buoy_container_stop_duration_seconds_sum) / sum by(container)(buoy_container_stop_duration_seconds_count)` |
| Container start by container | `sum by(container)(buoy_container_start_duration_seconds_sum) / sum by(container)(buoy_container_start_duration_seconds_count)` |
| Hook duration p95 | `histogram_quantile(0.95, sum by(type, le)(rate(buoy_hook_duration_seconds_bucket[$__rate_interval])))` |
| Retention duration p95 | `histogram_quantile(0.95, sum by(le)(rate(buoy_retention_duration_seconds_bucket[$__rate_interval])))` |
| Check duration p95 | `histogram_quantile(0.95, sum by(le)(rate(buoy_check_duration_seconds_bucket[$__rate_interval])))` |
| Last backup age | `time() - buoy_backup_last_success_seconds` |
| Errors & warnings (Loki) | `{service_name="buoy"} \| severity_text=~"ERROR\|WARN"` |

> [!NOTE]
> **Graceful degradation.** Each signal initializes independently. An unreachable collector at startup disables only that signal (logged as a warning). The daemon always starts. When no OTLP endpoint is set, all spans and metrics are no-op - zero overhead.
