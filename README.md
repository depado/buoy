# buoy

> [!WARNING]
> **Work in progress.** buoy is experimental and under active development.
> APIs, labels, and behavior may change without notice. Not yet recommended
> for production use.

Backup Docker container volumes and bind mounts with [restic](https://restic.net/).

buoy is a daemon that discovers Docker containers via labels, stops them, backs up their mounted volumes using restic, restarts them, and applies retention policies — all on cron schedules.

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
      - /srv/data:/srv/data:ro  # bind mounts you want backed up
    environment:
      - BUOY_RESTIC_PASSWORD=your-secure-password
      - BUOY_RESTIC_BASE_REPO=/backup
      - BUOY_DAEMON_CONCURRENCY=2
    labels:

    restart: unless-stopped
```

### 2. Label a container for backup

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

That's it. buoy discovers the container, initializes a restic repo at `/backup/<project>/<service>` at backup time, and backs it up daily at 3 AM.

### Compose stacks

Every container in a compose stack must have its own `buoy.schedule`. When schedules fire close together, buoy batches them: one coordinated stop/start cycle backs up each service in the stack. When schedules are far apart, each runs independently, backing up only its own service.

## Label Reference

| Label | Default | Description |
|-------|---------|-------------|
| `buoy.enabled` | — | Set to `"true"` to enable backup (required) |
| `buoy.schedule` | Global `default_schedule` | Cron expression. Every container in a compose stack needs one. Falls back to global default. |
| `buoy.repo` | Global `base_repo` | Override the restic repository URL for this container |
| `buoy.retention` | Global `default_retention` | Retention rules. Falls back to global default. Final fallback: `keep-daily:7`. |
| `buoy.stop-before-backup` | `"false"` | Stop the container before backing up. Defaults to `false` — opt-in to container stops. |
| `buoy.stop-timeout` | `"30s"` | Timeout for container stop |
| `buoy.include-volumes` | — | Comma-separated volume names to back up (overrides exclude) |
| `buoy.include-mounts` | — | Comma-separated source or destination paths to back up (overrides exclude) |
| `buoy.exclude-volumes` | — | Comma-separated volume names to skip |
| `buoy.exclude-mounts` | — | Comma-separated source or destination paths to skip |
| `buoy.exclude-patterns` | — | Comma-separated restic exclude patterns (e.g., `"*.log,*.tmp"`) |
| `buoy.files` | — | Comma-separated file patterns to back up (uses `--files-from`). When set, only matching files are backed up, not the whole mount. Supports globs (`*.sql`) and `!` negation. |
| `buoy.tags` | — | Comma-separated restic snapshot tags |
| `buoy.pre-backup-cmd` | — | Shell command to run on the host before backup |
| `buoy.post-backup-cmd` | — | Shell command to run on the host after backup |
| `buoy.pre-backup-exec` | — | Command to run inside the container before backup (docker exec) |
| `buoy.post-backup-exec` | — | Command to run inside the container after backup (docker exec) |

### Schedule Format

Standard 5-field cron: `"minute hour day-of-month month day-of-week"`

Shorthands: `@yearly`, `@monthly`, `@weekly`, `@daily`, `@hourly`, `@every 1h30m`

### Retention Format

Comma-separated `key:value` pairs. Supported keys:

| Key | Restic Flag | Example |
|-----|------------|---------|
| `keep-daily` | `--keep-daily N` | `keep-daily:7` |
| `keep-weekly` | `--keep-weekly N` | `keep-weekly:4` |
| `keep-monthly` | `--keep-monthly N` | `keep-monthly:6` |
| `keep-yearly` | `--keep-yearly N` | `keep-yearly:1` |
| `keep-within` | `--keep-within DURATION` | `keep-within:30d` |

All keys are optional. Omitted keys are not passed to restic.

### Compose Stack Awareness

buoy reads `com.docker.compose.project`, `com.docker.compose.service`, and `com.docker.compose.depends_on` labels that Docker Compose sets automatically.

**Every container needs its own schedule.** When multiple containers in the same stack have close or identical schedules, buoy batches them: jobs arriving while a stack backup is running wait in a per-stack queue and run immediately after the current batch finishes. One coordinated stop/start cycle per batch, backing up one container per service.

**Stop set:** buoy stops containers with `buoy.stop-before-backup=true` plus any container that transitively depends on a stopped container. Only containers being backed up in the current batch contribute to the stop decision. This ensures clean shutdowns: if the database stops, the API also stops rather than crashing on a lost connection.

**Start order:** buoy restarts containers in dependency order (database before API) and waits for health checks before starting dependents — same behavior as `docker compose up`.

**Example:** A stack with DB (depends on nothing), Cache (depends on nothing), and API (depends on DB + Cache).

```yaml
db:
  labels:
    buoy.enabled: "true"
    buoy.schedule: "0 3 * * *"
    buoy.stop-before-backup: "true"

api:
  labels:
    buoy.enabled: "true"
    buoy.schedule: "0 3 * * *"     # same schedule → batched
    buoy.stop-before-backup: "false"

cache:
  labels:
    buoy.enabled: "true"
    buoy.schedule: "0 3 * * *"     # same schedule → batched
    buoy.stop-before-backup: "false"
```

At 3 AM: DB, Cache, and API all fire. They batch together. DB has `stop=true` → API depends on DB → {DB, API} stop. Cache stays running. Back up each service. Restart DB, wait healthy, restart API.

**Repo paths** follow `<base>/<project>/<service>`:

```
/backup/myapp/db
/backup/myapp/api
/backup/myapp/cache
```

## Configuration

buoy is configured via a YAML file, environment variables (prefix `BUOY_`), or CLI flags.

### Config file (`conf.yaml`)

```yaml
log:
  level: info
  format: json

daemon:
  concurrency: 2
  default_schedule: ""              # global fallback for buoy.schedule
  default_retention: "keep-daily:7" # global fallback for buoy.retention

docker:
  host: unix:///var/run/docker.sock

restic:
  binary_path: restic
  password: "${RESTIC_PASSWORD}"
  base_repo: /backup
```

### Environment variables

All config keys can be set as `BUOY_<SECTION>_<KEY>`:

```bash
BUOY_RESTIC_PASSWORD=my-password
BUOY_RESTIC_BASE_REPO=s3:s3.amazonaws.com/my-bucket
BUOY_DAEMON_CONCURRENCY=2
BUOY_LOG_LEVEL=debug
```

### CLI flags

```bash
buoy run --daemon.concurrency 2 --restic.password my-password --restic.base_repo /backup --log.level debug
```

### Password precedence

1. `restic.password` in config file
2. `RESTIC_PASSWORD` environment variable (set externally)
3. `RESTIC_PASSWORD_FILE` environment variable
4. `RESTIC_PASSWORD_COMMAND` environment variable (buoy executes this)

The password is global — all per-container repos use the same one.

## Repository Layout

Each container gets a separate restic repository. The URL is `<base_repo>/<compose_project>/<compose_service>` for compose stacks, or `<base_repo>/<container_name>` for standalone containers.

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
      - /srv/app-data:/srv/app-data:ro  # each bind mount explicitly
    environment:
      - BUOY_RESTIC_PASSWORD=${RESTIC_PASSWORD:?required}
      - BUOY_RESTIC_BASE_REPO=/backup
    labels:

    restart: unless-stopped
```

Each path buoy needs to read must be mounted explicitly:

- **Named/anonymous volumes:** Mount `/var/lib/docker/volumes:/var/lib/docker/volumes:ro` (or your Docker data root). Covers all Docker-managed volumes.
- **Bind mounts:** Mount each host source path at the same location inside buoy. For example, if a container bind-mounts `/srv/app-data:/app/data`, mount `/srv/app-data:/srv/app-data:ro` in buoy's compose file.

If a mount source doesn't exist inside buoy, it's skipped with a warning.

### Restart policy caveat

Containers with `buoy.stop-before-backup=true` **must not** have `restart: always` (or similar). If they do, Docker restarts them immediately after buoy stops them, causing a race with the backup. Use `restart: "no"` or omit the restart policy on containers that buoy stops.

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

buoy supports all restic backends:

- Local filesystem: `/backup`
- Amazon S3: `s3:s3.amazonaws.com/bucket`
- Backblaze B2: `b2:bucketname:/path`
- Azure Blob: `azure:container:/path`
- Google Cloud: `gs:bucket:/path`
- SFTP: `sftp:user@host:/path`
- REST server: `rest:https://host:8000/`
- rclone: `rclone:remote:path`

Set `BUOY_RESTIC_BASE_REPO` to your backend URL. Cloud credentials are passed via standard restic environment variables.

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
