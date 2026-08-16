<p align="center">
  <img alt="buoy" src="https://shieldcn.dev/header/grid.svg?title=buoy&subtitle=Restic-powered+Docker+volume+and+mounts+backups.&logo=lu%3ALifeBuoy&mode=dark&align=left&border=false">
</p>

<p align="center">
  A label-driven, compose-aware backup daemon with hooks, notifications, and automatic retention - powered by <a href="https://restic.net">restic</a>, on your schedule.
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
  - [Schedule Format](#schedule-format)
  - [Retention Format](#retention-format)
  - [Include/Exclude Syntax](#includeexclude-syntax)
  - [Compose Stack Awareness](#compose-stack-awareness)
- [Examples](#examples)
- [Configuration](#configuration)
- [CLI](#cli)
- [Repository Layout](#repository-layout)
- [Development](#development)
- [Docs](#docs)

## Features

- **Label-driven** - Configure every aspect via Docker labels. No config files, no CLI per container.
- **Compose-aware** - Detects compose stacks, respects `depends_on` ordering for stop/start sequences, and batches containers sharing the same schedule into a single coordinated backup cycle.
- **Multi-repo** - Back up to multiple restic repositories at once. Store copies locally, on S3, SFTP, Backblaze B2, or any [rclone](https://rclone.org) backend - ready for 3-2-1.
- **Repo registry** - Maintains a persistent registry of all known restic repositories, so you can list, check, and run retention on repos even when their containers are down.
- **Hooks** - Run shell commands on the host or inside the container before and after each backup.
- **Stop-first** - Optionally stop containers before backup for data consistency, then restart them automatically. One label to opt in.
- **Notifications** - Success and failure alerts via [shoutrrr](https://github.com/nicholas-fedor/shoutrrr): Slack, Discord, Telegram, Pushover, email, Gotify, and more.
- **Retention** - Automatic `restic forget` after every backup with per-container policies (`keep-daily`, `keep-weekly`, `keep-monthly`, `keep-yearly`, `keep-within`), and scheduled `restic prune` (`daemon.prune_schedule`, default `@weekly`) so prune never slows down backups.
- **Real-time discovery** - Watches Docker events. New containers are picked up immediately; removed containers are cleaned up.
- **Selective backup** - Include or exclude volumes and mounts by name or path. Use restic file patterns to back up only what matters.
- **Stack lifecycle** - When a container opts into `buoy.stop-before`, buoy cascades the stop to its dependents, backs up, then restarts everything in dependency order - waiting for each to be healthy before starting the next.

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
│  │  post-hooks → forget                              │    │
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
      - TZ=Europe/Paris
      - BUOY_RESTIC_REPO_LOCAL_URL=/backup
      - BUOY_RESTIC_REPO_LOCAL_PASSWORD=your-secure-password
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

> [!TIP]
> For compose stacks, buoy reads `depends_on` ordering and batches containers sharing the same schedule into one coordinated stop/start cycle. See [Compose Stack Awareness](#compose-stack-awareness).

## Label Reference

| Label                  | Default                    | Description                                                                                                                                                                                    |
| ---------------------- | -------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `buoy.enabled`         | -                          | Set to `"true"` to enable backup (required)                                                                                                                                                    |
| `buoy.schedule`        | Global `default_schedule`  | Cron expression. Falls back to global default. Containers sharing the same schedule in a compose stack are batched together.                                                                   |
| `buoy.repos`           | Global `repos`             | Comma-separated repo **names** to back up to. Overrides the global list.                                                                                                                       |
| `buoy.password`        | Global fallback            | Restic repository password for this container. Overrides any per-repo and global password.                                                                                                     |
| `buoy.retention`       | Global `default_retention` | Retention rules (see below). Falls back to global default.                                                                                                                                     |
| `buoy.stop-before`     | `"false"`                  | Stop the container before backing up. Defaults to `false` - opt-in to container stops.                                                                                                         |
| `buoy.stop-timeout`    | `"30s"`                    | Timeout for container stop                                                                                                                                                                     |
| `buoy.repo-timeout`    | Global `repo_timeout`      | Per-container override for the per-repo backup budget. `0` falls back to the daemon config.                                                                                                    |
| `buoy.repo-timeout.<name>` | Per-repo `timeout`      | Per-repo backup budget override for the named repo, e.g. `buoy.repo-timeout.b2=45m`. Overrides `buoy.repo-timeout` and the repo config.                                                         |
| `buoy.health-wait-timeout` | Global `health_wait_timeout` | Per-container override for health/dependency wait and restart timeout. `0` falls back to the daemon config.                                                                                 |
| `buoy.include`         | -                          | Comma-separated mount identifiers (volume names or paths). Optional `name=value` syntax for per-mount backup overrides. Volume names are automatically used as the per-mount key when matched. |
| `buoy.exclude`         | -                          | Comma-separated mount identifiers (volume names or paths) to skip                                                                                                                              |
| `buoy.backup.files`    | -                          | Comma-separated file patterns to back up (uses `--files-from`). When set, only matching files are backed up, not the whole mount. Supports globs (`*.sql`) and `!` negation.                   |
| `buoy.backup.exclude`  | -                          | Comma-separated restic exclude patterns (e.g., `"*.log,*.tmp"`)                                                                                                                                |
| `buoy.backup.tags`     | -                          | Comma-separated restic snapshot tags                                                                                                                                                           |
| `buoy.backup.<name>.*` | -                          | Per-mount overrides for named include entries. `<name>.files`, `<name>.exclude` replace globals for that mount; `<name>.tags` are appended.                                                    |
| `buoy.hook.pre.cmd`    | -                          | Shell command to run on the host before backup                                                                                                                                                 |
| `buoy.hook.post.cmd`   | -                          | Shell command to run on the host after backup                                                                                                                                                  |
| `buoy.hook.pre.exec`   | -                          | Command to run inside the container before backup (docker exec)                                                                                                                                |
| `buoy.hook.post.exec`  | -                          | Command to run inside the container after backup (docker exec)                                                                                                                                 |

> [!IMPORTANT]
> A container is skipped if it has no schedule (neither `buoy.schedule` nor `daemon.default_schedule`) or if no repos are resolved (neither `buoy.repos` nor `restic.repos`).

### Schedule Format

Standard 5-field cron (`minute hour day-of-month month day-of-week`) or `@every` interval.

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
| `keep-last`    | `--keep-last N`          | `keep-last:10`    |
| `keep-hourly`  | `--keep-hourly N`        | `keep-hourly:24`  |
| `keep-daily`   | `--keep-daily N`         | `keep-daily:7`    |
| `keep-weekly`  | `--keep-weekly N`        | `keep-weekly:4`   |
| `keep-monthly` | `--keep-monthly N`       | `keep-monthly:6`  |
| `keep-yearly`  | `--keep-yearly N`        | `keep-yearly:1`   |
| `keep-within`  | `--keep-within DURATION` | `keep-within:30d` |

All keys are optional. Omitted keys are not passed to restic.

### Include/Exclude Syntax

`buoy.include` and `buoy.exclude` accept comma-separated mount identifiers.
An identifier can be a volume name, a host source path, or a container destination
path - buoy matches each entry against all three fields of every mount.

#### Basic filtering

```yaml
# Back up only these mounts
buoy.include: "db_data, /srv/uploads"

# Back up everything except these
buoy.exclude: "/tmp, cache_vol"
```

#### Named entries for per-mount backup options

Prefix an include entry with `name=` to assign it a name. Per-mount labels at
`buoy.backup.<name>.<option>` then apply to that mount, overriding the global
defaults. See the [Named includes example](#named-includes-with-per-mount-options).

```yaml
buoy.include: "src=/app/code, data=/app/data, /var/log"

# Per-mount: back up only .go files from /app/code
buoy.backup.src.files: "*.go,*.ts"

# Per-mount: exclude temp files from /app/data
buoy.backup.src.exclude: "*.log,*.tmp"

# Per-mount: append tags to the global set
buoy.backup.src.tags: "source-code"

# /var/log has no name - it uses global backup defaults
```

| Syntax       | Behavior                                                                                                                                                                                |
| ------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `name=value` | Named entry. `name` used as key for `buoy.backup.<name>.*` overrides.                                                                                                                   |
| Bare value   | Unnamed entry. If it matches a volume by its Docker name, that name is automatically used for per-mount overrides. Otherwise (bind mount), no per-mount overrides apply - uses globals. |
| Mixed        | Both named and unnamed entries can appear in the same `buoy.include` value.                                                                                                             |

Per-mount override semantics:

| Option    | Per-mount behavior                    |
| --------- | ------------------------------------- |
| `files`   | Replaces global `buoy.backup.files`   |
| `exclude` | Replaces global `buoy.backup.exclude` |
| `tags`    | Appended to global `buoy.backup.tags` |

### Compose Stack Awareness

buoy reads `com.docker.compose.project`, `com.docker.compose.service`, and `com.docker.compose.depends_on` labels that Docker Compose sets automatically.

Scheduling works the same as standalone containers - a container is backed up if it has a schedule, from either `buoy.schedule` or the global `default_schedule`. When multiple containers in the same stack share the same schedule, buoy batches them into one coordinated stop/start cycle. Jobs arriving while a stack backup is running wait in a per-stack queue and run immediately after.

**Stop set:** buoy stops containers with `buoy.stop-before=true` plus any container that transitively depends on a stopped container. If the database stops, the API also stops rather than crashing on a lost connection.

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

<details>
<summary>Click to expand</summary>

```yaml
services:
  postgres:
    image: postgres:16
    volumes:
      - postgres_data:/var/lib/postgresql/data
    labels:
      buoy.enabled: "true"
      buoy.schedule: "0 3 * * *"
      buoy.backup.files: "dump.sql"
      buoy.hook.pre.exec: "pg_dumpall -U postgres -f /var/lib/postgresql/data/dump.sql"
      buoy.hook.post.exec: "rm /var/lib/postgresql/data/dump.sql"

volumes:
  postgres_data:
```

</details>

### Named includes with per-mount options

A web application with two mount points: source code and user uploads. Different
backup strategies for each mount using named include entries.

<details>
<summary>Click to expand</summary>

```yaml
services:
  webapp:
    image: myapp:latest
    volumes:
      - ./src:/app/src
      - uploads:/app/uploads
    labels:
      buoy.enabled: "true"
      buoy.schedule: "@daily"

      # Named mount entries - volume names auto-derive their per-mount name
      buoy.include: "code=./src, uploads"

      # Global defaults - applied to both mounts
      buoy.backup.tags: "production,webapp"

      # Per-mount: src - back up only source files
      buoy.backup.code.files: "*.go,*.ts,*.js,*.css"
      buoy.backup.code.tags: "source-code"

      # Per-mount: uploads - exclude temp and cache
      buoy.backup.uploads.exclude: "*.tmp,*.cache,*.log,thumbs/"
      buoy.backup.uploads.tags: "user-data"
```

</details>

- `code=./src` creates a named entry `code`. Only `.go`, `.ts`, `.js`, `.css` files
  are backed up from `/app/src`. Tags `production,webapp,source-code` are applied.
- `uploads` is a bare entry matching the volume by its Docker name. Per-mount
  overrides use the volume name automatically - `buoy.backup.uploads.*`.
  Everything under `/app/uploads` is backed up except `*.tmp`, `*.cache`,
  `*.log`, and `thumbs/`. Tags `production,webapp,user-data` are applied.
- Global `buoy.backup.tags` apply to both mounts, with per-mount tags appended.

### Compose stack with dependencies

Three services: DB (stop before backup), Cache, and API (depends on both).
All share the same schedule, so buoy batches them into one coordinated cycle.

<details>
<summary>Click to expand</summary>

```yaml
services:
  db:
    image: postgres:16
    volumes:
      - db_data:/var/lib/postgresql/data
    labels:
      buoy.enabled: "true"
      buoy.schedule: "0 3 * * *"
      buoy.stop-before: "true"

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

</details>

At 3 AM: all three fire. DB has `stop-before=true`, API depends on DB
→ {DB, API} stop transitively. Cache stays running. Each service backed up.
Restart DB, wait healthy, restart API.

## Configuration

buoy is configured via a YAML file (`conf.yaml`), environment variables
(`BUOY_` prefix, dots → underscores), or CLI flags. The table below lists
every available key with its default value.

When a container label refers to a global value (`buoy.schedule` →
`daemon.default_schedule`, `buoy.retention` → `daemon.default_retention`,
`buoy.repos` → `restic.repos`), the default shown here applies.

### Reference

| Key                          | Default                                                                  | Env / CLI                                                          |
| ---------------------------- | ------------------------------------------------------------------------ | ------------------------------------------------------------------ |
| `log.level`                  | `info`                                                                   | `BUOY_LOG_LEVEL` / `--log.level`                                   |
| `log.format`                 | `json`                                                                   | `BUOY_LOG_FORMAT` / `--log.format`                                 |
| `log.source`                 | `false`                                                                  | `BUOY_LOG_SOURCE` / `--log.source`                                 |
| `log.color`                  | `auto`                                                                   | `BUOY_LOG_COLOR` / `--log.color`                                   |
| `daemon.concurrency`         | `2`                                                                      | `BUOY_DAEMON_CONCURRENCY` / `--daemon.concurrency`                 |
| `daemon.default_schedule`    | `""`                                                                     | `BUOY_DAEMON_DEFAULT_SCHEDULE` / `--daemon.default_schedule`       |
| `daemon.default_retention`   | `keep-within:7d,keep-daily:7,keep-weekly:4,keep-monthly:6,keep-yearly:3` | `BUOY_DAEMON_DEFAULT_RETENTION` / `--daemon.default_retention`     |
| `daemon.resync_interval`     | `5m`                                                                     | `BUOY_DAEMON_RESYNC_INTERVAL` / `--daemon.resync_interval`         |
| `daemon.exec_timeout`        | `5m`                                                                     | `BUOY_DAEMON_EXEC_TIMEOUT` / `--daemon.exec_timeout`               |
| `daemon.health_wait_timeout` | `5m`                                                                     | `BUOY_DAEMON_HEALTH_WAIT_TIMEOUT` / `--daemon.health_wait_timeout` |
| `daemon.backup_timeout`      | `1h`                                                                     | `BUOY_DAEMON_BACKUP_TIMEOUT` / `--daemon.backup_timeout`           |
| `daemon.repo_timeout`        | `30m`                                                                    | `BUOY_DAEMON_REPO_TIMEOUT` / `--daemon.repo_timeout`               |
| `daemon.check_schedule`      | `@weekly`                                                                | `BUOY_DAEMON_CHECK_SCHEDULE` / `--daemon.check_schedule`           |
| `daemon.prune_schedule`      | `@weekly`                                                                | `BUOY_DAEMON_PRUNE_SCHEDULE` / `--daemon.prune_schedule`           |
| `daemon.db_path`             | `./buoy.db`                                                              | `BUOY_DAEMON_DB_PATH` / `--daemon.db_path`                         |
| `docker.host`                | `unix:///var/run/docker.sock`                                            | `BUOY_DOCKER_HOST` / `--docker.host`                               |
| `restic.binary_path`         | `restic`                                                                 | `BUOY_RESTIC_BINARY_PATH` / `--restic.binary_path`                 |
| `restic.password`            | _(optional)_                                                             | `BUOY_RESTIC_PASSWORD` / `--restic.password`                       |
| `restic.compression`         | `auto`                                                                   | `BUOY_RESTIC_COMPRESSION` / `--restic.compression`                 |
| `restic.repos`               | _(none)_                                                                 | See [Configuration](docs/configuration.md#password) for env var format. |
| `api.enabled`                | `true`                                                                   | `BUOY_API_ENABLED` / `--api.enabled`                               |
| `api.host`                   | `0.0.0.0`                                                                | `BUOY_API_HOST` / `--api.host`                                     |
| `api.port`                   | `8080`                                                                   | `BUOY_API_PORT` / `--api.port`                                     |
| `api.token`                  | `""`                                                                     | `BUOY_API_TOKEN` / `--api.token`                                   |
| `notify.urls`                | _(none)_                                                                 | `BUOY_NOTIFY_URLS` / `--notify.urls`                               |
| `notify.level`               | `error`                                                                  | `BUOY_NOTIFY_LEVEL` / `--notify.level`                             |

> [!NOTE]
> **Concurrency is I/O-bound.** Each backup spawns a restic process that reads from disk and writes to storage. Setting `concurrency` higher than your I/O capacity can degrade performance across all running backups. Start low (1–2) and increase only if your storage backend and disk I/O can handle it.

For password setup, notifications, and the HTTP API, see **[Configuration](docs/configuration.md)**. For observability, see **[OpenTelemetry](docs/otel.md)**.

## CLI

buoyctl talks to the daemon's HTTP API. Quick reference:

```bash
buoyctl repo list     # list managed repos
buoyctl list          # list scheduled backups
buoyctl backup --all  # trigger immediate backup
buoyctl discover .    # scan compose files for mounts
```

See the full **[CLI Reference](docs/cli.md)** for all subcommands, flags, and examples.

## Repository Layout

Each container gets a separate restic repository under each configured base repo. For a container in compose project `myapp` service `db` with repos `[/backup, s3:s3.amazonaws.com/my-bucket]`, buoy creates:

- `/backup/myapp/db`
- `s3:s3.amazonaws.com/my-bucket/myapp/db`

For standalone containers, the URL is `<repo>/<container_name>`.

Repos are initialized automatically at backup time.

Snapshots use clean relative paths: buoy changes into each mount's source directory and backs up individual entries, so snapshots contain `file.db`, `logs/` instead of `/var/lib/docker/volumes/<name>/_data/file.db`. Mounts are tagged (`mount:<name>`) for correct parent snapshot selection.

## Development

```bash
go mod tidy
make build
./buoy run
```

Run with debug logging:

```bash
./buoy run --log.level debug --log.format text --log.color always

# Buoyctl commands talk to the running daemon
./buoyctl repo list --all
```

## Docs

- **[Configuration](docs/configuration.md)** - password levels, notifications, HTTP API, default config file
- **[OpenTelemetry](docs/otel.md)** - traces, metrics, and logs with OTLP
- **[CLI Reference](docs/cli.md)** - all `buoyctl` subcommands, flags, and examples
- **[Deployment](docs/deployment.md)** - image variants, timezone, restart policies, signal handling
- **[Restoring](docs/restoring.md)** - how to restore backups with restic
- **[Backends](docs/backends.md)** - restic backend URL formats (S3, B2, SFTP, etc.)
