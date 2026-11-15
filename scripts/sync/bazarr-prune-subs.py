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

A file can also lie about its own tracks. Little Miss Sunshine came from YTS with 98 subtitle
tracks whose tags were shifted: the one tagged Latin American Spanish was Icelandic, so Bazarr saw
Spanish and never searched. Deleting rows does not help there, since a rescan reads the same tags
back. Instead, the text tracks tagged with a profile language are sampled (their first ten minutes)
and run through Bazarr's own language detector. When one reads as another language, no tag in that
file is believed: every profile language not backed by an external subtitle or a text track that
reads right is asked of Bazarr directly. Bitmap tracks (PGS, VobSub) cannot be read, so they only
count in a file whose text tracks all check out. Verdicts are kept per file in the state directory,
so each file is sampled once.

Run from cron rather than from apply.sh: a full pass is ~55 s of ffprobe over every media file,
which has no business inside the deploy lock, and the rows only go stale when an upgrade lands.
Scheduled after Bazarr's own 04:00 index, so it cleans up what that pass has just re-inserted.
Silent unless something happened.
"""

import json
import os
import re
import sqlite3
import subprocess
import sys
import tempfile
from collections import namedtuple
from pathlib import Path

PROJECT_DIR = Path(os.path.expanduser("~/rpi-homeserver"))
BAZARR_DB = PROJECT_DIR / "appdata" / "bazarr" / "db" / "bazarr.db"
BAZARR_CONFIG = PROJECT_DIR / "appdata" / "bazarr" / "config" / "config.yaml"
STATE_DIR = PROJECT_DIR / "appdata" / "bazarr-prune-subs"
VERDICTS = STATE_DIR / "track-languages.json"
CONTAINER_DATA_ROOT = "/data/"
DATA_ROOT = os.environ.get("DATA_ROOT", "/mnt/data")
FFPROBE_TIMEOUT = 30
FFMPEG_TIMEOUT = 180
SQLITE_TIMEOUT = 30

TEXT_CODECS = {"subrip", "ass", "ssa", "mov_text", "webvtt", "text"}
# The first ten minutes are read in seconds; the whole track means reading the whole file.
SAMPLE_SECONDS = 600
MIN_SAMPLE_LETTERS = 1000

# Runs inside the bazarr container, which ships guess_language: {index: text} in, {index: code} out.
DETECT = """
import json, sys
sys.path.insert(0, "/app/bazarr/bin/libs")
from guess_language import guess_language
print(json.dumps({k: guess_language(v) for k, v in json.load(sys.stdin).items()}))
"""

# Bazarr codes with no tag of their own in a container: a track tagged spa is stored as es or as ea
# depending on what the track name says, so neither one disproves the other.
EQUIVALENT = ({"es", "ea"}, {"pt", "pb"}, {"zh", "zt"}, {"no", "nb", "nn"})

# key joins a subtitle row to its item; scan_key is what Bazarr's own rescan takes, and for an
# episode that is the series it belongs to.
# profile_sql yields (key, scan_key, path, profileId) for every item.
Target = namedtuple("Target",
                    "kind item_table subs_table key scan_key endpoint param profile_sql")
TARGETS = (
    Target("movies", "table_movies", "table_movies_subtitles",
           "radarrId", "radarrId", "movies", "radarrid",
           "SELECT radarrId, radarrId, path, profileId FROM table_movies"),
    Target("episodes", "table_episodes", "table_episodes_subtitles",
           "sonarrEpisodeId", "sonarrSeriesId", "series", "seriesid",
           "SELECT e.sonarrEpisodeId, e.sonarrSeriesId, e.path, s.profileId "
           "FROM table_episodes e JOIN table_shows s ON s.sonarrSeriesId = e.sonarrSeriesId"),
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


def probe(path):
    """{stream index: (language tag or None, codec)} for the subtitle streams the file has."""
    result = subprocess.run(
        ["ffprobe", "-v", "error", "-select_streams", "s",
         "-show_entries", "stream=index,codec_name:stream_tags=language", "-of", "json", path],
        capture_output=True, text=True, timeout=FFPROBE_TIMEOUT, check=True)
    return {s["index"]: ((s.get("tags") or {}).get("language"), s.get("codec_name"))
            for s in json.loads(result.stdout).get("streams", [])}


def real_tracks(path):
    return {index: tag for index, (tag, _) in probe(path).items()}


def same_language(a, b):
    return a == b or any({a, b} <= group for group in EQUIVALENT)


def is_stale(row_language, track_index, tracks, codes):
    if track_index not in tracks:
        return True
    tag = tracks[track_index]
    if not tag or tag == "und":
        return False
    stored = codes.get(tag)
    if stored is None:
        return False
    return not same_language(stored, row_language)


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
            names.append(display_name(target, path))
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


def profiles(con):
    """profileId -> [(code2, hi, forced)], the languages each Bazarr profile wants."""
    wanted = {}
    for profile_id, items in con.execute("SELECT profileId, items FROM table_languages_profiles"):
        wanted[profile_id] = [(item["language"], item.get("hi") == "True",
                               item.get("forced") == "True") for item in json.loads(items)]
    return wanted


ASS_DRAWING = re.compile(r"^[\s\d.,mnlbspc-]*$")


def plain(srt):
    """The dialogue alone: no cue numbers, timings, styling, ASS vector drawings ("m 561 454 l
    556 ..."), or the same sign repeated once per animation frame, all of which fool a detector."""
    lines = (re.sub(r"<[^>]+>|\{[^}]*\}", " ", line).strip() for line in srt.splitlines()
             if "-->" not in line)
    return "\n".join(dict.fromkeys(line for line in lines if not ASS_DRAWING.match(line)))


def sample_text(path, indexes):
    """{index: plain text of the track's first minutes}, all tracks in one ffmpeg pass."""
    with tempfile.TemporaryDirectory() as tmp:
        args = ["ffmpeg", "-nostdin", "-v", "error", "-y", "-i", path]
        for index in indexes:
            args += ["-map", f"0:{index}", "-t", str(SAMPLE_SECONDS), "-f", "srt", f"{tmp}/{index}.srt"]
        subprocess.run(args, capture_output=True, timeout=FFMPEG_TIMEOUT, check=True)
        return {index: plain(Path(tmp, f"{index}.srt").read_text(errors="replace"))
                for index in indexes}


def detect(texts):
    """{index: code2 or None}; None when the sample is too short or the detector cannot tell."""
    readable = {str(index): text[:4000] for index, text in texts.items()
                if sum(c.isalpha() for c in text) >= MIN_SAMPLE_LETTERS}
    found = {index: None for index in texts}
    if not readable:
        return found
    result = subprocess.run(["docker", "exec", "-i", "bazarr", "python3", "-c", DETECT],
                            input=json.dumps(readable), capture_output=True, text=True,
                            timeout=60, check=True)
    for index, code in json.loads(result.stdout).items():
        code = (code or "").split("_")[0].lower()
        found[int(index)] = code if len(code) == 2 and code.isalpha() else None
    return found


def track_reads(path, codes, languages, verdicts, seen):
    """{index: {"tag": code2, "read": code2 or None}} for the text tracks tagged with a profile
    language. Sampled once per file and profile; verdicts holds the answers between runs."""
    stat = os.stat(path)
    stamp = [stat.st_size, stat.st_mtime_ns, sorted(languages)]
    cached = verdicts.get(path)
    if cached and cached["stamp"] == stamp:
        seen[path] = cached
        return {int(index): read for index, read in cached["tracks"].items()}

    tagged = {index: codes[tag] for index, (tag, codec) in probe(path).items()
              if codec in TEXT_CODECS and tag in codes
              and any(same_language(codes[tag], language) for language in languages)}
    reads = detect(sample_text(path, tagged)) if tagged else {}
    tracks = {index: {"tag": tag, "read": reads.get(index)} for index, tag in tagged.items()}
    seen[path] = {"stamp": stamp, "tracks": tracks}
    return tracks


def mislabelled(con, target, codes, wanted, verdicts, seen):
    """Items whose own text tracks contradict their tags, with the profile languages nothing
    trustworthy covers: [(key, scan_key, name, [(code2, hi, forced)])], and probe failures."""
    embedded, external = {}, {}
    for key, language, track, path in con.execute(
            f"SELECT {target.key}, language, embedded_track_id, path FROM {target.subs_table}"):
        if path:
            external.setdefault(key, set()).add(language)
        elif track is not None:
            embedded.setdefault(key, set()).add(language)

    found, failed = [], 0
    for key, scan_id, path, profile_id in con.execute(target.profile_sql):
        languages = wanted.get(profile_id, [])
        claimed = [lang for lang, _, _ in languages
                   if any(same_language(lang, have) for have in embedded.get(key, ()))]
        if not claimed or not path:
            continue                       # Bazarr is already searching for whatever it lacks
        try:
            reads = track_reads(host_path(path), codes, {lang for lang, _, _ in languages},
                                verdicts, seen)
        except FileNotFoundError:
            continue
        except (subprocess.CalledProcessError, subprocess.TimeoutExpired, ValueError,
                OSError) as exc:
            print(f"cannot sample {Path(path).name}: {type(exc).__name__}", file=sys.stderr)
            failed += 1
            continue
        if not any(r["read"] and not same_language(r["read"], r["tag"]) for r in reads.values()):
            continue
        backed = external.get(key, set()) | {r["read"] for r in reads.values() if r["read"]}
        missing = [item for item in languages
                   if not any(same_language(item[0], have) for have in backed)]
        if missing:
            found.append((key, scan_id, display_name(target, path), missing))
    return found, failed


def display_name(target, path):
    return Path(path).parent.name if target.kind == "movies" else Path(path).stem


def load_verdicts():
    try:
        return json.loads(VERDICTS.read_text())
    except (OSError, ValueError):
        return {}


def save_verdicts(verdicts):
    try:
        STATE_DIR.mkdir(parents=True, exist_ok=True)
        partial = VERDICTS.with_suffix(".tmp")
        partial.write_text(json.dumps(verdicts, indent=1, sort_keys=True))
        partial.replace(VERDICTS)
    except OSError as exc:
        print(f"cannot keep the track verdicts: {exc}", file=sys.stderr)


def bazarr_apikey():
    try:
        import yaml
        return yaml.safe_load(BAZARR_CONFIG.read_text())["auth"]["apikey"]
    except (OSError, KeyError, ValueError, ImportError) as exc:
        print(f"no bazarr apikey ({exc})", file=sys.stderr)
        return None


def bazarr_patch(key, endpoint, params, max_time):
    # Bazarr publishes no port; reach it through a container already on media-network.
    args = ["timeout", str(max_time + 15), "docker", "exec", "maintainerr", "curl", "-sf",
            "--connect-timeout", "5", "--max-time", str(max_time), "-X", "PATCH",
            "-H", f"X-API-KEY: {key}"]
    for name, value in params.items():
        args += ["--data-urlencode", f"{name}={value}"]
    args.append(f"http://bazarr:6767/api/{endpoint}")
    return subprocess.run(args, capture_output=True, text=True).returncode == 0


def rescan(plan, key):
    """Ask Bazarr to re-read the items it just lost rows for, so missing_subtitles catches up."""
    for target, (_, rescans, _, _) in plan.items():
        for scan_id in sorted(rescans):
            if not bazarr_patch(key, target.endpoint,
                                {target.param: scan_id, "action": "scan-disk"}, 30):
                print(f"{target.endpoint} {scan_id} would not rescan, "
                      f"leaving it to the 04:00 index", file=sys.stderr)


def request(asks, key):
    """Have Bazarr search and download each language now, past its belief in the embedded tag."""
    for target, items in asks.items():
        for item_key, scan_id, name, missing in items:
            if target.kind == "movies":
                endpoint, ids = "movies/subtitles", {"radarrid": item_key}
            else:
                endpoint, ids = "episodes/subtitles", {"seriesid": scan_id, "episodeid": item_key}
            for language, hi, forced in missing:
                params = {**ids, "language": language,
                          "hi": str(hi).lower(), "forced": str(forced).lower()}
                # A search across every provider can take minutes.
                if not bazarr_patch(key, endpoint, params, 300):
                    print(f"{name}: bazarr would not search {language}", file=sys.stderr)


def main():
    dry_run = "--dry-run" in sys.argv
    if not BAZARR_DB.exists():
        return 0

    verdicts, seen = load_verdicts(), {}
    con = sqlite3.connect(f"file:{BAZARR_DB}?mode=ro", uri=True, timeout=SQLITE_TIMEOUT)
    try:
        codes = language_map(con)
        wanted = profiles(con)
        surveys = {target: survey(con, target, codes) for target in TARGETS}
        checks = {target: mislabelled(con, target, codes, wanted, verdicts, seen)
                  for target in TARGETS}
    finally:
        con.close()
    save_verdicts(seen)

    failed = sum(s[3] for s in surveys.values()) + sum(c[1] for c in checks.values())
    plan = {target: s for target, s in surveys.items() if s[0]}
    for target, (doomed, _, names, _) in plan.items():
        print(f"{target.kind}: {'would drop' if dry_run else 'dropped'} {len(doomed)} stale rows "
              f"from {len(names)}: {', '.join(names)}")
    asks = {target: c[0] for target, c in checks.items() if c[0]}
    for target, items in asks.items():
        for _, _, name, missing in items:
            languages = ", ".join(language for language, _, _ in missing)
            print(f"{target.kind}: {name} has mislabelled subtitle tracks, "
                  f"{'would ask' if dry_run else 'asked'} bazarr for {languages}")

    if dry_run or not (plan or asks):
        return 1 if failed else 0
    key = bazarr_apikey()
    if plan:
        try:
            delete(plan)
        except sqlite3.Error as exc:
            print(f"delete failed: {exc}", file=sys.stderr)
            return 1
        if key:
            rescan(plan, key)
    if asks and key:
        request(asks, key)
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
