#!/usr/bin/env python3
"""Drop Bazarr's embedded-subtitle rows that describe a file it no longer has.

Bazarr indexes the subtitle tracks inside every media file into table_movies_subtitles, and treats
them as satisfying the language profile. It never deletes those rows when Radarr or Sonarr replace
the file, so each upgrade leaves the previous release's tracks behind and Bazarr keeps believing in
languages the current file does not carry. A language it thinks it already has is never searched
for, and nothing in the UI shows why.

Its own daily "Index All Existing Movies Subtitles" does not help: it only inserts. Project Hail
Mary was imported as GAZPROM (English, French, Dutch, Spanish), replaced two days later by a MULTi
release with no Spanish at all, and kept both sets of rows, so the Spanish subtitles were never
downloaded.

Only rows the file disproves are deleted: the track index is gone, or the track sitting at that
index is a different language. A track with no language tag, a tag this Bazarr does not know, or
one of the code pairs below leaves the row alone, because a surviving stale row only delays a
download while a wrongly deleted one loses a subtitle Bazarr already has.

Run from cron rather than from apply.sh: a full pass is ~55 s of ffprobe over every media file,
which has no business inside the deploy lock, and the rows only go stale when an upgrade lands.
Scheduled after Bazarr's own 04:00 index, so it cleans up what that pass has just re-inserted.
Silent unless something happened.
"""

import json
import os
import sqlite3
import subprocess
import sys
from collections import namedtuple
from pathlib import Path

PROJECT_DIR = Path(os.path.expanduser("~/rpi-homeserver"))
BAZARR_DB = PROJECT_DIR / "appdata" / "bazarr" / "db" / "bazarr.db"
BAZARR_CONFIG = PROJECT_DIR / "appdata" / "bazarr" / "config" / "config.yaml"
CONTAINER_DATA_ROOT = "/data/"
DATA_ROOT = os.environ.get("DATA_ROOT", "/mnt/data")
FFPROBE_TIMEOUT = 30
SQLITE_TIMEOUT = 30

# Bazarr codes with no tag of their own in a container: a track tagged spa is stored as es or as ea
# depending on what the track name says, so neither one disproves the other.
EQUIVALENT = ({"es", "ea"}, {"pt", "pb"}, {"zh", "zt"}, {"no", "nb", "nn"})

# key joins a subtitle row to its item; scan_key is what Bazarr's own rescan takes, and for an
# episode that is the series it belongs to.
Target = namedtuple("Target", "kind item_table subs_table key scan_key endpoint param")
TARGETS = (
    Target("movies", "table_movies", "table_movies_subtitles",
           "radarrId", "radarrId", "movies", "radarrid"),
    Target("episodes", "table_episodes", "table_episodes_subtitles",
           "sonarrEpisodeId", "sonarrSeriesId", "series", "seriesid"),
)


def language_map(con):
    """code3 and its bibliographic variant -> the code2 Bazarr stores, from Bazarr's own table."""
    codes = {}
    for code3, code3b, code2 in con.execute(
            "SELECT code3, code3b, code2 FROM table_settings_languages"):
        codes[code3] = code2
        if code3b:
            codes[code3b] = code2
    return codes


def host_path(container_path):
    return container_path.replace(CONTAINER_DATA_ROOT, f"{DATA_ROOT.rstrip('/')}/", 1)


def real_tracks(path):
    """{stream index: language tag or None} for the subtitle streams the file actually has."""
    probe = subprocess.run(
        ["ffprobe", "-v", "error", "-select_streams", "s",
         "-show_entries", "stream=index:stream_tags=language", "-of", "json", path],
        capture_output=True, text=True, timeout=FFPROBE_TIMEOUT, check=True)
    return {s["index"]: (s.get("tags") or {}).get("language")
            for s in json.loads(probe.stdout).get("streams", [])}


def is_stale(row_language, track_index, tracks, codes):
    if track_index not in tracks:
        return True
    tag = tracks[track_index]
    if not tag or tag == "und":
        return False
    stored = codes.get(tag)
    if stored is None or stored == row_language:
        return False
    return not any({stored, row_language} <= group for group in EQUIVALENT)


def survey(con, target, codes):
    """Row ids the file disproves, the items to have Bazarr rescan, and names for the log."""
    rows = con.execute(
        f"SELECT s.id, s.language, s.embedded_track_id, i.{target.scan_key}, i.path "
        f"FROM {target.subs_table} s JOIN {target.item_table} i ON i.{target.key} = s.{target.key} "
        f"WHERE s.path IS NULL AND s.embedded_track_id IS NOT NULL AND i.path IS NOT NULL")
    by_item = {}
    for row_id, language, track_index, scan_id, path in rows:
        by_item.setdefault((scan_id, path), []).append((row_id, language, track_index))

    doomed, rescans, names, failed = [], set(), [], 0
    for (scan_id, path), item_rows in by_item.items():
        try:
            tracks = real_tracks(host_path(path))
        except FileNotFoundError:
            continue                       # gone or unmounted: not our business to guess
        except (subprocess.CalledProcessError, subprocess.TimeoutExpired, ValueError) as exc:
            print(f"cannot probe {Path(path).name}: {type(exc).__name__}", file=sys.stderr)
            failed += 1
            continue
        stale = [row_id for row_id, language, index in item_rows
                 if is_stale(language, index, tracks, codes)]
        if stale:
            doomed.extend(stale)
            rescans.add(scan_id)
            names.append(Path(path).parent.name if target.kind == "movies" else Path(path).stem)
    return doomed, rescans, sorted(names), failed


def delete(plan):
    con = sqlite3.connect(BAZARR_DB, timeout=SQLITE_TIMEOUT)
    try:
        with con:
            for target, (doomed, _, _, _) in plan.items():
                placeholders = ",".join("?" * len(doomed))
                con.execute(f"DELETE FROM {target.subs_table} WHERE id IN ({placeholders})", doomed)
    finally:
        con.close()


def rescan(plan):
    """Ask Bazarr to re-read the items it just lost rows for, so missing_subtitles catches up."""
    try:
        import yaml
        key = yaml.safe_load(BAZARR_CONFIG.read_text())["auth"]["apikey"]
    except (OSError, KeyError, ValueError, ImportError) as exc:
        print(f"no bazarr apikey ({exc}), leaving the rescan to its 04:00 index", file=sys.stderr)
        return
    for target, (_, rescans, _, _) in plan.items():
        for scan_id in sorted(rescans):
            # Bazarr publishes no port; reach it through a container already on media-network.
            result = subprocess.run(
                ["timeout", "45", "docker", "exec", "maintainerr", "curl", "-sf",
                 "--connect-timeout", "5", "--max-time", "30", "-X", "PATCH",
                 "-H", f"X-API-KEY: {key}", "--data-urlencode", f"{target.param}={scan_id}",
                 "--data-urlencode", "action=scan-disk",
                 f"http://bazarr:6767/api/{target.endpoint}"],
                capture_output=True, text=True)
            if result.returncode != 0:
                print(f"{target.endpoint} {scan_id} would not rescan, "
                      f"leaving it to the 04:00 index", file=sys.stderr)


def main():
    dry_run = "--dry-run" in sys.argv
    if not BAZARR_DB.exists():
        return 0

    con = sqlite3.connect(f"file:{BAZARR_DB}?mode=ro", uri=True, timeout=SQLITE_TIMEOUT)
    try:
        codes = language_map(con)
        surveys = {target: survey(con, target, codes) for target in TARGETS}
    finally:
        con.close()

    failed = sum(s[3] for s in surveys.values())
    plan = {target: s for target, s in surveys.items() if s[0]}
    for target, (doomed, _, names, _) in plan.items():
        print(f"{target.kind}: {'would drop' if dry_run else 'dropped'} {len(doomed)} stale rows "
              f"from {len(names)}: {', '.join(names)}")

    if plan and not dry_run:
        try:
            delete(plan)
        except sqlite3.Error as exc:
            print(f"delete failed: {exc}", file=sys.stderr)
            return 1
        rescan(plan)
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
