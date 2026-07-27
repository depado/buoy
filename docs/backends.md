# Backends

buoy supports all [restic backends](https://restic.readthedocs.io/en/stable/030_preparing_a_new_repo.html):

- Local filesystem: `/backup`
- Amazon S3: `s3:s3.amazonaws.com/bucket`
- Backblaze B2: `b2:bucketname:/path`
- Azure Blob: `azure:container:/path`
- Google Cloud: `gs:bucket:/path`
- SFTP: `sftp:user@host:/path`
- REST server: `rest:https://host:8000/`
- rclone: `rclone:remote:path`

Each container backs up to all configured repos simultaneously. Cloud credentials are passed via standard restic environment variables.

```yaml
restic:
  repos:
    local:
      url: /backup
    s3:
      url: s3:s3.amazonaws.com/my-bucket
      password: "s3-specific-password"
    b2:
      url: b2:my-bucket:/buoy
      password: "b2-specific-password"
```
