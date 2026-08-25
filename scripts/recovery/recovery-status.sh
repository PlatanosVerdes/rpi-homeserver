#!/bin/bash
# Report what a rebuilt Pi still needs a human for, after scripts/deploy/apply.sh has already converged
# everything that is derivable from .env (Radarr/Sonarr formats and profiles, Pi-hole DNS, the
# Seerr/Bazarr links). Run this after a rebuild to see exactly what is left, instead of
# rediscovering it by clicking through each app.
#
# These three cannot be scripted away: each is a one-time interactive step with an external
# provider (a claim token, a setup wizard, an Apple 2FA code) that no API call can substitute for.
set -uo pipefail

APPDATA_ROOT="/home/raspi/rpi-homeserver/appdata"

check() {
    local label=$1 ok=$2 detail=$3
    if [[ "$ok" == "1" ]]; then
        printf '  \xe2\x9c\x93 %-10s %s\n' "$label" "$detail"
    else
        printf '  \xe2\x9a\xa0 %-10s %s\n' "$label" "$detail"
    fi
}

echo "Auto-synced from git on every deploy (nothing to check by hand):"
echo "  arr custom formats + quality profiles, Pi-hole DNS, Seerr/Bazarr links"
echo
echo "Needs a human, at least once per rebuild:"

# Grafana reads GF_DATABASE_WAL but 13.1.2's pure-Go sqlite driver did not convert an existing
# database with it, so a rebuild is the moment to confirm the new one came up in WAL. Without it
# an alert rule that fails to evaluate says nothing: execErrState is KeepLast everywhere.
gf_db="$APPDATA_ROOT/grafana/data/grafana.db"
if sudo test -f "$gf_db"; then
    mode=$(sudo python3 -c "import sqlite3;print(sqlite3.connect('file:$gf_db?mode=ro',uri=True).execute('pragma journal_mode').fetchone()[0])" 2>/dev/null)
    if [[ "$mode" == "wal" ]]; then
        check Grafana 1 "database in WAL"
    else
        check Grafana 0 "database in '$mode', not wal — see compose-mon.yml for the one-liner"
    fi
fi


plex_prefs="$APPDATA_ROOT/plex/Library/Application Support/Plex Media Server/Preferences.xml"
if sudo test -f "$plex_prefs" && sudo grep -q 'PlexOnlineToken="' "$plex_prefs"; then
    check Plex 1 "claimed"
else
    check Plex 0 "not claimed — claim at https://plex.tv/claim, then set it in Plex's own setup"
fi

jf_status=$(curl -sf http://localhost:8096/System/Info/Public 2>/dev/null | jq -r '.StartupWizardCompleted // false')
if [[ "$jf_status" == "true" ]]; then
    check Jellyfin 1 "setup wizard completed"
else
    check Jellyfin 0 "setup wizard not completed — open Jellyfin and create the admin user + libraries"
fi

airtag_account="/home/raspi/rpi-services/appdata/air-tag/account.json"
if sudo test -s "$airtag_account"; then
    check air-tag 1 "Apple session present"
else
    check air-tag 0 "no Apple session — log in through air-tag once (APPLE_ID/APPLE_PASSWORD come from .env, but Apple may ask for a 2FA code)"
fi
