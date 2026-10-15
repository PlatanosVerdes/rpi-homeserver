#!/bin/bash
# Install the udev rules in config/udev/ and apply them to the disks already attached.
#
# Same idea as install-logrotate.sh: a tuning that only lives in /etc is lost with the SD card.
# Idempotent, only writes when the installed file differs. Called from scripts/deploy/apply.sh.

set -euo pipefail

PROJECT_DIR="${PROJECT_DIR:-$HOME/rpi-homeserver}"
SRC_DIR="$PROJECT_DIR/config/udev"

changed=0
for src in "$SRC_DIR"/*.rules; do
    [[ -f "$src" ]] || continue
    dst="/etc/udev/rules.d/$(basename "$src")"
    if ! sudo cmp -s "$src" "$dst" 2>/dev/null; then
        sudo install -o root -g root -m 644 "$src" "$dst"
        echo "installed $(basename "$src")"
        changed=1
    fi
done

if [[ $changed -eq 0 ]]; then
    echo "udev rules already up to date"
    exit 0
fi

# A rule only runs on an event, so a disk plugged in since boot keeps its old value until this
sudo udevadm control --reload
sudo udevadm trigger --action=change --subsystem-match=block
sudo udevadm settle
for disk in /sys/block/sd*; do
    [[ -e "$disk" ]] && echo "$(basename "$disk") read_ahead_kb=$(cat "$disk/queue/read_ahead_kb")"
done
