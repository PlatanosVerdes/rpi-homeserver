#!/bin/bash
# Converge qBittorrent's tuning to config/qbittorrent/preferences.json.
#
# Same problem the other sync scripts solve: these were set by hand through the WebUI (or its API)
# and live only in appdata/qbittorrent/qBittorrent.conf, so a lost or rebuilt Pi silently goes back
# to unlimited downloads and default queue limits. SYSTEM_NOTES.md used to carry them as "remember
# to set these again"; this makes them reproducible instead.
#
# Only the keys present in the JSON are touched, so everything not listed there (WebUI credentials,
# categories, connection settings) stays whatever the app already had. Nothing here is a secret.
#
# The call goes through the container because qBittorrent's WebUI trusts localhost: from the host it
# would arrive via the Docker gateway, which is not in its AuthSubnetWhitelist, and would need
# credentials. Same trick as media-metrics.py.
set -uo pipefail

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PREFS_FILE="$PROJECT_DIR/config/qbittorrent/preferences.json"
API="http://localhost:8080/api/v2"

[[ -f "$PREFS_FILE" ]] || exit 0
docker ps --format '{{.Names}}' | grep -qx qbittorrent || exit 0

read_prefs() {
    docker exec qbittorrent curl -sf --max-time 15 "$API/app/preferences"
}

# The tracked keys whose live value differs from what is committed.
drift_from() {
    jq -n --argjson want "$(cat "$PREFS_FILE")" --argjson have "$1" \
        '$want | to_entries | map(select($have[.key] != .value)) | from_entries'
}

current=$(read_prefs) || { echo "WebUI did not answer, skipping" >&2; exit 1; }

drift=$(drift_from "$current")
[[ "$(jq -r 'length' <<< "$drift")" -eq 0 ]] && exit 0

docker exec qbittorrent curl -sf --max-time 15 -X POST "$API/app/setPreferences" \
    --data-urlencode "json=$drift" > /dev/null || {
    echo "setPreferences rejected $(jq -c 'keys' <<< "$drift")" >&2
    exit 1
}

# Read back rather than trusting the 200: qBittorrent accepts a key it does not know and silently
# drops it, so without this a typo in the JSON would report success on every deploy forever.
after=$(read_prefs) || { echo "applied, but could not read back to verify" >&2; exit 1; }
still=$(drift_from "$after")

echo "applied $(jq -c 'keys' <<< "$drift")"
if [[ "$(jq -r 'length' <<< "$still")" -ne 0 ]]; then
    echo "did NOT stick: $(jq -c 'keys' <<< "$still") — unknown key, or a value this build rejects" >&2
    exit 1
fi
