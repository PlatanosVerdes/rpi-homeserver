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

    # A profile has to score EVERY format this install holds, not just the committed ones. Radarr
    # silently discards the whole update when formatItems omits one of them: it answers 202, echoes
    # the body back, and changes nothing. Measured on 2026-08-21 with 13 formats live against 12 in
    # the repo, when neither the scores nor cutoffFormatScore moved and this script still reported
    # three profiles synced. Whatever the repo does not score is sent as 0.
    local all_formats
    all_formats=$(call GET "$base/customformat" "$key" | jq 'map({format: .id, name: .name})') || {
        failures=$((failures + 1)); return 1
    }

    # A formatName in the committed profile that does not exist here is dropped from the body
    # without a word, so a typo would look like a clean sync forever. Say it out loud instead.
    local unknown
    unknown=$(jq -r --argjson all "$all_formats" '
        ($all | map(.name)) as $known
        | [.[].formatItems[].formatName] | unique | map(select(. as $n | $known | index($n) | not))
        | join(", ")' "$qp_path")
    if [[ -n "$unknown" ]]; then
        echo "[$app] profiles name formats that do not exist here: $unknown" >&2
        failures=$((failures + 1))
    fi

    local qp_count=0 qp_applied=0
    while IFS= read -r qp; do
        qp_count=$((qp_count + 1))
        local name profile_id body live
        name=$(jq -r '.name' <<< "$qp")
        body=$(jq --argjson all "$all_formats" '
            . as $p
            | .formatItems = [
                $all[] as $f
                | $f + {score: ([$p.formatItems[] | select(.formatName == $f.name) | .score] | first // 0)}
              ]' <<< "$qp")

        profile_id=$(jq -r --arg n "$name" '.[$n] // empty' <<< "$existing_profiles")
        if [[ -n "$profile_id" ]]; then
            body=$(jq --argjson id "$profile_id" '. + {id: $id}' <<< "$body")
            call PUT "$base/qualityprofile/$profile_id" "$key" "$body" > /dev/null || {
                failures=$((failures + 1)); continue
            }
        else
            profile_id=$(call POST "$base/qualityprofile" "$key" "$body" | jq -r '.id') || {
                failures=$((failures + 1)); continue
            }
        fi

        # Read back instead of trusting the 202, the same reason sync-qbit-config.sh does it: this
        # API accepts changes it never makes, and a sync that claims success while doing nothing is
        # worse than one that fails loudly.
        live=$(call GET "$base/qualityprofile/$profile_id" "$key") || {
            failures=$((failures + 1)); continue
        }
        local drift
        drift=$(jq -rn --argjson want "$body" --argjson live "$live" '
            ($live.formatItems | map({(.name): .score}) | add // {}) as $live_scores
            | [$want | (.cutoffFormatScore // 0) as $c
               | select($c != ($live.cutoffFormatScore // 0)) | "cutoffFormatScore"]
              + [$want.formatItems[] | select($live_scores[.name] != .score) | .name]
            | join(", ")')
        if [[ -n "$drift" ]]; then
            echo "[$app] $name: did NOT stick -> $drift" >&2
            failures=$((failures + 1))
        else
            qp_applied=$((qp_applied + 1))
        fi
    done < <(jq -c '.[]' "$qp_path")

    echo "[$app] $cf_count custom formats, $qp_applied of $qp_count quality profiles applied"
}

# Radarr's QualityProfiles.Language column is a legacy field the current API/UI never reads or
# writes (confirmed: PUTting an explicit "language" value gets echoed back in the response but
# never reaches the database), yet the decision engine (LanguageSpecification.cs) still reads it
# directly and hard-rejects every release that doesn't match. NULL there is not the same as
# Language.Any (id -1) the way the API pretends when displaying it — it silently rejects
# everything regardless of language, defeating the whole point of the custom-format-based
# language scoring (Audio Espanol/VOSE/Ingles) configured above. Since the API can't write this
# column, go around it directly with a container that bind-mounts the appdata dir. A *live* write
# while Radarr keeps running does NOT stick — Radarr already has the old (null) value cached in
# memory from its own startup read and overwrites the file back to match it within moments,
# regardless of what the row says on disk in between. Only a write immediately followed by a
# restart survives, so Radarr loads the corrected value fresh instead of clobbering it.
# Sonarr has no Language column on this table at all, so this is Radarr-only.
fix_radarr_language() {
    local db="$APPDATA_ROOT/radarr/radarr.db"
    sudo test -f "$db" || return 0
    local changed
    changed=$(docker run --rm -v "$APPDATA_ROOT/radarr:/data" python:3-alpine python3 -c "
import sqlite3
conn = sqlite3.connect('/data/radarr.db', timeout=10)
cur = conn.execute('UPDATE QualityProfiles SET Language = -1 WHERE Language IS NULL OR Language != -1')
conn.commit()
print(cur.rowcount)
conn.close()
" 2>/dev/null) || { echo "[radarr] WARNING: could not fix QualityProfiles.Language" >&2; return 1; }
    [[ "$changed" =~ ^[0-9]+$ && "$changed" -gt 0 ]] || return 0  # already fixed, no restart needed
    docker restart radarr > /dev/null && echo "[radarr] QualityProfiles.Language was reset ($changed row(s)), fixed and restarted"
}

sync_app radarr 7878
fix_radarr_language
sync_app sonarr 8989

[[ $failures -eq 0 ]] || exit 1
