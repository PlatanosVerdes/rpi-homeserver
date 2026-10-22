# Backups

Persistent container state lives in `appdata/` (not in git). `scripts/ops/backup.sh` snapshots it
to a compressed, rotated archive on the external disk and reports health to Grafana. It backs
up **both** `rpi-homeserver/appdata` and `rpi-services/appdata` (the latter holds the personal
apps' data, such as the Telegram bot DB).

Storage note: the media/data volume `/mnt/data` is a **mergerfs pool** of two disks
(`/mnt/disk1` + `/mnt/disk2`); backups land on the pool, which is a different physical device
from the SD card where `appdata` actually lives, so a card failure never takes the backups too.

## What is backed up

Everything under `appdata/` **except** regenerable or huge data:

| Excluded | Why |
| :--- | :--- |
| `prometheus/` | Metrics TSDB, regenerable. Capped at 15 days by default anyway. |
| `*/Cache/`, `*/Metadata` thumbnails, `Crash Reports`, `Diagnostics` | Plex/Jellyfin caches, rebuilt on demand |
| `*/log/`, `*/logs/`, `*.log` | Logs |

So the important state (Pi-hole, Grafana, *arr* databases, qBittorrent, Seerr,
speedtest-tracker, the Telegram bot DB) is all included.

## Configuration (`.env`)

```bash
BACKUP_DEST=/mnt/data/backups/appdata   # where archives go (default: ${DATA_ROOT}/backups/appdata)
BACKUP_RETENTION=7                       # how many daily archives to keep
BACKUP_RCLONE_REMOTE=                    # optional offsite copy, e.g. b2:my-bucket/rpi
```

## Schedule

Already scheduled, nothing to install. The entry lives in `scripts/crontab`, which is this repo's
cron fragment, and `scripts/setup/install-crontab.sh` writes it into the host crontab on every deploy:

```
0 4 * * * @PROJECT_DIR@/scripts/ops/backup.sh >> @PROJECT_DIR@/backup.log 2>&1
```

`install-crontab.sh` replaces `@PROJECT_DIR@` with the checkout on each node, so every node runs it.

Change the time there rather than with `crontab -e`, or the next deploy overwrites it.

## Restore

```bash
# List archives
ls -1t /mnt/data/backups/appdata/

# Stop the stack, restore, start again
export COMPOSE_ENV_FILES=versions.env,.env
docker compose down
sudo tar -xzf /mnt/data/backups/appdata/appdata-YYYYMMDD-HHMMSS.tar.gz -C /home/raspi/rpi-homeserver/
docker compose up -d
bash scripts/recovery/recovery-status.sh    # what no archive can put back
```

**The archive is a hot copy.** It is taken at 04:00 while every container runs: `prowlarr.db`,
`radarr.db`, `sonarr.db`, `autobrr.db`, `maintainerr.sqlite`, `grafana.db` and the rest are SQLite
files being written as tar reads them (their `-wal` and `-shm` files come along). A restore usually
recovers cleanly rather than certainly. Nothing stops the containers first, on purpose: the trade is
a backup that never needs a maintenance window.

What git already holds, and what only this archive has (Prowlarr's indexers, qBittorrent's
`BT_backup`, Plex's libraries and history), is in
[architecture.md](architecture.md#what-git-does-not-have).

## Monitoring

`scripts/ops/backup.sh` pushes to Pushgateway (visible in the **Backup Monitor** Grafana dashboard):

- `backup_last_status` — 0 ok, 1 error
- `backup_last_run_timestamp` — alert if it goes stale (no backup in >36h)
- `backup_last_size_bytes` — archive size
- `appdata_size_bytes` — total `appdata/` size on disk, to watch growth over time

Every exit reports, including one that never got as far as reading `.env`. The reason the trap is
on EXIT and not ERR is in the script. What it buys is that a failed run cannot leave
`backup_last_status` reading 0 from the last one that worked, which is the state where the only
sign is the staleness alert 36 hours later.

## Offsite (recommended, not enabled by default)

Local backups protect against corruption/fat-fingers, not disk failure. For true safety,
set `BACKUP_RCLONE_REMOTE` and install `rclone` with a remote (Backblaze B2, another Pi, etc.).
The script copies each new archive to the remote, then deletes remote archives older than
`BACKUP_RETENTION` days (`rclone copy` alone never deletes, so the remote would keep everything).

A second node whose `appdata` lives on an SD card points `BACKUP_RCLONE_REMOTE` at the main Pi, so
its archive ends up on a different device.
