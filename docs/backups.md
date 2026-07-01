# Backups

Persistent container state lives in `appdata/` (not in git). `scripts/backup.sh` snapshots it
to a compressed, rotated archive on the external disk and reports health to Grafana.

## What is backed up

Everything under `appdata/` **except** regenerable or huge data:

| Excluded | Why |
| :--- | :--- |
| `prometheus/` | Metrics TSDB, regenerable. Capped at 15 days by default anyway. |
| `*/Cache/`, `*/Metadata` thumbnails, `Crash Reports`, `Diagnostics` | Plex/Jellyfin caches, rebuilt on demand |
| `*/log/`, `*/logs/`, `*.log` | Logs |

So the important state (Pi-hole, Grafana, *arr* databases, qBittorrent, Overseerr,
speedtest-tracker, vaultwarden, the Telegram bot DB, cal-bridge tokens) is all included.

## Configuration (`.env`)

```bash
BACKUP_DEST=/mnt/data/backups/appdata   # where archives go (default: ${DATA_ROOT}/backups/appdata)
BACKUP_RETENTION=7                       # how many daily archives to keep
BACKUP_RCLONE_REMOTE=                    # optional offsite copy, e.g. b2:my-bucket/rpi
```

## Schedule

Add a host cron entry (runs daily at 04:00):

```
0 4 * * * /home/raspi/rpi-homeserver/scripts/backup.sh >> /home/raspi/rpi-homeserver/backup.log 2>&1
```

## Restore

```bash
# List archives
ls -1t /mnt/data/backups/appdata/

# Stop the stack, restore, start again
docker compose down
sudo tar -xzf /mnt/data/backups/appdata/appdata-YYYYMMDD-HHMMSS.tar.gz -C /home/raspi/rpi-homeserver/
docker compose up -d
```

## Monitoring

`backup.sh` pushes to Pushgateway (visible in Grafana):

- `backup_last_status` — 0 ok, 1 error
- `backup_last_run_timestamp` — alert if it goes stale (no backup in >24h)
- `backup_last_size_bytes` — archive size
- `appdata_size_bytes` — total `appdata/` size on disk, to watch growth over time

## Offsite (recommended, not enabled by default)

Local backups protect against corruption/fat-fingers, not disk failure. For true safety,
set `BACKUP_RCLONE_REMOTE` and install `rclone` with a remote (Backblaze B2, another Pi, etc.).
The script copies each new archive to the remote automatically.
