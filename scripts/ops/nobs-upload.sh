#!/bin/bash
# Upload one film from the library to NOBS with RawLoadrr, by hand. See docs/nobs-upload.md.
#
# 1. Hardlinks it into downloads/uploads/<release>/, the one place qBittorrent can seed it from.
# 2. nobs-check.py: refuses a file without a Castilian audio track, or one NOBS already has at the
#    same resolution (looked up by TMDB id).
# 3. Dry run: name, description, screenshots and the dupe check, nothing sent.
# 4. Asks, and only on "y" runs it again for real: upload, fetch the tracker's .torrent, seed it.
#
# Usage: scripts/ops/nobs-upload.sh "<library file>" "<NOBS name>" <tmdb id> [rawloadrr args...]
#        CASTILIAN_OK=1 and DUPE_OK=1: see nobs-check.py.

set -euo pipefail

PROJECT_DIR="$HOME/rpi-homeserver"
MAX_LOAD="${MAX_LOAD:-4}"

cd "$PROJECT_DIR"
set -a; source .env; set +a
export COMPOSE_ENV_FILES=versions.env,.env

usage='usage: nobs-upload.sh "<library file>" "<NOBS name>" <tmdb id> [rawloadrr args...]'
file="$(realpath -e "${1:?$usage}")"
name="${2:?$usage}"
tmdb="${3:?$usage}"
[[ "$tmdb" =~ ^[0-9]+$ ]] || { echo "the TMDB id is a number: $tmdb" >&2; exit 1; }
shift 3
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

# RawLoadrr names the film after the folder holding it, so each file gets one named like its release.
release="$(basename "${file%.*}")"
dir="$DATA_ROOT/downloads/uploads/$release"
link="$dir/$(basename "$file")"
mkdir -p "$dir" "$APPDATA/rawloadrr/tmp" "$APPDATA/rawloadrr/logs"
if [[ ! "$link" -ef "$file" ]]; then
    # Never a copy: a hardlink costs nothing, a copy is the whole film again on the disk.
    ln "$file" "$link" || { echo "cannot hardlink into $dir (another disk of the pool?)" >&2; exit 1; }
fi
seeded=0
trap '[[ $seeded -eq 1 ]] || { rm -f "$link"; rmdir "$dir" 2>/dev/null || true; }' EXIT

target="/data/downloads/uploads/$release/$(basename "$file")"
# stdin from /dev/null: ffmpeg reads stdin and would swallow the answer to the prompt below.
compose_run() { docker compose run --rm -T "$@" </dev/null; }

compose_run --entrypoint ffprobe rawloadrr -v error -show_entries stream=index,codec_type,codec_name,channels,width:stream_tags=language,title -of json "$target" \
    | python3 scripts/ops/nobs-check.py "$tmdb"

# --unattended: no prompt inside RawLoadrr. Its own dupe check judges by name similarity, hence nobs-check.py.
run() { compose_run rawloadrr "$target" --trackers NOBS --torrent-creation mktorrent --unattended --manual-title "$name" --tmdb "$tmdb" "$@"; }

run --debug "$@"
read -r -p "Upload \"$name\" to NOBS for real? [y/N] " answer
[[ "$answer" == y ]] || exit 0
run "$@"
seeded=1
echo "Check it in qBittorrent (category manual, tags upload and keep) and on the site."
