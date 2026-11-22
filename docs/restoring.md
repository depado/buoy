# Restoring

buoy does not have a built-in restore command. Use restic directly.

Recovery is **per mount**. Each backup run creates one snapshot per mount per repo, tagged `mount:<identifier>`, and there is no single application-wide snapshot. Restore every mount you need, or you will silently get one volume back. Mounts are captured one after another, so snapshots from the same run are not a single point in time; set `buoy.stop-before=true` if you need the application stopped while it backs up.

The identifier is the Docker volume name, or the container destination path when the mount has no volume name (bind mounts). tmpfs mounts are never backed up.

## Find the mount tags

```bash
restic -r /backup/myapp/postgres snapshots --json \
  | jq -r '.[].tags[]?' | grep '^mount:' | sort -u
```

That prints one tag per line, for example `mount:data` and `mount:pgdata`. Tags from `buoy.backup.tags` appear in the same list. The mount tag is always `mount:` followed by the volume name or destination path.

## List the snapshots for one mount

```bash
restic -r /backup/myapp/postgres snapshots --group-by host,tags --tag mount:data
restic -r /backup/myapp/postgres snapshots --group-by host,tags --tag mount:pgdata
```

Snapshots with the same tags group by host (the container name) and tags, so each mount has its own list. Pick snapshot IDs from the same run window.

## Restore each mount into its own directory

```bash
restic -r /backup/myapp/postgres restore <data-snapshot-id>   --target /tmp/restore/data
restic -r /backup/myapp/postgres restore <pgdata-snapshot-id> --target /tmp/restore/pgdata
```

Move the files into place, then restart the container. For a compose stack, `docker compose stop postgres` before replacing data and `docker compose start postgres` after.

Since buoy backs up relative entries from the mount root, restored files go directly into the target directory without the full host path prefix.

## Limits

- Snapshots from different mounts are not a single point in time.
- Restoring two mounts into the same target can overwrite files that share a name. Use one target per mount.
- A mount that was empty at backup time produces no snapshot, and a run where every eligible mount was empty is reported as a failure (`no snapshots created`). A missing snapshot is therefore not a broken restore, just no data for that mount.
- Retention runs per mount tag, so different mounts may have different snapshot counts.

## Cloud backends

Include the relevant credentials:

```bash
export RESTIC_PASSWORD=your-password
export AWS_ACCESS_KEY_ID=...
export AWS_SECRET_ACCESS_KEY=...
restic -r s3:s3.amazonaws.com/my-bucket/myapp/postgres snapshots --tag mount:data
restic -r s3:s3.amazonaws.com/my-bucket/myapp/postgres restore <data-snapshot-id> --target /tmp/restore/data
```
