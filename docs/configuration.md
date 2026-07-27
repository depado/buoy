# Configuration

## Default config file

```yaml
log:
  level: info
  format: json
  source: false
  color: auto

daemon:
  concurrency: 2
  default_schedule: ""
  default_retention: "keep-within:7d,keep-daily:7,keep-weekly:4,keep-monthly:6,keep-yearly:3"
  resync_interval: "5m"
  exec_timeout: "5m"
  health_wait_timeout: "5m"
  backup_timeout: "1h"
  check_schedule: "@weekly"
  db_path: "./buoy.db"

docker:
  host: unix:///var/run/docker.sock

restic:
  binary_path: restic
  password: ""
  compression: auto
  repos:
    local:
      url: /backup

api:
  enabled: true
  host: "0.0.0.0"
  port: 8080
  token: ""

notify:
  urls: []
  level: error
```

## Password

buoy supports three password levels for maximum flexibility:

| Level         | Source                                                                           | Priority                                     |
| ------------- | -------------------------------------------------------------------------------- | -------------------------------------------- |
| Global fallback | `restic.password` config / `BUOY_RESTIC_PASSWORD` env / `--restic.password` flag | Lowest — used when nothing else is set       |
| Per-repo      | `password` field on each named repo                                              | Middle — overrides the global fallback       |
| Per-container | `buoy.password` Docker label                                                     | Highest — overrides both per-repo and global |

buoy passes all passwords to restic via a temporary `--password-file` rather than the `RESTIC_PASSWORD` environment variable.

### Env var format for named repos

```sh
BUOY_RESTIC_REPO_LOCAL_URL=/backup
BUOY_RESTIC_REPO_LOCAL_PASSWORD=local-password
BUOY_RESTIC_REPO_S3_URL=s3:https://mybucket/backups
BUOY_RESTIC_REPO_S3_PASSWORD=s3-password
```

Repo names must match `[a-zA-Z0-9][a-zA-Z0-9_-]*`.

### Per-container override

```yaml
services:
  app:
    labels:
      buoy.enabled: "true"
      buoy.password: "${APP_RESTIC_PASSWORD}"
```

This overrides all repo-level and global passwords for that container.

## Notifications

buoy can send failure notifications via [shoutrrr](https://github.com/nicholas-fedor/shoutrrr), supporting 50+ services including Slack, Discord, Telegram, email, and Gotify. Configure one or more shoutrrr URLs and set the notification level:

- `error` — notify on backup failures only (default)
- `all` — notify on all backup events
- `none` — disable notifications (or omit config)

Each URL encodes both the service and its credentials. Examples:

| Service      | URL format                                                                     |
| ------------ | ------------------------------------------------------------------------------ |
| Slack        | `slack://hook:tokenA-tokenB-tokenC@webhook`                                    |
| Discord      | `discord://token@channel`                                                      |
| Telegram     | `telegram://token@telegram?chats=@channel`                                     |
| Gotify       | `gotify://host:port/token`                                                     |
| Email (SMTP) | `smtp://user:pass@host:port/?from=sender@example.com&to=recipient@example.com` |

See [shoutrrr's documentation](https://containrrr.dev/shoutrrr/latest/services/overview/) for the full list of services and URL formats.

> [!NOTE]
> Notifications are best-effort — a failure logs a warning but never blocks or fails a backup.

## Periodic Repository Check

buoy can periodically verify the structural integrity of all restic repositories with `restic check`. This is a lightweight verification that reads the repository index and ensures all pack files are referenced correctly.

Configure via `daemon.check_schedule` (default: `@weekly`). Set to `""` to disable.

```yaml
daemon:
  check_schedule: "@weekly"
```

When the check runs, buoy reads known repositories from its persistent state database (`buoy.db`). Failures are logged and optionally trigger notifications. This is a structural check only — it does not read pack file data (use the CLI or API for `restic check --read-data` if needed).

## State Persistence

buoy maintains a [bbolt](https://github.com/etcd-io/bbolt) database at the path configured by `daemon.db_path` (default `./buoy.db`). This database tracks every repository buoy has ever managed: when it was created, when it was last backed up, and whether the associated container still exists.

This enables:

- **Orphaned repo detection**: repos belonging to removed containers are tracked rather than forgotten, so you can still run retention, integrity checks, or manually clean them up
- **Cross-restart awareness**: the repository list survives daemon restarts

The database is a single file. Mount a volume or bind mount at the directory containing it to persist state across container recreates.

## OpenTelemetry

buoy can export traces, metrics, and logs to OTLP-compatible backends. See **[OpenTelemetry](otel.md)**.

## HTTP API

buoy exposes a read/write HTTP API on `api.host:api.port` (default `0.0.0.0:8080`) for querying and operating on repositories. Authentication is via a Bearer token (`api.token`); when the token is empty, no authentication is required.

| Method | Path                   | Description                                                             |
| ------ | ---------------------- | ----------------------------------------------------------------------- |
| `GET`  | `/api/v1/health`       | Health check (no auth)                                                  |
| `GET`  | `/api/v1/scheduled`    | List currently scheduled backups                                        |
| `GET`  | `/api/v1/repos`        | List all known repos. `?orphaned=true` to show only orphaned.           |
| `POST` | `/api/v1/repos/check`  | Run `restic check` on all repos. `?read-data=true` for full check.      |
| `POST` | `/api/v1/repos/stats`  | Aggregate `restic stats` across all repos.                              |
| `POST` | `/api/v1/repos/unlock` | Unlock all repos.                                                       |
| `POST` | `/api/v1/repos/forget` | Run `restic forget` with `?retention=keep-daily:7,...`.                 |
| `POST` | `/api/v1/repos/prune`  | Run `restic prune` on all repos.                                        |
| `POST` | `/api/v1/backup`       | Trigger backup. `?all=true`, `?project=<name>`, or `?container=<name>`. |

Connect to the API from a local or remote buoy CLI, or use it as the backend for a dashboard/aggregator.
