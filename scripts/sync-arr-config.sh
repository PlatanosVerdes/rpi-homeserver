#!/bin/bash
# Converge Radarr/Sonarr custom formats and quality profiles to config/arr/<app>/*.json.
#
# These were built by hand through each app's own UI (language scoring, the 4K/good/wasteful/
# unwanted split, which qualities each profile allows) and live only in that app's own database
# under appdata/ -- nothing in a compose file or .env captures them, so losing appdata silently
# loses hours of tuning. Matched by name, so re-running this on every deploy is a no-op unless
# the committed JSON changed.
#
# Custom formats must sync first: quality profiles score them by name, and the id a format gets
# on THIS install is only known after it exists here.
set -uo pipefail

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APPDATA_ROOT="/home/raspi/rpi-homeserver/appdata"

failures=0

# call METHOD URL [API_KEY] [JSON_BODY] -> prints the response body, returns 1 on a non-2xx status
call() {
    local method=$1 url=$2 key=$3 body=${4:-}
    local out status body_out
    if [[ -n "$body" ]]; then
        out=$(curl -s -w '\n%{http_code}' -X "$method" "$url" \
            -H "X-Api-Key: $key" -H "Content-Type: application/json" --data "$body")
    else
        out=$(curl -s -w '\n%{http_code}' -X "$method" "$url" -H "X-Api-Key: $key")
    fi
    status=${out##*$'\n'}
    body_out=${out%$'\n'"$status"}
    if [[ "$status" != 2* ]]; then
        echo "$method $url -> $status: ${body_out:0:300}" >&2
        return 1
    fi
    printf '%s' "$body_out"
}

sync_app() {
    local app=$1 port=$2
    local conf_dir="$PROJECT_DIR/config/arr/$app"
    local cf_path="$conf_dir/custom-formats.json" qp_path="$conf_dir/quality-profiles.json"
    [[ -f "$cf_path" ]] || return 0

    local conf="$APPDATA_ROOT/$app/config.xml"
    sudo test -f "$conf" || return 0  # not running here (e.g. a secondary Pi)
    local key
    key=$(sudo grep -oP '(?<=<ApiKey>)[^<]+' "$conf")
    local base="http://localhost:$port/api/v3"

    # name -> id for every custom format that already exists on this install
    local name_to_id
    name_to_id=$(call GET "$base/customformat" "$key" | jq 'map({(.name): .id}) | add // {}') || {
        failures=$((failures + 1)); return 1
    }

    local cf_count=0
    while IFS= read -r cf; do
        cf_count=$((cf_count + 1))
        local name existing_id resp new_id
        name=$(jq -r '.name' <<< "$cf")
        existing_id=$(jq -r --arg n "$name" '.[$n] // empty' <<< "$name_to_id")
        if [[ -n "$existing_id" ]]; then
            local put_body
            put_body=$(jq --argjson id "$existing_id" '. + {id: $id}' <<< "$cf")
            call PUT "$base/customformat/$existing_id" "$key" "$put_body" > /dev/null || {
                failures=$((failures + 1)); continue
            }
        else
            resp=$(call POST "$base/customformat" "$key" "$cf") || {
                failures=$((failures + 1)); continue
            }
            new_id=$(jq -r '.id' <<< "$resp")
            name_to_id=$(jq --arg n "$name" --argjson i "$new_id" '.[$n] = $i' <<< "$name_to_id")
        fi
    done < <(jq -c '.[]' "$cf_path")

    # name -> id for every quality profile that already exists on this install
    local existing_profiles
    existing_profiles=$(call GET "$base/qualityprofile" "$key" | jq 'map({(.name): .id}) | add // {}') || {
        failures=$((failures + 1)); return 1
    }

    local qp_count=0
    while IFS= read -r qp; do
        qp_count=$((qp_count + 1))
        local name profile_id body
        name=$(jq -r '.name' <<< "$qp")
        body=$(jq --argjson namemap "$name_to_id" \
            '.formatItems = [.formatItems[] | {format: $namemap[.formatName], name: .formatName, score}]' \
            <<< "$qp")
        profile_id=$(jq -r --arg n "$name" '.[$n] // empty' <<< "$existing_profiles")
        if [[ -n "$profile_id" ]]; then
            body=$(jq --argjson id "$profile_id" '. + {id: $id}' <<< "$body")
            call PUT "$base/qualityprofile/$profile_id" "$key" "$body" > /dev/null || failures=$((failures + 1))
        else
            call POST "$base/qualityprofile" "$key" "$body" > /dev/null || failures=$((failures + 1))
        fi
    done < <(jq -c '.[]' "$qp_path")

    echo "[$app] $cf_count custom formats, $qp_count quality profiles synced"
}

# Radarr's QualityProfiles.Language column is a legacy field the current API/UI never reads or
# writes (confirmed: PUTting an explicit "language" value gets echoed back in the response but
# never reaches the database), yet the decision engine (LanguageSpecification.cs) still reads it
# directly and hard-rejects every release that doesn't match. NULL there is not the same as
# Language.Any (id -1) the way the API pretends when displaying it — it silently rejects
# everything regardless of language, defeating the whole point of the custom-format-based
# language scoring (Audio Espanol/VOSE/Ingles) configured above. Since the API can't write this
# column, go around it directly; a container with the appdata dir bind-mounted can update the
# live sqlite file's row without stopping Radarr (verified safe: normal SQLite file locking).
# Sonarr has no Language column on this table at all, so this is Radarr-only.
fix_radarr_language() {
    local db="$APPDATA_ROOT/radarr/radarr.db"
    sudo test -f "$db" || return 0
    docker run --rm -v "$APPDATA_ROOT/radarr:/data" python:3-alpine python3 -c "
import sqlite3
conn = sqlite3.connect('/data/radarr.db', timeout=10)
conn.execute('UPDATE QualityProfiles SET Language = -1 WHERE Language IS NULL OR Language != -1')
conn.commit()
conn.close()
" > /dev/null 2>&1 || echo "[radarr] WARNING: could not fix QualityProfiles.Language" >&2
}

sync_app radarr 7878
fix_radarr_language
sync_app sonarr 8989

[[ $failures -eq 0 ]] || exit 1
