#!/usr/bin/env python3
"""Films in the library NOBS would take, for when its ratio needs upload. See docs/nobs-upload.md.

Reads the tracks audio-manager already probed (appdata/audio-manager/add-audio/films.json), so no
file is opened. A candidate has a Castilian audio track, is not an upscale, and NOBS has nothing at
its resolution. Each one is printed with the nobs-upload.sh line to start from.

Usage: scripts/ops/nobs-candidates.py
"""
import importlib.util
import json
import os
import re
import urllib.parse
import urllib.request
from pathlib import Path

PROJECT_DIR = Path(__file__).resolve().parent.parent.parent
SEARCH = "https://nobs.rawsmoke.net/api/torrents/filter"
TMDB_FIND = "https://api.themoviedb.org/3/find/{}"

spec = importlib.util.spec_from_file_location("nobs_check", Path(__file__).with_name("nobs-check.py"))
nobs_check = importlib.util.module_from_spec(spec)
spec.loader.exec_module(nobs_check)


def env():
    values = {}
    for line in (PROJECT_DIR / ".env").read_text().splitlines():
        key, sep, value = line.partition("=")
        if sep and not key.lstrip().startswith("#"):
            values[key.strip()] = value.strip().strip('"').strip("'")
    return values


def get(url, params):
    request = urllib.request.Request(f"{url}?{urllib.parse.urlencode(params)}", headers={"User-Agent": "nobs-candidates"})
    with urllib.request.urlopen(request, timeout=30) as response:
        return json.load(response)


def resolution(path):
    match = re.search(r"\b(2160p|1080p|720p|4K|UHD)\b", path, re.I)
    if not match:
        return None
    return "2160p" if match.group(1).upper() in ("4K", "UHD") else match.group(1).lower()


def main():
    config = env()
    os.environ["CASTILIAN_OK"] = "0"
    data_root = config.get("DATA_ROOT", "/mnt/data")
    appdata = Path(config.get("APP_CONFIG_PATH", "./appdata"))
    if not appdata.is_absolute():
        appdata = PROJECT_DIR / appdata
    films = json.loads((appdata / "audio-manager/add-audio/films.json").read_text())["films"]

    candidates, unsure = [], []
    for film in films:
        audio = (film.get("file") or {}).get("audio") or []
        path = film["path"].replace("/data/", f"{data_root}/", 1)
        refusal = nobs_check.castilian(audio)
        if re.search(r"upscale", path, re.I) or (refusal and refusal.startswith("no Castilian")):
            continue
        ours = resolution(path)
        existing = [t["attributes"] for t in get(SEARCH, {
            "api_token": config["NOBS_APIKEY"], "imdbId": film["imdb"].removeprefix("tt"), "perPage": 100})["data"]]
        if ours and any(t["resolution"].lower() == ours for t in existing):
            continue
        tmdb = next((t["tmdb_id"] for t in existing if t.get("tmdb_id")), None)
        if not tmdb:
            found = get(TMDB_FIND.format(film["imdb"]), {"api_key": config["TMDB_API_KEY"], "external_source": "imdb_id"})
            tmdb = (found.get("movie_results") or [{}])[0].get("id", "<tmdb id>")
        on_nobs = ", ".join(sorted({f"{t['resolution']} {t['type']}" for t in existing})) or "nothing"
        entry = (f"{film['title']} ({film['year']}), {ours or 'resolution unknown'}; NOBS has: {on_nobs}\n"
                 f'  scripts/ops/nobs-upload.sh "{path}" "<NOBS name>" {tmdb}')
        (unsure if refusal else candidates).append(entry)

    print(f"Candidates ({len(candidates)}):")
    for entry in candidates:
        print(entry)
    if unsure:
        print(f"\nWith a spa track not titled Castellano, listen first, then CASTILIAN_OK=1 ({len(unsure)}):")
        for entry in unsure:
            print(entry)
    print("\nBefore each one: the source group must not be another private site's internal, and the "
          "name is the Spanish title in NOBS's format (docs/nobs-upload.md).")


if __name__ == "__main__":
    main()
