#!/bin/bash
# Point Jellyfin's XMLTV guide at the guide.xml bifrost writes next to the playlist.
#
# The published guide is 34 MB for 642 channels, and ours are about 23 of them: parsing all of it is
# what made a Refresh Guide cost the Pi 4 two and a half minutes. bifrost cuts it down, and this
# keeps Jellyfin reading the cut-down copy. The provider is updated in place, keeping its id, so the
# channel-to-guide mappings Jellyfin already holds survive.
set -uo pipefail

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
set -a; source "$PROJECT_DIR/.env"; set +a

GUIDE_PATH="/config/acestream/guide.xml"
GUIDE_FILE="$PROJECT_DIR/${APP_CONFIG_PATH#./}/jellyfin/acestream/guide.xml"
JELLYFIN="http://localhost:8096"

if [[ -z "${JELLYFIN_API_KEY:-}" ]]; then
    echo "sync-jellyfin-guide: JELLYFIN_API_KEY not set in .env, skipping" >&2
    exit 0
fi
# Pointing Jellyfin at a file that is not there yet would leave it with no guide at all.
if [[ ! -s "$GUIDE_FILE" ]]; then
    echo "jellyfin guide: $GUIDE_FILE not written yet, leaving the guide as it is"
    exit 0
fi

config=$(docker exec jellyfin curl -sf -m 15 -H "X-Emby-Token: $JELLYFIN_API_KEY" \
    "$JELLYFIN/System/Configuration/livetv" 2>/dev/null)
if [[ -z "$config" ]]; then
    echo "sync-jellyfin-guide: could not reach Jellyfin's API, skipping" >&2
    exit 0
fi

provider=$(jq -c '[.ListingProviders[]? | select(.Type == "xmltv")][0] // empty' <<<"$config")
if [[ -n "$provider" && "$(jq -r '.Path' <<<"$provider")" == "$GUIDE_PATH" ]]; then
    echo "jellyfin guide: already reading $GUIDE_PATH"
    exit 0
fi

if [[ -n "$provider" ]]; then
    was=$(jq -r '.Path' <<<"$provider")
    body=$(jq -c --arg p "$GUIDE_PATH" '.Path = $p' <<<"$provider")
else
    was="no XMLTV provider"
    body=$(jq -cn --arg p "$GUIDE_PATH" '{Type: "xmltv", Path: $p, EnableAllTuners: true}')
fi

status=$(docker exec -i jellyfin curl -s -o /dev/null -w '%{http_code}' -m 30 -X POST \
    -H "X-Emby-Token: $JELLYFIN_API_KEY" -H "Content-Type: application/json" --data-binary @- \
    "$JELLYFIN/LiveTv/ListingProviders?validateListings=false&validateLogin=false" <<<"$body")
if [[ "$status" == 2* ]]; then
    echo "jellyfin guide: now reading $GUIDE_PATH (was $was)"
else
    echo "sync-jellyfin-guide: failed to update the XMLTV provider (HTTP $status)" >&2
fi
