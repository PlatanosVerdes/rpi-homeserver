#!/bin/bash
# Install the logrotate policy for both repos' cron logs from the copy in git.
#
# Same idea as scripts/setup/install-crontab.sh: the thing that keeps the Pi's logs bounded should come back
# with a `git clone`, not from a note in SYSTEM_NOTES.md. Idempotent, only writes when the
# installed file differs, and prints what changed so the deploy log shows it.
#
# Called from scripts/deploy/apply.sh. To run by hand: bash scripts/setup/install-logrotate.sh

set -euo pipefail

PROJECT_DIR="${PROJECT_DIR:-$HOME/rpi-homeserver}"
SRC="$PROJECT_DIR/config/logrotate/rpi-homeserver"
DST="/etc/logrotate.d/rpi-homeserver"

[[ -f "$SRC" ]] || { echo "no policy at $SRC, skipping"; exit 0; }

if sudo cmp -s "$SRC" "$DST" 2>/dev/null; then
    echo "logrotate policy already up to date"
    exit 0
fi

# logrotate ignores any file in logrotate.d that is group- or world-writable, and refuses to run
# the whole directory if one entry is not owned by root. Both are silent failures.
sudo install -o root -g root -m 644 "$SRC" "$DST"

if ! sudo logrotate -d "$DST" >/dev/null 2>&1; then
    echo "WARNING: logrotate rejected the new policy, see: sudo logrotate -d $DST" >&2
    exit 1
fi

echo "logrotate policy installed"
