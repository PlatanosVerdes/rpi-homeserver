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
# Called from scripts/deploy/apply.sh. To run by hand: bash scripts/setup/install-crontab.sh

set -euo pipefail

PROJECT_DIR="${PROJECT_DIR:-$HOME/rpi-homeserver}"
SERVICES_DIR="${SERVICES_DIR:-$HOME/rpi-services}"

# Deliberately not mktemp: this runs from a deploy whose /tmp can be pulled out from under it
# when the webhook service restarts mid-run.
MERGED="$PROJECT_DIR/.crontab.merged"
trap 'rm -f "$MERGED"' EXIT

# The compose services this machine runs, for the `# when:` lines. Unreadable config keeps every
# job, which is what this did before the lines existed.
active=$(cd "$PROJECT_DIR" && COMPOSE_ENV_FILES=versions.env,.env docker compose config --services 2>/dev/null || echo '*')

awk -v dir="$PROJECT_DIR" -v active="$active" '
    BEGIN { n = split(active, list, "\n"); for (i = 1; i <= n; i++) on[list[i]] = 1 }
    /^# when: / {
        need = substr($0, 9)
        if (need ~ /^file /) keep = (system("test -e \"" dir "/" substr(need, 6) "\"") == 0)
        else keep = (on["*"] || on[need])
        pending = 1
        next
    }
    pending && /^[^#[:space:]]/ { pending = 0; if (!keep) next }
    !/^#/ { gsub(/@PROJECT_DIR@/, dir) }
    { print }
' "$PROJECT_DIR/scripts/crontab" > "$MERGED"

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
