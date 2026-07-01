#!/bin/bash
# Back up persistent container state (appdata) to a compressed, rotated archive.
#
# Regenerable/huge data is excluded (Prometheus TSDB, Plex/Jellyfin caches, logs).
# Pushes metrics to Pushgateway so backup health shows up in Grafana.
#
# Schedule via host cron (see bottom of file).

set -euo pipefail

PROJECT_DIR="$HOME/rpi-homeserver"
PUSHGATEWAY_URL="http://localhost:9091"

set -a; source "$PROJECT_DIR/.env"; set +a
cd "$PROJECT_DIR"

# APP_CONFIG_PATH may be relative (e.g. ./appdata); resolve it against the project dir
APPDATA="${APP_CONFIG_PATH:-./appdata}"
[[ "$APPDATA" != /* ]] && APPDATA="$PROJECT_DIR/${APPDATA#./}"
DEST="${BACKUP_DEST:-${DATA_ROOT}/backups/appdata}"
RETENTION="${BACKUP_RETENTION:-7}"

log() { echo "$(date '+%Y-%m-%d %H:%M:%S') - $1"; }

push_metrics() {
    local status=$1 size=$2 appdata_size=${3:-0}
    cat <<EOF | curl -fsSL --connect-timeout 5 --data-binary @- "${PUSHGATEWAY_URL}/metrics/job/backup" 2>/dev/null || true
# HELP backup_last_status Last backup status (0=ok, 1=error)
# TYPE backup_last_status gauge
backup_last_status $status
# HELP backup_last_run_timestamp Last backup run timestamp
# TYPE backup_last_run_timestamp gauge
backup_last_run_timestamp $(date +%s)
# HELP backup_last_size_bytes Size of the last backup archive
# TYPE backup_last_size_bytes gauge
backup_last_size_bytes $size
# HELP appdata_size_bytes Total size of appdata on disk (monitors growth over time)
# TYPE appdata_size_bytes gauge
appdata_size_bytes $appdata_size
EOF
}

trap 'log "Backup FAILED"; push_metrics 1 0; exit 1' ERR

mkdir -p "$DEST"
STAMP=$(date +%Y%m%d-%H%M%S)
ARCHIVE="$DEST/appdata-$STAMP.tar.gz"

log "Backing up $APPDATA -> $ARCHIVE"
sudo tar \
    --exclude='*/prometheus/*' \
    --exclude='*/Cache/*' \
    --exclude='*/Crash Reports/*' \
    --exclude='*/Diagnostics/*' \
    --exclude='*/Logs/*' \
    --exclude='*/log/*' \
    --exclude='*/logs/*' \
    --exclude='*.log' \
    --exclude='*.sock' \
    -czf "$ARCHIVE" -C "$(dirname "$APPDATA")" "$(basename "$APPDATA")"

SIZE=$(stat -c%s "$ARCHIVE" 2>/dev/null || echo 0)
APPDATA_SIZE=$(sudo du -sb "$APPDATA" 2>/dev/null | cut -f1 || echo 0)
log "Archive created ($((SIZE / 1024 / 1024)) MiB); appdata on disk: $((APPDATA_SIZE / 1024 / 1024)) MiB"

# Retention: keep the newest $RETENTION archives, delete the rest
log "Pruning old backups (keeping $RETENTION)..."
ls -1t "$DEST"/appdata-*.tar.gz 2>/dev/null | tail -n +$((RETENTION + 1)) | while read -r old; do
    log "Removing $old"
    sudo rm -f "$old"
done

# Optional offsite copy: set BACKUP_RCLONE_REMOTE (e.g. "b2:my-bucket/rpi") in .env
if [[ -n "${BACKUP_RCLONE_REMOTE:-}" ]] && command -v rclone >/dev/null 2>&1; then
    log "Copying to offsite remote $BACKUP_RCLONE_REMOTE"
    rclone copy "$ARCHIVE" "$BACKUP_RCLONE_REMOTE" || log "Offsite copy failed (non-fatal)"
fi

push_metrics 0 "$SIZE" "$APPDATA_SIZE"
log "Backup done."

# crontab -e
# 0 4 * * * /home/raspi/rpi-homeserver/scripts/backup.sh >> /home/raspi/rpi-homeserver/backup.log 2>&1
