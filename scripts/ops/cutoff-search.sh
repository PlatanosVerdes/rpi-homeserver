#!/bin/bash
# Force an indexer search for everything the *arrs are still waiting for.
#
# They only query indexers when an item is added or when you hit search by hand. Otherwise a
# release has to show up in the RSS window, which rolls over (the feed only carries the last N
# uploads), so a better release can exist on the indexer and never be grabbed. This closes that.
#
# Two separate lists, both covered here, because the *arrs treat them independently:
#   cutoff   there is a file but below the quality cutoff  -> Cutoff Unmet
#   missing  monitored with no file at all                 -> Missing
#
# Usage: scripts/ops/cutoff-search.sh [radarr|sonarr ...]   (default: radarr)
#        DRY_RUN=1 reports the pending counts without searching.
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
# Command names verified against Radarr.Core.dll / Sonarr.Core.dll, they differ per app
declare -A CUTOFF_COMMAND=([radarr]=CutoffUnmetMoviesSearch [sonarr]=CutoffUnmetEpisodeSearch)
declare -A MISSING_COMMAND=([radarr]=MissingMoviesSearch [sonarr]=MissingEpisodeSearch)

APPS=("$@")
[ ${#APPS[@]} -eq 0 ] && APPS=(radarr)

log() { echo "$(date '+%Y-%m-%d %H:%M:%S') - $1"; }

push_metrics() {
    local app=$1 kind=$2 status=$3 pending=$4
    cat <<EOF | curl -fsSL --connect-timeout 5 --data-binary @- "${PUSHGATEWAY_URL}/metrics/job/cutoff_search/app/${app}/kind/${kind}" 2>/dev/null || true
# HELP cutoff_search_last_status Last search status (0=ok, 1=error)
# TYPE cutoff_search_last_status gauge
cutoff_search_last_status $status
# HELP cutoff_search_last_run_timestamp Last search run timestamp
# TYPE cutoff_search_last_run_timestamp gauge
cutoff_search_last_run_timestamp $(date +%s)
# HELP cutoff_search_pending_items Items waiting when the search was triggered
# TYPE cutoff_search_pending_items gauge
cutoff_search_pending_items $pending
EOF
}

# search <app> <kind> <api base> <api key> <wanted endpoint> <command>
search() {
    local app=$1 kind=$2 api=$3 key=$4 endpoint=$5 command=$6
    local pending

    # The arrs pretty-print their JSON, so allow whitespace around the colon
    pending=$(curl -fsSL --connect-timeout 5 -H "X-Api-Key: $key" \
        "$api/wanted/$endpoint?pageSize=1" 2>/dev/null \
        | grep -o '"totalRecords"[[:space:]]*:[[:space:]]*[0-9]*' | grep -o '[0-9]*$') || pending=""

    if [ -z "$pending" ]; then
        log "$app/$kind: could not reach the API"
        push_metrics "$app" "$kind" 1 0
        return 1
    fi

    if [ "$pending" -eq 0 ]; then
        log "$app/$kind: nothing pending, skipping"
        push_metrics "$app" "$kind" 0 0
        return 0
    fi

    if [ "$DRY_RUN" = "1" ]; then
        log "$app/$kind: DRY_RUN, would run $command for $pending item(s)"
        return 0
    fi

    if curl -fsSL --connect-timeout 5 -X POST -H "X-Api-Key: $key" \
        -H "Content-Type: application/json" -d "{\"name\":\"$command\"}" \
        "$api/command" >/dev/null 2>&1; then
        log "$app/$kind: $command queued for $pending item(s)"
        push_metrics "$app" "$kind" 0 "$pending"
        return 0
    fi

    log "$app/$kind: $command was rejected by the API"
    push_metrics "$app" "$kind" 1 "$pending"
    return 1
}

rc=0

for app in "${APPS[@]}"; do
    port="${PORT[$app]:-}"
    config="$APPDATA/$app/config.xml"

    if [ -z "$port" ] || [ ! -r "$config" ]; then
        log "$app: unsupported or config.xml not readable ($config)"
        rc=1
        continue
    fi

    key=$(sed -n 's:.*<ApiKey>\(.*\)</ApiKey>.*:\1:p' "$config")
    api="http://localhost:$port/api/v3"

    search "$app" cutoff  "$api" "$key" cutoff  "${CUTOFF_COMMAND[$app]}"  || rc=1
    search "$app" missing "$api" "$key" missing "${MISSING_COMMAND[$app]}" || rc=1
done

exit $rc
