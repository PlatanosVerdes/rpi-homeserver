#!/bin/bash
# Upload one film from the library to NOBS with RawLoadrr, by hand. See docs/nobs-upload.md.
#
# 1. Hardlinks the file into downloads/uploads, the one place qBittorrent can seed it from.
# 2. Dry run (--debug): name, description, screenshots and the dupe check, nothing sent.
# 3. Asks, and only on "y" runs it again for real: upload, fetch the tracker's .torrent, seed it.
#
# Usage: scripts/ops/nobs-upload.sh "<library file>" [rawloadrr args...]

set -euo pipefail

PROJECT_DIR="$HOME/rpi-homeserver"
MAX_LOAD="${MAX_LOAD:-4}"

cd "$PROJECT_DIR"
set -a; source .env; set +a
export COMPOSE_ENV_FILES=versions.env,.env

file="$(realpath -e "${1:?usage: nobs-upload.sh <library file> [rawloadrr args...]}")"
shift
case "$file" in
    "$DATA_ROOT"/films/*|"$DATA_ROOT"/series/*) ;;
    *) echo "not a library file under $DATA_ROOT/films or series: $file" >&2; exit 1 ;;
esac
[[ -n "${TMDB_API_KEY:-}" && -n "${NOBS_APIKEY:-}" ]] || { echo "TMDB_API_KEY and NOBS_APIKEY must be in .env" >&2; exit 1; }

load=$(cut -d' ' -f2 /proc/loadavg)
if awk -v l="$load" -v m="$MAX_LOAD" 'BEGIN { exit !(l >= m) }'; then
    echo "5-minute load is $load (limit $MAX_LOAD): try later" >&2
    exit 1
fi

APPDATA="${APP_CONFIG_PATH:-./appdata}"
[[ "$APPDATA" != /* ]] && APPDATA="$PROJECT_DIR/${APPDATA#./}"

# The conversion lock: never beside a re-encode or an add-audio run.
mkdir -p "$APPDATA/audio-manager"
exec 9>"$APPDATA/audio-manager/convert.lock"
flock -n 9 || { echo "an audio conversion is running: try later" >&2; exit 1; }

uploads="$DATA_ROOT/downloads/uploads"
mkdir -p "$uploads" "$APPDATA/rawloadrr/tmp" "$APPDATA/rawloadrr/logs"
link="$uploads/$(basename "$file")"
if [[ ! "$link" -ef "$file" ]]; then
    # Never a copy: a hardlink costs nothing, a copy is the whole film again on the disk.
    ln "$file" "$link" || { echo "cannot hardlink into $uploads (another disk of the pool?)" >&2; exit 1; }
fi
seeded=0
trap '[[ $seeded -eq 1 ]] || rm -f "$link"' EXIT

target="/data/downloads/uploads/$(basename "$file")"
run() { docker compose run --rm rawloadrr "$target" --trackers NOBS --torrent-creation mktorrent "$@"; }

run --debug "$@"
read -r -p "Upload to NOBS for real? [y/N] " answer
[[ "$answer" == y ]] || exit 0
run "$@"
seeded=1
echo "Check it in qBittorrent (category manual, tags upload and keep) and on the site."
