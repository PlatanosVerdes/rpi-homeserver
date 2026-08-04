#!/bin/bash
# Dead man's switch: tell an outside service the Pi is still alive.
#
# Every Grafana alert dies with the Pi, so a power cut, a dead SD card or an ISP outage produces
# silence, which looks exactly like "everything is fine". This inverts it: the Pi pings an external
# check on a schedule, and that service alerts when the pings STOP.
#
# Set HEALTHCHECK_URL in .env (e.g. a healthchecks.io ping URL). With it unset this is a no-op,
# so a clone without an account is not broken, just uncovered.
#
# Schedule via host cron (see scripts/crontab).

set -uo pipefail

PROJECT_DIR="$HOME/rpi-homeserver"
set -a; source "$PROJECT_DIR/.env"; set +a

[[ -z "${HEALTHCHECK_URL:-}" ]] && exit 0

# Report a failure when the essentials are not actually working, so a Pi that is powered on but
# broken does not keep sending a reassuring heartbeat.
reason=""
docker ps --format '{{.Names}}' 2>/dev/null | grep -q '^caddy$' || reason="caddy is not running"
[[ -z "$reason" ]] && ! docker ps --format '{{.Names}}' 2>/dev/null | grep -q '^prometheus$' \
    && reason="prometheus is not running"

if [[ -n "$reason" ]]; then
    curl -fsS -m 10 --data-raw "$reason" "${HEALTHCHECK_URL}/fail" >/dev/null 2>&1
    exit 1
fi

curl -fsS -m 10 "$HEALTHCHECK_URL" >/dev/null 2>&1
