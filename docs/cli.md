# CLI Reference

buoyctl communicates with a running buoy daemon via its HTTP API. Set `--api.url` and `--api.token` per command, or use the `BUOY_URL` / `BUOY_TOKEN` environment variables. Defaults to `http://127.0.0.1:8080`.

## `buoyctl version`

Display the version of buoyctl and the daemon (if connected).

```bash
buoyctl version
# buoyctl v0.4.0-dev (build abc1234, 2026-01-15)

buoy version
# buoy v0.4.0-dev (build abc1234, 2026-01-15)
```

## `buoyctl repo`

Query and operate on managed repositories.

```bash
buoyctl repo list                       # list active (non-orphaned) repos
buoyctl repo list --orphaned            # show only orphaned repos
buoyctl repo list --all                 # show all repos including orphaned
buoyctl repo list --repo /backup/myapp  # show a specific repo
buoyctl repo check                      # structural integrity check (active repos)
buoyctl repo check --read-data          # full data integrity check
buoyctl repo stats                      # storage usage across active repos
buoyctl repo unlock --repo /backup/myapp
buoyctl repo forget --retention keep-daily:7,keep-weekly:4 --all
buoyctl repo prune --orphaned

# JSON output
buoyctl repo list --json
buoyctl repo stats --json

# Remote daemon with auth
export BUOY_URL=https://buoy.internal.example.com
export BUOY_TOKEN=secret123
buoyctl repo stats
```

Read-only commands (`list`, `check`, `stats`) default to active (non-orphaned) repositories. Destructive commands (`unlock`, `forget`, `prune`) require an explicit scope via `--all`, `--orphaned`, or `--repo`.

> [!WARNING]
> Destructive operations (`unlock`, `forget`, `prune`) require an explicit scope flag (`--all`, `--orphaned`, or `--repo`). They also return an error if any backup is currently in progress, preventing accidental lock conflicts or corruption.

### Flags

| Flag          | Availability | Description                                                                    |
| ------------- | ------------ | ------------------------------------------------------------------------------ |
| `--json`      | all          | Output as JSON                                                                 |
| `--api.url`   | all          | Daemon API URL (defaults to `BUOY_URL` env)                                    |
| `--api.token` | all          | API bearer token (defaults to `BUOY_TOKEN` env)                                |
| `--orphaned`  | all          | Operate on orphaned repos only (mutually exclusive with `--all`)               |
| `--all`       | all          | Operate on all repos including orphaned (mutually exclusive with `--orphaned`) |
| `--repo`      | all          | Operate on a specific repository URL                                           |
| `--read-data` | `check`      | Read all pack files for full integrity check                                   |
| `--retention` | `forget`     | Retention policy (e.g. `keep-daily:7`)                                         |

## `buoyctl list`

Lists all currently active scheduled backups known to the daemon.

```bash
buoyctl list            # render as a table
buoyctl list --json     # JSON output
```

Shows container name, compose project/service, schedule expression, repos (if overridden), and whether the container is stopped before backup. Empty output means no containers are currently scheduled.

## `buoyctl backup`

Triggers an immediate backup without waiting for the cron schedule. Respects compose stack dependencies and `buoy.stop-before` - containers are stopped and restarted in the correct order, just like scheduled backups.

```bash
buoyctl backup uptime-kuma           # single container by name
buoyctl backup uptime-kuma beszel    # multiple containers
buoyctl backup --project myapp       # all services in a compose project
buoyctl backup --project myapp db api # specific services in a project
buoyctl backup --all                 # all scheduled containers
buoyctl backup --all --json          # JSON output
```

For compose projects, triggered backups serialize through the same project queue as scheduled backups - triggering a project backup while one is already running queues it behind the current one, preventing race conditions.

## `buoyctl discover`

Scans a directory recursively for Docker Compose files and lists the volumes and bind mounts buoy would need access to - so you can configure your buoy container with the right host mounts before deploying.

The output highlights bind mounts from enabled services, and produces a ready-to-paste `volumes:` block for buoy's compose service.

```bash
# Scan current directory (unlimited depth)
buoyctl discover .

# Scan a specific directory, two levels deep
buoyctl discover /opt/stacks --depth 2

# Unlimited depth
buoyctl discover /opt/stacks --depth -1

# Custom glob pattern for compose files
buoyctl discover /opt/stacks --pattern "stack.*.yml"

# JSON output
buoyctl discover . --json

# Resolve ${VAR} and ${VAR:-default} from .env and the environment
buoyctl discover /opt/stacks --resolve-env
```

### Flags

| Flag            | Default                            | Description                                                                      |
| --------------- | ---------------------------------- | -------------------------------------------------------------------------------- |
| `--json`        | `false`                            | Output as JSON                                                                   |
| `--depth`       | `-1`                               | Maximum directory depth (`-1` for unlimited)                                     |
| `--pattern`     | `compose.y*ml,docker-compose.y*ml` | Comma-separated glob patterns for compose file names                             |
| `--resolve-env` | `false`                            | Resolve `${VAR}` and `${VAR:-default}` from `.env` files and process environment |

### Label support

Respects buoy labels. The `buoy.enabled`, `buoy.include`/`buoy.exclude` labels defined in compose files are honored, including both map and list syntax:

```yaml
labels:
  buoy.enabled: "true"         # map syntax
  buoy.backup.tags: "production"

labels:
  - "buoy.enabled=true"        # list syntax
  - "traefik.enable=true"
```

- Services with `buoy.enabled: "true"` are shown as enabled; disabled or unlabeled services are shown as disabled.
- `buoy.include` / `buoy.exclude` filter mounts by volume name, source path, or destination path. `include` supports optional `name=value` syntax for per-mount backup overrides (e.g. `src=/app/code`).
- Bind mounts from enabled services are shown with paths to add to buoy's compose service. Built-in mounts (`/var/run/docker.sock`, `/var/lib/docker/volumes`) are excluded.

### Example output

```
       /opt/stacks/webapp/compose.yaml

  Service  Enabled  Type    Source            Destination     Mode
 ───────────────────────────────────────────────────────────────────────
  db       yes      volume  db_data           /var/lib/mysql   rw
  db       yes      bind    /srv/backups      /backups         rw
  api      yes      bind    /var/run/docker…  /var/run/docker… ro
  web      no       bind    ./html            /usr/share/nginx ro

       /opt/stacks/worker/compose.yaml

  Service  Enabled  Type    Source     Destination  Mode
 ───────────────────────────────────────────────────────────────────────
  worker   yes      bind    ./jobs     /jobs        rw


Add to buoy's compose service volumes:
volumes:
  - /opt/stacks/webapp/backups:/opt/stacks/webapp/backups:ro
  - /opt/stacks/worker/jobs:/opt/stacks/worker/jobs:ro
  - /var/run/docker.sock:/var/run/docker.sock:ro
  - /var/lib/docker/volumes:/var/lib/docker/volumes:ro
```
