# buoy

> [!WARNING]
> **Work in progress.** buoy is experimental and under active development.
> APIs, labels, and behavior may change without notice. Not yet recommended
> for production use.

Backup Docker container volumes and bind mounts with [restic](https://restic.net/).

buoy is a daemon that discovers Docker containers via labels, stops them, backs up their mounted volumes using restic, restarts them, and applies retention policies — all on cron schedules.

## How It Works

```
┌──────────────────────────────────────────────────────────┐
│                       buoy daemon                         │
│                                                           │
│  1. Discover containers with buoy.enabled=true            │
│  2. Parse labels → backup schedule, repo, retention       │
│  3. Register cron job for each container                  │
│                                                           │
│  When a schedule fires:                                   │
│  ┌───────────────────────────────────────────────────┐    │
│  │  Stop container → pre-hooks → restic backup →     │    │
│  │  post-hooks → start container → forget → prune    │    │
│  └───────────────────────────────────────────────────┘    │
│                                                           │
│  Reacts to Docker events in real-time:                    │
│  - Container start → schedule it                          │
│  - Container stop  → remove from schedule                 │
└──────────────────────────────────────────────────────────┘
```

buoy uses restic's `--json` scripting API for structured output and per-container repositories for isolation. Snapshots use clean relative paths (`./data/file.db`) thanks to directory-relative backups.

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
      buoy.stop_before_backup: "false"
      buoy.files: "dump.sql"
      buoy.pre_backup_exec: "pg_dumpall -U postgres -f /var/lib/postgresql/data/dump.sql"
      buoy.post_backup_exec: "rm /var/lib/postgresql/data/dump.sql"

volumes:
  postgres_data:
```

That's it. buoy discovers the container, initializes a restic repo at `/backup/<project>/<service>`, and backs it up daily at 3 AM.

## Label Reference

| Label | Default | Description |
|-------|---------|-------------|
| `buoy.enabled` | — | Set to `"true"` to enable backup (required) |
| `buoy.schedule` | — | Cron expression (e.g., `"0 3 * * *"`, `"@daily"`, `"@every 6h"`) |
| `buoy.repo` | Global `base_repo` | Override the restic repository URL for this container |
| `buoy.retention` | `"keep-daily:7"` | Retention rules: `"keep-daily:7,keep-weekly:4,keep-monthly:6,keep-yearly:1,keep-within:30d"` |
| `buoy.stop_before_backup` | `"true"` | Stop the container before backing up |
| `buoy.stop_timeout` | `"30s"` | Timeout for container stop |
| `buoy.exclude_volumes` | — | Comma-separated volume names to skip |
| `buoy.exclude_patterns` | — | Comma-separated restic exclude patterns (e.g., `"*.log,*.tmp"`) |
| `buoy.files` | — | Comma-separated file patterns to back up (uses `--files-from`). When set, only matching files are backed up, not the whole mount. Supports globs (`*.sql`) and `!` negation. |
| `buoy.tags` | — | Comma-separated restic snapshot tags |
| `buoy.pre_backup_cmd` | — | Shell command to run on the host before backup |
| `buoy.post_backup_cmd` | — | Shell command to run on the host after backup |
| `buoy.pre_backup_exec` | — | Command to run inside the container before backup (docker exec) |
| `buoy.post_backup_exec` | — | Command to run inside the container after backup (docker exec) |

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

buoy reads the canonical `com.docker.compose.project` and `com.docker.compose.service` labels that Docker Compose adds automatically. Repository paths follow the pattern `<base>/<project>/<service>`:

```
/backup/myapp/postgres     # compose project=myapp, service=postgres
/backup/myapp/redis        # compose project=myapp, service=redis
/backup/webserver           # standalone container
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

```
buoy run --daemon.concurrency 2 --log.level debug
```

### Password precedence

1. `restic.password` in config file
2. `RESTIC_PASSWORD` environment variable (set externally)
3. `RESTIC_PASSWORD_FILE` environment variable
4. `RESTIC_PASSWORD_COMMAND` environment variable (buoy executes this)

The password is global — all per-container repos use the same one.

## Repository Layout

Each container gets a separate restic repository. The URL is `<base_repo>/<compose_project>/<compose_service>` for compose stacks, or `<base_repo>/<container_name>` for standalone containers.

Repos are initialized automatically when a container is first discovered.

Snapshots use clean relative paths. buoy changes into each mount's source directory before backing up, so snapshots contain `./data/file.db` instead of `/var/lib/docker/volumes/<hash>/_data/data/file.db`. Mounts are tagged (`mount:<name>`) for correct parent snapshot selection.

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

Containers with `buoy.stop_before_backup=true` **must not** have `restart: always` (or similar). If they do, Docker restarts them immediately after buoy stops them, causing a race with the backup. Use `restart: "no"` or omit the restart policy on containers that buoy stops.

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
