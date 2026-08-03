#!/bin/bash
# Force an indexer search for every item still below its quality cutoff.
#
# The *arrs only query indexers when an item is added or when you hit search by hand.
# Upgrades otherwise rely on the release showing up in the RSS window, which rolls over
# (feed only carries the last N uploads), so a better release can exist on the indexer
# and never be grabbed. This closes that gap.
#
# Usage: cutoff-search.sh [radarr|sonarr ...]   (default: radarr)
#        DRY_RUN=1 reports the pending count without searching.
#
# Schedule via host cron (see scripts/crontab).

set -euo pipefail

PROJECT_DIR="$HOME/rpi-homeserver"
PUSHGATEWAY_URL="http://localhost:9091"
DRY_RUN="${DRY_RUN:-0}"

set -a; source "$PROJECT_DIR/.env"; set +a

# APP_CONFIG_PATH may be relative (e.g. ./appdata); resolve it against the project dir
APPDATA="${APP_CONFIG_PATH:-./appdata}"
[[ "$APPDATA" != /* ]] && APPDATA="$PROJECT_DIR/${APPDATA#./}"

declare -A PORT=([radarr]=7878 [sonarr]=8989)
declare -A COMMAND=([radarr]=CutoffUnmetMoviesSearch [sonarr]=CutoffUnmetEpisodeSearch)

APPS=("$@")
[ ${#APPS[@]} -eq 0 ] && APPS=(radarr)

log() { echo "$(date '+%Y-%m-%d %H:%M:%S') - $1"; }

push_metrics() {
    local app=$1 status=$2 pending=$3
    cat <<EOF | curl -fsSL --connect-timeout 5 --data-binary @- "${PUSHGATEWAY_URL}/metrics/job/cutoff_search/app/${app}" 2>/dev/null || true
# HELP cutoff_search_last_status Last cutoff-unmet search status (0=ok, 1=error)
# TYPE cutoff_search_last_status gauge
cutoff_search_last_status $status
# HELP cutoff_search_last_run_timestamp Last cutoff-unmet search run timestamp
# TYPE cutoff_search_last_run_timestamp gauge
cutoff_search_last_run_timestamp $(date +%s)
# HELP cutoff_search_pending_items Items below their quality cutoff when the search was triggered
# TYPE cutoff_search_pending_items gauge
cutoff_search_pending_items $pending
EOF
}

rc=0

for app in "${APPS[@]}"; do
    port="${PORT[$app]:-}"
    command="${COMMAND[$app]:-}"
    config="$APPDATA/$app/config.xml"

    if [ -z "$port" ] || [ ! -r "$config" ]; then
        log "$app: unsupported or config.xml not readable ($config)"
        rc=1
        continue
    fi

    key=$(sed -n 's:.*<ApiKey>\(.*\)</ApiKey>.*:\1:p' "$config")
    api="http://localhost:$port/api/v3"

    pending=$(curl -fsSL --connect-timeout 5 -H "X-Api-Key: $key" \
        "$api/wanted/cutoff?pageSize=1" 2>/dev/null \
        | grep -o '"totalRecords"[[:space:]]*:[[:space:]]*[0-9]*' | grep -o '[0-9]*$') || pending=""

    if [ -z "$pending" ]; then
        log "$app: could not reach the API on port $port"
        push_metrics "$app" 1 0
        rc=1
        continue
    fi

    if [ "$pending" -eq 0 ]; then
        log "$app: nothing below cutoff, skipping"
        push_metrics "$app" 0 0
        continue
    fi

    if [ "$DRY_RUN" = "1" ]; then
        log "$app: DRY_RUN, would run $command for $pending item(s)"
        continue
    fi

    if curl -fsSL --connect-timeout 5 -X POST -H "X-Api-Key: $key" \
        -H "Content-Type: application/json" -d "{\"name\":\"$command\"}" \
        "$api/command" >/dev/null 2>&1; then
        log "$app: $command queued for $pending item(s)"
        push_metrics "$app" 0 "$pending"
    else
        log "$app: $command was rejected by the API"
        push_metrics "$app" 1 "$pending"
        rc=1
    fi
done

exit $rc
