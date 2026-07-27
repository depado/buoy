# Deployment

## Docker container

Two image variants are available:

- **`ghcr.io/depado/buoy:latest`** - distroless (~15 MB), no shell or SSH client. Use for all backends except SFTP.
- **`ghcr.io/depado/buoy:latest-alpine`** - Alpine (~20 MB), includes `openssh-client` for the SFTP backend. Also works with all other backends.

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
      - TZ=Europe/Paris
      - BUOY_RESTIC_REPO_LOCAL_URL=/backup
      - BUOY_RESTIC_REPO_LOCAL_PASSWORD=${RESTIC_PASSWORD:?required}
      # - BUOY_NOTIFY_URLS=slack://tokenA/tokenB/tokenC
      # - BUOY_NOTIFY_LEVEL=error
    restart: unless-stopped

volumes:
  buoy_data:
```

Each path buoy needs to read must be mounted explicitly:

- **Named/anonymous volumes:** Mount `/var/lib/docker/volumes:/var/lib/docker/volumes:ro` (or your Docker data root). Covers all Docker-managed volumes.
- **Bind mounts:** Mount each host source path at the same location inside buoy. For example, if a container bind-mounts `/srv/app-data:/app/data`, mount `/srv/app-data:/srv/app-data:ro` in buoy's compose file.

If a mount source doesn't exist inside buoy, it's skipped with a warning.

## Timezone

Cron schedules respect the `TZ` environment variable. Set it to your local timezone so `30 6 * * *` runs at 6:30 AM local time, not UTC.

```yaml
environment:
  - TZ=Europe/Paris
```

Without `TZ`, schedules run on UTC.

## Restart policy caveat

> [!WARNING]
> Containers with `buoy.stop-before=true` **must not** have `restart: always` (or similar). If they do, Docker restarts them immediately after buoy stops them, causing a race with the backup. Use `restart: "no"` or omit the restart policy on containers that buoy stops.

## Signal handling

On Unix systems, pressing Ctrl+C sends SIGINT to the entire foreground process group, which includes restic child processes. buoy prevents this by placing restic in its own process group (`Setpgid`). This means backups complete cleanly even when buoy receives a shutdown signal.

When building from source on Windows, this protection is not applied. If you encounter partial backups during shutdown on Windows, consider running buoy inside the Linux container image instead.
