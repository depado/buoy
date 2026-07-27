# Restoring

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
