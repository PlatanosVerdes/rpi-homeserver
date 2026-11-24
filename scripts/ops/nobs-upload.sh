#!/bin/bash
# Upload one film from the library to NOBS with RawLoadrr, by hand. See docs/nobs-upload.md.
#
# 1. Hardlinks it into downloads/uploads/<release>/, the one place qBittorrent can seed it from.
# 2. Refuses a file without a Castilian audio track: NOBS takes no film dubbed in Castilian without it.
# 3. Dry run: name, description, screenshots and the dupe check, nothing sent.
# 4. Asks, and only on "y" runs it again for real: upload, fetch the tracker's .torrent, seed it.
#
# Usage: scripts/ops/nobs-upload.sh "<library file>" "<NOBS name>" [rawloadrr args...]
#        CASTILIAN_OK=1 accepts a `spa` track whose title does not say Castellano, once heard.

set -euo pipefail

PROJECT_DIR="$HOME/rpi-homeserver"
MAX_LOAD="${MAX_LOAD:-4}"

cd "$PROJECT_DIR"
set -a; source .env; set +a
export COMPOSE_ENV_FILES=versions.env,.env

usage='usage: nobs-upload.sh "<library file>" "<NOBS name>" [rawloadrr args...]'
file="$(realpath -e "${1:?$usage}")"
name="${2:?$usage}"
shift 2
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

compose_run --entrypoint ffprobe rawloadrr -v error -select_streams a -show_entries stream=index,codec_name,channels:stream_tags=language,title -of json "$target" \
    | CASTILIAN_OK="${CASTILIAN_OK:-0}" python3 -c '
import json, os, re, sys
spa = json.load(open("scripts/sync/languages.json"))["languages"]["spa"]
rejected = re.compile(spa["rejected"] + "|comment", re.I)
preferred = re.compile(spa["preferred"], re.I)
tracks = json.load(sys.stdin)["streams"]
for t in tracks:
    tags = t.get("tags", {})
    print("  audio", t["index"], tags.get("language", "?"), t["codec_name"], t.get("channels", "?"), tags.get("title", ""))
spanish = [t.get("tags", {}).get("title", "") for t in tracks
           if t.get("tags", {}).get("language", "").lower() in spa["aliases"]
           and not rejected.search(t.get("tags", {}).get("title", ""))]
if any(preferred.search(title) for title in spanish):
    sys.exit(0)
if spanish and os.environ["CASTILIAN_OK"] == "1":
    print("  spa track not titled Castellano, accepted with CASTILIAN_OK=1")
    sys.exit(0)
sys.exit("no Castilian audio track: NOBS refuses a film dubbed in Castilian without one" if not spanish
         else "a spa track, but its title does not say Castellano: listen to it, then CASTILIAN_OK=1")
'

# --unattended: no prompt inside RawLoadrr; a possible dupe is skipped rather than asked about.
run() { compose_run rawloadrr "$target" --trackers NOBS --torrent-creation mktorrent --unattended --manual-title "$name" "$@"; }

run --debug "$@"
read -r -p "Upload \"$name\" to NOBS for real? [y/N] " answer
[[ "$answer" == y ]] || exit 0
run "$@"
seeded=1
echo "Check it in qBittorrent (category manual, tags upload and keep) and on the site."
