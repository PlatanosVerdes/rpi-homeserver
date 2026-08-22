#!/bin/bash
# Install the host crontab from the per-repo fragments.
#
#   rpi-homeserver/scripts/crontab   generic jobs
#   rpi-services/scripts/crontab     personal jobs (optional, skipped if the repo is absent)
#
# Same idea as Caddy importing config/caddy/services/: the generic repo never has to know what
# the personal one schedules. Idempotent, only touches the live crontab when the merged result
# differs, and prints the diff so the deploy log shows what changed.
#
# Called from scripts/deploy/apply.sh. To run by hand: bash scripts/deploy/install-crontab.sh

set -euo pipefail

PROJECT_DIR="${PROJECT_DIR:-$HOME/rpi-homeserver}"
SERVICES_DIR="${SERVICES_DIR:-$HOME/rpi-services}"

# Deliberately not mktemp: this runs from a deploy whose /tmp can be pulled out from under it
# when the webhook service restarts mid-run.
MERGED="$PROJECT_DIR/.crontab.merged"
trap 'rm -f "$MERGED"' EXIT

cat "$PROJECT_DIR/scripts/crontab" > "$MERGED"

if [[ -f "$SERVICES_DIR/scripts/crontab" ]]; then
    printf '\n' >> "$MERGED"
    cat "$SERVICES_DIR/scripts/crontab" >> "$MERGED"
fi

if crontab -l 2>/dev/null | diff -q - "$MERGED" >/dev/null 2>&1; then
    echo "crontab already up to date"
    exit 0
fi

echo "crontab differs, installing (- live, + repo):"
crontab -l 2>/dev/null | diff -u - "$MERGED" | tail -n +3 | grep -E '^[+-]' || true
crontab "$MERGED"
echo "crontab installed"
