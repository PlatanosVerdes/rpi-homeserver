#!/usr/bin/env python3
"""Whether NOBS would take a file, before RawLoadrr touches it. Used by nobs-upload.sh.

Reads the file's ffprobe JSON on stdin and exits non-zero, saying why, when:
- it has no Castilian audio track (NOBS refuses a film dubbed in Castilian without one), or
- NOBS already has the same film (TMDB id) at the same resolution: a duplicate unless clearly
  better, and RawLoadrr's own check judges by name similarity and let one through.

Usage: nobs-check.py <tmdb id>   (ffprobe -show_streams -of json on stdin)
       CASTILIAN_OK=1  accept a `spa` track whose title does not say Castellano, once heard
       DUPE_OK=1       upload beside a same-resolution release, once you judge yours clearly better
"""
import json
import os
import re
import sys
import urllib.parse
import urllib.request
from pathlib import Path

LANGUAGES = Path(__file__).resolve().parent.parent / "sync" / "languages.json"
SEARCH = "https://nobs.rawsmoke.net/api/torrents/filter"


def resolution(width):
    for min_width, label in ((3800, "2160p"), (1900, "1080p"), (1260, "720p")):
        if width >= min_width:
            return label
    return "SD"


def castilian(audio):
    spa = json.loads(LANGUAGES.read_text())["languages"]["spa"]
    rejected = re.compile(spa["rejected"] + "|comment", re.I)
    preferred = re.compile(spa["preferred"], re.I)
    titles = [t.get("tags", {}).get("title", "") for t in audio
              if t.get("tags", {}).get("language", "").lower() in spa["aliases"]
              and not rejected.search(t.get("tags", {}).get("title", ""))]
    if any(preferred.search(title) for title in titles):
        return None
    if titles and os.environ.get("CASTILIAN_OK") == "1":
        print("  spa track not titled Castellano, accepted with CASTILIAN_OK=1")
        return None
    if not titles:
        return "no Castilian audio track: NOBS refuses a film dubbed in Castilian without one"
    return "a spa track, but its title does not say Castellano: listen to it, then CASTILIAN_OK=1"


def on_nobs(tmdb):
    query = urllib.parse.urlencode({"api_token": os.environ["NOBS_APIKEY"], "tmdbId": tmdb, "perPage": 100})
    request = urllib.request.Request(f"{SEARCH}?{query}", headers={"User-Agent": "nobs-upload"})
    with urllib.request.urlopen(request, timeout=30) as response:
        return [t["attributes"] for t in json.load(response)["data"]]


def main():
    tmdb = sys.argv[1]
    streams = json.load(sys.stdin)["streams"]
    audio = [s for s in streams if s.get("codec_type") == "audio"]
    video = next(s for s in streams if s.get("codec_type") == "video")
    for t in audio:
        tags = t.get("tags", {})
        print("  audio", t["index"], tags.get("language", "?"), t["codec_name"], t.get("channels", "?"), tags.get("title", ""))
    refusal = castilian(audio)
    if refusal:
        sys.exit(refusal)

    ours = resolution(video.get("width", 0))
    existing = on_nobs(tmdb)
    print(f"  this file: {ours}; on NOBS for TMDB {tmdb}: {len(existing)}")
    for t in existing:
        print(f"    {t['resolution']:>6} {t['type']:<8} {t['size'] / 2**30:6.2f} GiB  {t['name']}")
    if any(t["resolution"].lower() == ours.lower() for t in existing):
        if os.environ.get("DUPE_OK") == "1":
            print(f"  NOBS has it at {ours}, uploading anyway with DUPE_OK=1")
            return
        sys.exit(f"NOBS already has this film at {ours}: a duplicate unless clearly better (then DUPE_OK=1)")


if __name__ == "__main__":
    main()
