<p align="center">
  <img alt="buoy" src="https://shieldcn.dev/header/grid.svg?title=buoy&subtitle=Restic-powered+Docker+volume+and+mounts+backups.&logo=lu%3ALifeBuoy&mode=dark&align=left&border=false">
</p>

<p align="center">
  A label-driven, compose-aware backup daemon with hooks, notifications, and automatic retention - powered by <a href="https://restic.net">restic</a>, on your schedule.
</p>

<p align="center">
  <a href="https://github.com/depado/buoy">GitHub</a> ·
  <a href="https://github.com/depado/buoy/releases">Releases</a>
</p>

<p align="center">
  <a href="https://github.com/depado/buoy/actions"><img src="https://shieldcn.dev/github/ci/depado/buoy.svg?variant=branded" alt="CI" /></a>
  <a href="https://github.com/depado/buoy/releases"><img src="https://shieldcn.dev/github/release/depado/buoy.svg?variant=branded" alt="Release" /></a>
  <a href="https://github.com/depado/buoy/blob/main/LICENSE"><img src="https://shieldcn.dev/github/license/depado/buoy.svg?variant=branded" alt="License" /></a>
  <a href="https://github.com/depado/buoy"><img src="https://shieldcn.dev/github/last-commit/depado/buoy.svg?variant=branded" alt="Last Commit" /></a>
  <a href="https://github.com/depado/buoy"><img src="https://shieldcn.dev/github/stars/depado/buoy.svg?variant=branded" alt="Stars" /></a>
  <a href="https://github.com/depado/buoy/graphs/contributors"><img src="https://shieldcn.dev/github/contributors/depado/buoy.svg?variant=branded" alt="Contributors" /></a>
  <a href="https://github.com/depado/buoy/issues"><img src="https://shieldcn.dev/github/issues/depado/buoy.svg?variant=branded" alt="Issues" /></a>
  <a href="https://github.com/depado/buoy/pkgs/container/buoy"><img src="https://shieldcn.dev/badge/container-ghcr.io%2Fdepado%2Fbuoy-2496ED.svg?logo=docker&variant=branded" alt="container image" /></a>
</p>

> [!WARNING]
> **Work in progress.** buoy is experimental, under active development and testing.
> APIs, labels, and behavior may change without notice. Use at your own risk.

- [Features](#features)
- [How It Works](#how-it-works)
- [Quick Start](#quick-start)
- [Label Reference](#label-reference)
- [Examples](#examples)
- [Configuration](#configuration)
- [Repository Layout](#repository-layout)
- [Deployment](#deployment)
- [Restoring](#restoring)
- [Backends](#backends)
- [Development](#development)

## Features

- **Label-driven** - Configure every aspect via Docker labels. No config files, no CLI per container.
- **Compose-aware** - Detects compose stacks, respects `depends_on` ordering for stop/start sequences, and batches containers sharing the same schedule into a single coordinated backup cycle.
- **Multi-repo** - Back up to multiple restic repositories at once. Store copies locally, on S3, SFTP, Backblaze B2, or any [rclone](https://rclone.org) backend - ready for 3-2-1.
- **Repo registry** - Maintains a persistent registry of all known restic repositories, so you can list, check, and run retention on repos even when their containers are down.
- **Hooks** - Run shell commands on the host or inside the container before and after each backup.
- **Stop-first** - Optionally stop containers before backup for data consistency, then restart them automatically. One label to opt in.
- **Notifications** - Success and failure alerts via [shoutrrr](https://github.com/nicholas-fedor/shoutrrr): Slack, Discord, Telegram, Pushover, email, Gotify, and more.
- **Retention** - Automatic `restic forget` and `restic prune` with per-container policies (`keep-daily`, `keep-weekly`, `keep-monthly`, `keep-yearly`, `keep-within`).
- **Real-time discovery** - Watches Docker events. New containers are picked up immediately; removed containers are cleaned up.
- **Selective backup** - Include or exclude volumes and mounts by name or path. Use restic file patterns to back up only what matters.
- **Stack lifecycle** - When a container opts into `stop-before-backup`, buoy cascades the stop to its dependents, backs up, then restarts everything in dependency order - waiting for each to be healthy before starting the next.

## How It Works

```
┌───────────────────────────────────────────────────────────┐
│                       buoy daemon                         │
│                                                           │
│  1. Discover containers with buoy.enabled=true            │
│  2. Parse labels → backup schedule, repo, retention       │
│  3. Register cron job for each container                  │
│                                                           │
│  When a schedule fires:                                   │
│  ┌───────────────────────────────────────────────────┐    │
│  │  pre-hooks → stop (ordered, cascade) →            │    │
│  │  restic backup → start (ordered + health wait) →  │    │
│  │  post-hooks → forget → prune                      │    │
│  └───────────────────────────────────────────────────┘    │
│                                                           │
│  Reacts to Docker events in real-time:                    │
│  - Container start → schedule it                          │
│  - Container stop  → remove from schedule                 │
└───────────────────────────────────────────────────────────┘
```

buoy uses restic's `--json` scripting API for structured output and per-container repositories for isolation. Snapshots use clean relative paths for portable restores across hosts and storage backends.

## Quick Start

### 1. Deploy buoy

```yaml
# compose.yaml
services:
  buoy:
    image: ghcr.io/depado/buoy:latest
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - /var/lib/docker/volumes:/var/lib/docker/volumes:ro
      - /srv/data:/srv/data:ro # bind mounts you want backed up
      - buoy_data:/data # state persistence
    environment:
      - BUOY_RESTIC_PASSWORD=your-secure-password
      - BUOY_RESTIC_REPOS=/backup
      - BUOY_DAEMON_CONCURRENCY=2
    restart: unless-stopped

volumes:
  buoy_data:
```

### 2. Label a container for backup

```yaml
services:
  myapp:
    image: myapp:latest
    volumes:
      - app_data:/data
    labels:
      buoy.enabled: "true"
      buoy.schedule: "0 3 * * *"

volumes:
  app_data:
```

That's it. buoy discovers the container, initializes a restic repo at `/backup/<project>/<service>` at backup time, and backs it up daily at 3 AM.

See [Examples](#examples) for more advanced setups with hooks, file patterns, and compose stacks.

### Compose stacks

Containers in a compose stack follow the same scheduling rules as standalone containers. When they share the same schedule, buoy batches them into one coordinated stop/start cycle that backs up each service in the stack. Different schedules run independently.

## Label Reference

| Label                     | Default                    | Description                                                                                                                                                                  |
| ------------------------- | -------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `buoy.enabled`            | -                          | Set to `"true"` to enable backup (required)                                                                                                                                  |
| `buoy.schedule`           | Global `default_schedule`  | Cron expression. Falls back to global default. Containers sharing the same schedule in a compose stack are batched together.                                                 |
| `buoy.repos`              | Global `repos`             | Comma-separated repo URLs, overrides the global list                                                                                                                         |
| `buoy.retention`          | Global `default_retention` | Retention rules (see below). Falls back to global default.                                                                                                                   |
| `buoy.stop-before-backup` | `"false"`                  | Stop the container before backing up. Defaults to `false` - opt-in to container stops.                                                                                       |
| `buoy.stop-timeout`       | `"30s"`                    | Timeout for container stop                                                                                                                                                   |
| `buoy.include-volumes`    | -                          | Comma-separated volume names to back up (overrides exclude)                                                                                                                  |
| `buoy.include-mounts`     | -                          | Comma-separated source or destination paths to back up (overrides exclude)                                                                                                   |
| `buoy.exclude-volumes`    | -                          | Comma-separated volume names to skip                                                                                                                                         |
| `buoy.exclude-mounts`     | -                          | Comma-separated source or destination paths to skip                                                                                                                          |
| `buoy.exclude-patterns`   | -                          | Comma-separated restic exclude patterns (e.g., `"*.log,*.tmp"`)                                                                                                              |
| `buoy.files`              | -                          | Comma-separated file patterns to back up (uses `--files-from`). When set, only matching files are backed up, not the whole mount. Supports globs (`*.sql`) and `!` negation. |
| `buoy.tags`               | -                          | Comma-separated restic snapshot tags                                                                                                                                         |
| `buoy.pre-backup-cmd`     | -                          | Shell command to run on the host before backup                                                                                                                               |
| `buoy.post-backup-cmd`    | -                          | Shell command to run on the host after backup                                                                                                                                |
| `buoy.pre-backup-exec`    | -                          | Command to run inside the container before backup (docker exec)                                                                                                              |
| `buoy.post-backup-exec`   | -                          | Command to run inside the container after backup (docker exec)                                                                                                               |

### Schedule Format

Standard 5-field cron: `"minute hour day-of-month month day-of-week"`

Shorthands:

- `@yearly` / `@annually` - midnight, January 1st
- `@monthly` - midnight, first day of the month
- `@weekly` - midnight between Saturday and Sunday
- `@daily` / `@midnight` - midnight every day
- `@hourly` - start of every hour
- `@every 1h30m` - fixed interval (any duration accepted by Go's `time.ParseDuration`)

See the [`robfig/cron` v3 docs](https://pkg.go.dev/github.com/robfig/cron/v3) for full syntax.

### Retention Format

Comma-separated `key:value` pairs. Supported keys:

| Key            | Restic Flag              | Example           |
| -------------- | ------------------------ | ----------------- |
| `keep-daily`   | `--keep-daily N`         | `keep-daily:7`    |
| `keep-weekly`  | `--keep-weekly N`        | `keep-weekly:4`   |
| `keep-monthly` | `--keep-monthly N`       | `keep-monthly:6`  |
| `keep-yearly`  | `--keep-yearly N`        | `keep-yearly:1`   |
| `keep-within`  | `--keep-within DURATION` | `keep-within:30d` |

All keys are optional. Omitted keys are not passed to restic.

### Compose Stack Awareness

buoy reads `com.docker.compose.project`, `com.docker.compose.service`, and `com.docker.compose.depends_on` labels that Docker Compose sets automatically.

Scheduling works the same as standalone containers - a container is backed up if it has a schedule, from either `buoy.schedule` or the global `default_schedule`. When multiple containers in the same stack share the same schedule, buoy batches them into one coordinated stop/start cycle. Jobs arriving while a stack backup is running wait in a per-stack queue and run immediately after.

**Stop set:** buoy stops containers with `buoy.stop-before-backup=true` plus any container that transitively depends on a stopped container. If the database stops, the API also stops rather than crashing on a lost connection.

**Start order:** buoy restarts containers in dependency order (database before API) and waits for health checks before starting dependents - same behavior as `docker compose up`.

See [Examples](#examples) for a full compose stack setup.

**Repo paths** follow `<base>/<project>/<service>`:

```
/backup/myapp/db
/backup/myapp/api
/backup/myapp/cache
```

## Examples

### PostgreSQL with dump hooks

Run `pg_dumpall` before backup to create a consistent SQL dump, then back up only
the dump file. Clean up after.

```yaml
services:
  postgres:
    image: postgres:16
    volumes:
      - postgres_data:/var/lib/postgresql/data
    labels:
      buoy.enabled: "true"
      buoy.schedule: "0 3 * * *"
      buoy.retention: "keep-daily:7,keep-weekly:4,keep-monthly:6"
      buoy.tags: "database,production"
      buoy.files: "dump.sql"
      buoy.pre-backup-exec: "pg_dumpall -U postgres -f /var/lib/postgresql/data/dump.sql"
      buoy.post-backup-exec: "rm /var/lib/postgresql/data/dump.sql"

volumes:
  postgres_data:
```

### Compose stack with dependencies

Three services: DB (stop before backup), Cache, and API (depends on both).
All share the same schedule, so buoy batches them into one coordinated cycle.

```yaml
services:
  db:
    image: postgres:16
    volumes:
      - db_data:/var/lib/postgresql/data
    labels:
      buoy.enabled: "true"
      buoy.schedule: "0 3 * * *"
      buoy.stop-before-backup: "true"

  cache:
    image: redis:7
    volumes:
      - cache_data:/data
    labels:
      buoy.enabled: "true"
      buoy.schedule: "0 3 * * *"

  api:
    image: myapi:latest
    depends_on:
      db:
        condition: service_healthy
      cache:
        condition: service_started
    volumes:
      - api_data:/app/data
    labels:
      buoy.enabled: "true"
      buoy.schedule: "0 3 * * *"

volumes:
  db_data:
  cache_data:
  api_data:
```

At 3 AM: all three fire. DB has `stop-before-backup=true`, API depends on DB
→ {DB, API} stop transitively. Cache stays running. Each service backed up.
Restart DB, wait healthy, restart API.

## Configuration

buoy is configured via a YAML file, environment variables (prefix `BUOY_`), or CLI flags.

### Config file (`conf.yaml`)

The values shown below are the defaults - you only need a config file to override them.

```yaml
log:
  level: info # debug, info, warn, error
  format: json # json, text
  source: false # include source file/line in logs
  color: auto # auto, always, never

daemon:
  concurrency: 2 # max simultaneous backups (each container or stack batch uses one slot)
  default_schedule: "" # global fallback for buoy.schedule
  default_retention: "keep-within:7d,keep-daily:7,keep-weekly:4,keep-monthly:6,keep-yearly:3"

  resync_interval: "5m" # interval for label resync (0 to disable)
  exec_timeout: "5m" # max time for docker exec hooks
  health_wait_timeout: "5m" # max time to wait for container health
  backup_timeout: "1h" # max time for a complete backup cycle
  check_schedule: "@weekly" # cron for periodic restic check (empty = disabled)
  db_path: "./buoy.db" # path to the bbolt state database

docker:
  host: unix:///var/run/docker.sock

restic:
  binary_path: restic
  password: "${RESTIC_PASSWORD}"
  compression: auto
  repos:
    - /backup

api:
  enabled: true # enable the HTTP API server (required for CLI commands like `buoy repo`)
  host: "0.0.0.0" # API listen host
  port: 8080 # API listen port
  token: "" # bearer token (empty = no auth)

notify:
  urls: # shoutrrr notification URLs
    - slack://tokenA/tokenB/tokenC
    - discord://token@channel
  level: error # none, error, all
```

> [!NOTE]
> **Concurrency is I/O-bound.** Each backup spawns a restic process that reads from disk and writes to storage. Setting `concurrency` higher than your I/O capacity can degrade performance across all running backups. Start low (1–2) and increase only if your storage backend and disk I/O can handle it.

### Environment variables

All config keys can be set as `BUOY_<SECTION>_<KEY>`:

```bash
BUOY_LOG_LEVEL=debug
BUOY_LOG_FORMAT=json
BUOY_LOG_SOURCE=false
BUOY_LOG_COLOR=auto
BUOY_DAEMON_CONCURRENCY=2
BUOY_DAEMON_DEFAULT_SCHEDULE="0 3 * * *"
BUOY_DAEMON_DEFAULT_RETENTION="keep-within:7d,keep-daily:7,keep-weekly:4,keep-monthly:6,keep-yearly:3"
BUOY_DAEMON_RESYNC_INTERVAL=5m
BUOY_DAEMON_EXEC_TIMEOUT=5m
BUOY_DAEMON_HEALTH_WAIT_TIMEOUT=5m
BUOY_DAEMON_BACKUP_TIMEOUT=1h
BUOY_DAEMON_CHECK_SCHEDULE=@weekly
BUOY_DAEMON_DB_PATH=./buoy.db
BUOY_DOCKER_HOST=unix:///var/run/docker.sock
BUOY_RESTIC_BINARY_PATH=restic
BUOY_RESTIC_PASSWORD=my-password
BUOY_RESTIC_COMPRESSION=auto
BUOY_RESTIC_REPOS=/backup,s3:s3.amazonaws.com/my-bucket
BUOY_API_ENABLED=true
BUOY_API_HOST=0.0.0.0
BUOY_API_PORT=8080
BUOY_API_TOKEN=my-secret-token
BUOY_NOTIFY_URLS=slack://tokenA/tokenB/tokenC
BUOY_NOTIFY_LEVEL=error
```

### CLI flags

```bash
buoy run --daemon.concurrency 2 --daemon.resync_interval 5m --daemon.exec_timeout 5m --daemon.health_wait_timeout 5m --daemon.backup_timeout 1h --daemon.check_schedule @weekly --restic.password my-password --restic.compression auto --restic.repos /backup --restic.repos s3:s3.amazonaws.com/bucket --api.enabled true --api.host 0.0.0.0 --api.port 8080 --api.token my-secret-token --log.level debug --notify.urls slack://tokenA/tokenB/tokenC --notify.level error
```

### Password precedence

buoy requires a password to start. Set it via config, env var, or CLI flag.

1. `restic.password` in config file
2. `BUOY_RESTIC_PASSWORD` environment variable
3. `--restic.password` CLI flag

The password is global - all per-container repos use the same one. Buoy passes
it to restic via a temporary `--password-file` rather than the `RESTIC_PASSWORD`
environment variable.

### Notifications

buoy can send failure notifications via [shoutrrr](https://github.com/nicholas-fedor/shoutrrr),
supporting 50+ services including Slack, Discord, Telegram, email, and Gotify.
Configure one or more shoutrrr URLs and set the notification level:

- `error` - notify on backup failures only (default)
- `all` - notify on all backup events
- `none` - disable notifications (or omit config)

Each URL encodes both the service and its credentials. Examples:

| Service      | URL format                                                                     |
| ------------ | ------------------------------------------------------------------------------ |
| Slack        | `slack://hook:tokenA-tokenB-tokenC@webhook`                                    |
| Discord      | `discord://token@channel`                                                      |
| Telegram     | `telegram://token@telegram?chats=@channel`                                     |
| Gotify       | `gotify://host:port/token`                                                     |
| Email (SMTP) | `smtp://user:pass@host:port/?from=sender@example.com&to=recipient@example.com` |

See [shoutrrr's documentation](https://containrrr.dev/shoutrrr/latest/services/overview/)
for the full list of services and URL formats.

Notifications are best-effort - a notification failure logs a warning but
never blocks or fails a backup.

### Periodic Repository Check

buoy can periodically verify the structural integrity of all restic repositories
with `restic check`. This is a lightweight verification that reads the repository
index and ensures all pack files are referenced correctly.

Configure via `daemon.check_schedule` (default: `@weekly`). Set to `""` to
disable.

```yaml
daemon:
  check_schedule: "@weekly"
```

When the check runs, buoy reads known repositories from its persistent state
database (`buoy.db`). Failures are logged and optionally trigger notifications.
This is a structural check only - it does not read pack file data (use the CLI
or API for `restic check --read-data` if needed).

### State Persistence

buoy maintains a [bbolt](https://github.com/etcd-io/bbolt) database at the path
configured by `daemon.db_path` (default `./buoy.db`). This database tracks
every repository buoy has ever managed: when it was created, when it was last
backed up, and whether the associated container still exists.

This enables:

- **Orphaned repo detection**: repos belonging to removed containers are tracked
  rather than forgotten, so you can still run retention, integrity checks, or
  manually clean them up
- **Cross-restart awareness**: the repository list survives daemon restarts

The database is a single file. Mount a volume or bind mount at the directory
containing it to persist state across container recreates.

### HTTP API

buoy exposes a read/write HTTP API on `api.host:api.port` (default
`0.0.0.0:8080`) for querying and operating on repositories. Authentication is
via a Bearer token (`api.token`); when the token is empty, no authentication is
required.

| Method | Path                   | Description                                                       |
| ------ | ---------------------- | ----------------------------------------------------------------- |
| `GET`  | `/api/v1/health`       | Health check (no auth)                                            |
| `GET`  | `/api/v1/repos`        | List all known repos. `?orphaned=true` to show only orphaned      |
| `POST` | `/api/v1/repos/check`  | Run `restic check` on all repos. `?read-data=true` for full check |
| `POST` | `/api/v1/repos/stats`  | Aggregate `restic stats` across all repos                         |
| `POST` | `/api/v1/repos/unlock` | Unlock all repos                                                  |
| `POST` | `/api/v1/repos/forget` | Run `restic forget` with `?retention=keep-daily:7,...`            |
| `POST` | `/api/v1/repos/prune`  | Run `restic prune` on all repos                                   |

Connect to the API from a local or remote buoy CLI, or use it as the backend
for a dashboard/aggregator.

### CLI: `buoy repo`

buoy provides a `repo` subcommand for querying and operating on managed
repositories. The CLI communicates with a running buoy daemon via its HTTP API.

Set `--api.url` and `--api.token` per command, or use the `BUOY_URL` /
`BUOY_TOKEN` environment variables. Defaults to `http://127.0.0.1:8080`.

```bash
buoy repo list --all                 # list all non-orphaned repos
buoy repo list --orphaned            # show only orphaned repos
buoy repo list --repo /backup/myapp  # show a specific repo
buoy repo check --all                # structural integrity check
buoy repo check --read-data --all    # full data integrity check
buoy repo stats --all                # storage usage across all repos
buoy repo unlock --repo /backup/myapp
buoy repo forget --retention keep-daily:7,keep-weekly:4 --all
buoy repo prune --orphaned

# Remote daemon with auth
export BUOY_URL=https://buoy.internal.example.com
export BUOY_TOKEN=secret123
buoy repo stats --all
```

> [!WARNING]
> Destructive operations (`unlock`, `forget`, `prune`) return an error if any backup is currently in progress, preventing accidental lock conflicts or corruption. Read-only commands (`check`, `stats`) are not gated and may run alongside backups.

## Repository Layout

Each container gets a separate restic repository under each configured base repo. For a container in compose project `myapp` service `db` with repos `[/backup, s3:my-bucket]`, buoy creates:

- `/backup/myapp/db`
- `s3:my-bucket/myapp/db`

For standalone containers, the URL is `<repo>/<container_name>`.

Repos are initialized automatically at backup time.

Snapshots use clean relative paths: buoy changes into each mount's source directory and backs up individual entries, so snapshots contain `file.db`, `logs/` instead of `/var/lib/docker/volumes/<name>/_data/file.db`. Mounts are tagged (`mount:<name>`) for correct parent snapshot selection.

## Deployment

### As a Docker container (recommended)

```yaml
services:
  buoy:
    image: ghcr.io/depado/buoy:latest
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - /var/lib/docker/volumes:/var/lib/docker/volumes:ro
      - /srv/app-data:/srv/app-data:ro # each bind mount explicitly
      - buoy_data:/data # state persistence
    environment:
      - BUOY_RESTIC_PASSWORD=${RESTIC_PASSWORD:?required}
      - BUOY_RESTIC_REPOS=/backup
      # - BUOY_NOTIFY_URLS=slack://tokenA/tokenB/tokenC
      # - BUOY_NOTIFY_LEVEL=error
    labels:

    restart: unless-stopped

volumes:
  buoy_data:
```

Each path buoy needs to read must be mounted explicitly:

- **Named/anonymous volumes:** Mount `/var/lib/docker/volumes:/var/lib/docker/volumes:ro` (or your Docker data root). Covers all Docker-managed volumes.
- **Bind mounts:** Mount each host source path at the same location inside buoy. For example, if a container bind-mounts `/srv/app-data:/app/data`, mount `/srv/app-data:/srv/app-data:ro` in buoy's compose file.

If a mount source doesn't exist inside buoy, it's skipped with a warning.

### Restart policy caveat

Containers with `buoy.stop-before-backup=true` **must not** have `restart: always` (or similar). If they do, Docker restarts them immediately after buoy stops them, causing a race with the backup. Use `restart: "no"` or omit the restart policy on containers that buoy stops.

### Signal handling

On Unix systems, pressing Ctrl+C sends SIGINT to the entire foreground process group, which includes restic child processes. buoy prevents this by placing restic in its own process group (`Setpgid`). This means backups complete cleanly even when buoy receives a shutdown signal.

When building from source on Windows, this protection is not applied. If you encounter partial backups during shutdown on Windows, consider running buoy inside the Linux container image instead.

## Restoring

buoy does not have a built-in restore command. Use restic directly:

```bash
# List snapshots
restic -r /backup/myapp/postgres snapshots

# Restore a specific snapshot
restic -r /backup/myapp/postgres restore <snapshot-id> --target /tmp/restore

# Restore the latest snapshot
restic -r /backup/myapp/postgres restore latest --target /tmp/restore
```

Since buoy uses clean relative paths, restored files go directly into the target directory without the full host path prefix.

For cloud backends, include the relevant credentials:

```bash
export RESTIC_PASSWORD=your-password
export AWS_ACCESS_KEY_ID=...
export AWS_SECRET_ACCESS_KEY=...
restic -r s3:s3.amazonaws.com/my-bucket/myapp/postgres restore latest --target /tmp/restore
```

## Backends

buoy supports all [restic backends](https://restic.readthedocs.io/en/stable/030_preparing_a_new_repo.html):

- Local filesystem: `/backup`
- Amazon S3: `s3:s3.amazonaws.com/bucket`
- Backblaze B2: `b2:bucketname:/path`
- Azure Blob: `azure:container:/path`
- Google Cloud: `gs:bucket:/path`
- SFTP: `sftp:user@host:/path`
- REST server: `rest:https://host:8000/`
- rclone: `rclone:remote:path`

Configure one or more repos for 3-2-1 backup strategy. Each container backs up to all repos simultaneously. Cloud credentials are passed via standard restic environment variables.

```yaml
restic:
  repos:
    - /backup # local copy
    - s3:s3.amazonaws.com/my-bucket # offsite copy
    - b2:my-bucket:/buoy # different media
```

## Development

```bash
go mod tidy
make build
./buoy run
```

Run with debug logging:

```bash
./buoy run --log.level debug --log.format text --log.color always
```
