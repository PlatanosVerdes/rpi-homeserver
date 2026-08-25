#!/bin/bash
# Push Radarr/Sonarr's connection into Seerr and Bazarr.
#
# That link lives only in each app's appdata. The arr API keys are read live from config.xml and
# never written to git; config/seerr-links.json holds only preferences, no secrets.
#
# Bazarr publishes no port, so its call is proxied through a container already on media-network.
set -uo pipefail

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
APPDATA_ROOT="/home/raspi/rpi-homeserver/appdata"
LINKS_FILE="$PROJECT_DIR/config/seerr-links.json"
# Every call is capped: a service that accepts the connection and then never answers would hang
# this script, and the deploy lock with it.
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

sync_one_seerr_app() {
    # sync_one_seerr_app APP PORT SEERR_KEY
    local app=$1 port=$2 seerr_key=$3
    local arr_key_val
    arr_key_val=$(arr_key "$app") || { echo "seerr: $app not running, skipping its link" >&2; return 0; }

    local cfg profile_id body existing existing_id
    cfg=$(jq --arg a "$app" '.[$a]' "$LINKS_FILE")
    [[ "$cfg" == "null" ]] && return 0
    profile_id=$(arr_profile_id "$port" "$arr_key_val" "$(jq -r '.activeProfileName' <<< "$cfg")")
    body=$(jq --arg key "$arr_key_val" --argjson pid "${profile_id:-0}" \
        '. + {apiKey: $key, activeProfileId: $pid}' <<< "$cfg")

    existing=$(curl -sf $CURL_LIMITS "http://localhost:5055/api/v1/settings/$app" -H "X-Api-Key: $seerr_key") || {
        failures=$((failures + 1)); return
    }
    existing_id=$(jq -r --arg h "$(jq -r '.hostname' <<< "$cfg")" \
        '[.[] | select(.hostname==$h)][0].id // empty' <<< "$existing")

    if [[ -n "$existing_id" ]]; then
        curl -sf $CURL_LIMITS -X PUT "http://localhost:5055/api/v1/settings/$app/$existing_id" \
            -H "X-Api-Key: $seerr_key" -H "Content-Type: application/json" \
            --data "$body" > /dev/null || failures=$((failures + 1))
    else
        curl -sf $CURL_LIMITS -X POST "http://localhost:5055/api/v1/settings/$app" \
            -H "X-Api-Key: $seerr_key" -H "Content-Type: application/json" \
            --data "$body" > /dev/null || failures=$((failures + 1))
    fi
}

sync_seerr() {
    [[ -f "$LINKS_FILE" ]] || return 0
    local settings="$APPDATA_ROOT/seerr/settings.json"
    sudo test -f "$settings" || return 0
    local seerr_key
    seerr_key=$(sudo jq -r '.main.apiKey // empty' "$settings")
    [[ -n "$seerr_key" ]] || { echo "seerr: no apiKey yet, skipping" >&2; return 0; }

    sync_one_seerr_app radarr 7878 "$seerr_key"
    sync_one_seerr_app sonarr 8989 "$seerr_key"
    echo "seerr: radarr/sonarr links synced"
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

sync_seerr
sync_bazarr

[[ $failures -eq 0 ]] || exit 1
