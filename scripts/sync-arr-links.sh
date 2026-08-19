#!/bin/bash
# Push Radarr/Sonarr's connection into Overseerr and Bazarr.
#
# Both were linked by hand through their own UI (paste the *arr API key, pick a quality profile
# and root folder) and that link lives only in each app's own appdata, not in any compose file or
# .env. Radarr/Sonarr's API keys are read live from their config.xml and never written to git;
# the only thing tracked here is config/overseerr-links.json, which holds preferences (which
# quality profile, which root folder), not secrets.
#
# Bazarr has no host-published port (see compose-media.yml), so its call is proxied through a
# container already on media-network rather than localhost.
set -uo pipefail

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APPDATA_ROOT="/home/raspi/rpi-homeserver/appdata"
LINKS_FILE="$PROJECT_DIR/config/overseerr-links.json"
# A service that accepts the connection and then never answers (Bazarr wedged on 2026-08-19) used
# to hang this script, and with it the deploy lock, forever.
CURL_LIMITS="--connect-timeout 5 --max-time 30"

failures=0

arr_key() {
    local conf="$APPDATA_ROOT/$1/config.xml"
    sudo test -f "$conf" || return 1
    sudo grep -oP '(?<=<ApiKey>)[^<]+' "$conf"
}

arr_profile_id() {
    # arr_profile_id PORT KEY NAME
    curl -sf $CURL_LIMITS "http://localhost:$1/api/v3/qualityprofile" -H "X-Api-Key: $2" \
        | jq -r --arg n "$3" '.[] | select(.name==$n) | .id' | head -1
}

sync_one_overseerr_app() {
    # sync_one_overseerr_app APP PORT OVERSEERR_KEY
    local app=$1 port=$2 overseerr_key=$3
    local arr_key_val
    arr_key_val=$(arr_key "$app") || { echo "overseerr: $app not running, skipping its link" >&2; return 0; }

    local cfg profile_id body existing existing_id
    cfg=$(jq --arg a "$app" '.[$a]' "$LINKS_FILE")
    [[ "$cfg" == "null" ]] && return 0
    profile_id=$(arr_profile_id "$port" "$arr_key_val" "$(jq -r '.activeProfileName' <<< "$cfg")")
    body=$(jq --arg key "$arr_key_val" --argjson pid "${profile_id:-0}" \
        '. + {apiKey: $key, activeProfileId: $pid}' <<< "$cfg")

    existing=$(curl -sf $CURL_LIMITS "http://localhost:5055/api/v1/settings/$app" -H "X-Api-Key: $overseerr_key") || {
        failures=$((failures + 1)); return
    }
    existing_id=$(jq -r --arg h "$(jq -r '.hostname' <<< "$cfg")" \
        '[.[] | select(.hostname==$h)][0].id // empty' <<< "$existing")

    if [[ -n "$existing_id" ]]; then
        curl -sf $CURL_LIMITS -X PUT "http://localhost:5055/api/v1/settings/$app/$existing_id" \
            -H "X-Api-Key: $overseerr_key" -H "Content-Type: application/json" \
            --data "$body" > /dev/null || failures=$((failures + 1))
    else
        curl -sf $CURL_LIMITS -X POST "http://localhost:5055/api/v1/settings/$app" \
            -H "X-Api-Key: $overseerr_key" -H "Content-Type: application/json" \
            --data "$body" > /dev/null || failures=$((failures + 1))
    fi
}

sync_overseerr() {
    [[ -f "$LINKS_FILE" ]] || return 0
    local settings="$APPDATA_ROOT/overseerr/settings.json"
    sudo test -f "$settings" || return 0
    local overseerr_key
    overseerr_key=$(sudo jq -r '.main.apiKey // empty' "$settings")
    [[ -n "$overseerr_key" ]] || { echo "overseerr: no apiKey yet, skipping" >&2; return 0; }

    sync_one_overseerr_app radarr 7878 "$overseerr_key"
    sync_one_overseerr_app sonarr 8989 "$overseerr_key"
    echo "overseerr: radarr/sonarr links synced"
}

sync_bazarr() {
    local conf="$APPDATA_ROOT/bazarr/config/config.yaml"
    sudo test -f "$conf" || return 0
    local bazarr_key
    bazarr_key=$(sudo python3 -c "
import yaml
print(yaml.safe_load(open('$conf'))['auth']['apikey'])" 2>/dev/null)
    [[ -n "$bazarr_key" ]] || { echo "bazarr: no apikey yet, skipping" >&2; return 0; }

    local data="" radarr_key_val sonarr_key_val
    if radarr_key_val=$(arr_key radarr); then
        data+="settings-radarr-ip=radarr&settings-radarr-port=7878&settings-radarr-apikey=$radarr_key_val&"
    fi
    if sonarr_key_val=$(arr_key sonarr); then
        data+="settings-sonarr-ip=sonarr&settings-sonarr-port=8989&settings-sonarr-apikey=$sonarr_key_val&"
    fi
    [[ -n "$data" ]] || return 0

    # Bazarr has no host-published port; reach it through a container on media-network.
    # timeout sobre el exec, no solo sobre el curl: hoy este docker exec se quedo colgado 53
    # minutos con el lock del despliegue cogido, y --max-time de curl no acota el exec.
    timeout 45 docker exec maintainerr curl -sf $CURL_LIMITS -X POST "http://bazarr:6767/api/system/settings" \
        -H "X-API-KEY: $bazarr_key" --data "${data%&}" > /dev/null || {
        failures=$((failures + 1)); return
    }
    echo "bazarr: radarr/sonarr connection synced"
}

sync_overseerr
sync_bazarr

[[ $failures -eq 0 ]] || exit 1
