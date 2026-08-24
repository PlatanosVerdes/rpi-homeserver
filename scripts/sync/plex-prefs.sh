#!/bin/bash
# Tell Plex which networks count as local, so tailnet clients are not billed as remote, and where
# it marks a film as watched.
#
# Plex decides local from the connection's source IP, and its tailscale0 address is a /32, so every
# 100.x peer looks external and hits the remote-playback paywall. `allowedNetworks` does not help:
# that one only skips authentication. Why it cannot be fixed with a proxy instead:
# docs/plex-remote-access.md.
#
# The played threshold goes from 90% to 95% because Maintainerr deletes what Plex calls watched,
# and 90% is inside the credits: a film abandoned before the end would be queued for deletion.
#
# Both live only in Preferences.xml, which the container rewrites on shutdown, so they are pushed
# through Plex's own API and only when the running value differs.
set -uo pipefail

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
set -a; source "$PROJECT_DIR/.env"; set +a

PLEX_BASE="http://localhost:32400"
PREFS_FILE="$PROJECT_DIR/appdata/plex/Library/Application Support/Plex Media Server/Preferences.xml"
# Percentage of a film that counts as watched. Policy rather than host config, so it is not in .env.
PLAYED_THRESHOLD=95

if [[ -z "${PLEX_LAN_NETWORKS:-}" || -z "${PLEX_API_TOKEN:-}" ]]; then
    echo "sync-plex-prefs: PLEX_LAN_NETWORKS or PLEX_API_TOKEN not set in .env, skipping" >&2
    exit 0
fi

if ! curl -sf -m 10 -o /dev/null -H "X-Plex-Token: $PLEX_API_TOKEN" "$PLEX_BASE/identity"; then
    echo "sync-plex-prefs: could not reach Plex's API, skipping" >&2
    exit 0
fi

# Unlike LanNetworksBandwidth, this one is a normal preference and readable from the API.
played=$(curl -sf -m 10 -H "X-Plex-Token: $PLEX_API_TOKEN" "$PLEX_BASE/:/prefs" \
    | grep -oP 'id="LibraryVideoPlayedThreshold"[^>]*value="\K[0-9]+' || true)

if [[ "$played" != "$PLAYED_THRESHOLD" ]]; then
    status=$(curl -s -o /dev/null -w '%{http_code}' -m 10 -X PUT \
        -H "X-Plex-Token: $PLEX_API_TOKEN" \
        "$PLEX_BASE/:/prefs?LibraryVideoPlayedThreshold=$PLAYED_THRESHOLD")
    if [[ "$status" == 2* ]]; then
        echo "plex prefs: played threshold set to ${PLAYED_THRESHOLD}% (was '${played:-unknown}')"
    else
        echo "sync-plex-prefs: failed to write the played threshold (HTTP $status)" >&2
    fi
fi

# LanNetworksBandwidth is a hidden preference: it is absent from /:/prefs until it has a value,
# so the running value has to be read back from the file rather than from the API.
current=$(sudo grep -oP 'LanNetworksBandwidth="\K[^"]*' "$PREFS_FILE" 2>/dev/null || true)

if [[ "$current" == "$PLEX_LAN_NETWORKS" ]]; then
    echo "plex prefs: LAN networks already $PLEX_LAN_NETWORKS"
    exit 0
fi

encoded=$(jq -rn --arg v "$PLEX_LAN_NETWORKS" '$v|@uri')
status=$(curl -s -o /dev/null -w '%{http_code}' -m 10 -X PUT \
    -H "X-Plex-Token: $PLEX_API_TOKEN" \
    "$PLEX_BASE/:/prefs?LanNetworksBandwidth=$encoded")

if [[ "$status" != 2* ]]; then
    echo "sync-plex-prefs: failed to write LAN networks (HTTP $status)" >&2
    exit 0
fi

written=$(sudo grep -oP 'LanNetworksBandwidth="\K[^"]*' "$PREFS_FILE" 2>/dev/null || true)
if [[ "$written" == "$PLEX_LAN_NETWORKS" ]]; then
    echo "plex prefs: LAN networks set to $PLEX_LAN_NETWORKS (was '${current:-empty}')"
else
    echo "sync-plex-prefs: Plex accepted the write but the preference did not stick (got '${written:-empty}')" >&2
fi
