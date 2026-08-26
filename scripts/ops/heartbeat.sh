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

# A deploy recreates caddy, and the check below would call those 30 seconds an outage. That is not
# theoretical: on 2026-08-25 two deploys produced a DOWN and an UP a minute apart, at 10:26 and
# 11:56, and both were caddy being recreated. The check's grace period does not save you either,
# because a /fail is acted on immediately, which is the whole point of a /fail.
#
# So while apply.sh holds its lock, a missing container is expected rather than news. Capped at ten
# minutes, which is two ARM builds: past that, a deploy still running AND an essential container
# missing is an outage whatever the lock says.
deploying() {
    local holder age
    holder=$(head -1 "$PROJECT_DIR/.deploy.lock" 2>/dev/null || true)
    [[ "$holder" =~ ^[0-9]+$ ]] || return 1
    age=$(ps -o etimes= -p "$holder" 2>/dev/null | tr -d " ")
    [[ -n "$age" ]] && (( age < 600 ))
}

# Report a failure when the essentials are not actually working, so a Pi that is powered on but
# broken does not keep sending a reassuring heartbeat.
# One docker call, not one per container: it is the expensive part of this script (~0.5s) and this
# runs every minute.
running=$(docker ps --format '{{.Names}}' 2>/dev/null)
reason=""
for name in caddy prometheus; do
    grep -qx "$name" <<< "$running" || { reason="$name is not running"; break; }
done

if [[ -n "$reason" ]] && deploying; then
    reason=""
fi

if [[ -n "$reason" ]]; then
    curl -fsS -m 10 --data-raw "$reason" "${HEALTHCHECK_URL}/fail" >/dev/null 2>&1
    exit 1
fi

curl -fsS -m 10 "$HEALTHCHECK_URL" >/dev/null 2>&1
