#!/usr/bin/env python3
"""Push the media stack's "which one" metrics to Pushgateway, for the Media Pipeline dashboard.

Three things the existing exporters cannot answer:

  arr_quality_change_*   which movie was upgraded, from which quality to which
  qbit_torrent_*         which torrents are in each state, not how many
  arr_indexer_grabs_90d  which indexers actually get used, so alerts can ignore the rest
  arr_media_size_bytes   where the disk went: size per title, tagged with its quality
  arr_library_titles     how many films and series there are, on disk and in total
  arr_media_audio        which audio tracks each film actually has, one series per language
  arr_waiting            everything Radarr waits for: downloading, missing or below cutoff
  prowlarr_indexer_up    which indexers are failing right now, over time
  prowlarr_indexer_*_total  queries, grabs and failures per indexer, so "is it being used"
                         is answerable separately from "is it up"
  maintainerr_pending_*  which films are watched and waiting out the grace period before deletion
  arr_orphan_*           what nothing is managing: unimportable queue items, unclaimed data

All three use labels to carry identity, which is not what Prometheus is for. It is a deliberate
trade: the alternative is the Infinity datasource querying each API live, which means a Grafana
plugin plus every app's API key stored in Grafana. Each group is bounded (tens of series) and pushed
with PUT, so it replaces itself instead of growing.

Run from cron (see scripts/crontab). Silent unless something fails; a failure in one group does not
stop the others.
"""

import json
import os
import subprocess
import sys
import urllib.error
import urllib.request
from datetime import datetime, timedelta, timezone
from pathlib import Path

PROJECT_DIR = Path(os.path.expanduser("~/rpi-homeserver"))
PUSHGATEWAY = "http://localhost:9091"
KEEP_UPGRADES = 25
PAIR_WINDOW = 120  # seconds between the delete and the import to call it the same upgrade

DATA_ROOT = Path("/mnt/data")          # what the containers see as /data
DOWNLOADS = DATA_ROOT / "downloads"
SKIP_ENTRIES = {"incomplete"}          # qBittorrent writes downloads in progress here
MIN_ORPHAN_BYTES = 100 * 1024 ** 2     # below this it is a sample, an nfo or a stray subtitle

QUEUES = [
    # name, port, the id an unattributable item lacks, the arg that includes those items
    ("radarr", 7878, "movieId", "includeUnknownMovieItems"),
    ("sonarr", 8989, "seriesId", "includeUnknownSeriesItems"),
]

ARRS = [
    # name, port, api, deleted-event type, extra query args, title extractor
    ("radarr", 7878, "v3", "movieFileDeleted", "includeMovie=true",
     lambda r: (r.get("movie") or {}).get("title", "?")),
    ("sonarr", 8989, "v3", "episodeFileDeleted", "includeSeries=true&includeEpisode=true",
     lambda r: (r.get("series") or {}).get("title", "?")),
]

failures = []


def api_key(app):
    text = (PROJECT_DIR / "appdata" / app / "config.xml").read_text(errors="ignore")
    start = text.index("<ApiKey>") + len("<ApiKey>")
    return text[start:text.index("</ApiKey>", start)]


def get(url, key):
    request = urllib.request.Request(url, headers={"X-Api-Key": key})
    with urllib.request.urlopen(request, timeout=15) as resp:
        return json.loads(resp.read())


def push(job, lines):
    payload = "\n".join(lines) + "\n"
    request = urllib.request.Request(f"{PUSHGATEWAY}/metrics/job/{job}",
                                    data=payload.encode(), method="PUT")
    try:
        urllib.request.urlopen(request, timeout=10).close()
    except (urllib.error.URLError, OSError) as exc:
        failures.append(f"{job}: pushgateway unreachable: {exc}")


def escape(value):
    return str(value).replace("\\", "\\\\").replace('"', '\\"').replace("\n", " ")


def parse_time(value):
    return datetime.fromisoformat(value.replace("Z", "+00:00"))


def quality(record):
    return ((record.get("quality") or {}).get("quality") or {}).get("name", "?")


def quality_changes():
    """An upgrade is two events at the same instant: old file deleted, new one imported."""
    rows = []
    for app, port, api, deleted_event, extra, title_of in ARRS:
        try:
            records = get(f"http://localhost:{port}/api/{api}/history"
                          f"?pageSize=200&sortKey=date&sortDirection=descending&{extra}",
                          api_key(app))["records"]
        except Exception as exc:
            failures.append(f"{app} history: {exc}")
            continue

        imported = [r for r in records if r["eventType"] == "downloadFolderImported"]
        for record in records:
            if record["eventType"] != deleted_event:
                continue
            if (record.get("data") or {}).get("reason") != "Upgrade":
                continue
            when = parse_time(record["date"])
            match = next((i for i in imported
                          if title_of(i) == title_of(record)
                          and abs((parse_time(i["date"]) - when).total_seconds()) <= PAIR_WINDOW), None)
            if match:
                rows.append({"app": app, "title": title_of(record),
                             "from": quality(record), "to": quality(match), "at": when})

    rows.sort(key=lambda r: r["at"], reverse=True)
    rows = rows[:KEEP_UPGRADES]

    lines = ["# HELP arr_quality_change_timestamp Unix time a file was replaced by a different quality",
             "# TYPE arr_quality_change_timestamp gauge"]
    for row in rows:
        labels = ",".join(f'{k}="{escape(row[k])}"' for k in ("app", "title", "from", "to"))
        lines.append(f"arr_quality_change_timestamp{{{labels}}} {int(row['at'].timestamp())}")
    lines += ["# HELP arr_quality_changes_tracked How many upgrades are being exposed",
              "# TYPE arr_quality_changes_tracked gauge",
              f"arr_quality_changes_tracked {len(rows)}"]
    push("arr_history", lines)


def qbit_list():
    """Read from inside the container: the WebUI trusts localhost, the host arrives via the Docker
    gateway which is not in its AuthSubnetWhitelist, so this needs no credentials.

    None means "could not ask", which is not the same as "no torrents": orphan detection has to
    tell those apart or a qBittorrent that is merely down turns every download into an orphan.
    """
    try:
        result = subprocess.run(
            ["docker", "exec", "qbittorrent", "curl", "-sf",
             "http://localhost:8080/api/v2/torrents/info"],
            capture_output=True, text=True, timeout=30)
        if result.returncode != 0:
            raise RuntimeError(result.stderr.strip()[:120] or "curl failed")
        return json.loads(result.stdout)
    except Exception as exc:
        failures.append(f"qbittorrent: {exc}")
        return None


def qbit_torrents():
    items = qbit_list()
    if items is None:
        return

    progress = ["# HELP qbit_torrent_progress Download progress 0-1, name and state as labels",
                "# TYPE qbit_torrent_progress gauge"]
    ratio = ["# HELP qbit_torrent_ratio Share ratio per torrent",
             "# TYPE qbit_torrent_ratio gauge"]
    size = ["# HELP qbit_torrent_size_bytes Total size per torrent",
            "# TYPE qbit_torrent_size_bytes gauge"]
    for t in items:
        labels = (f'name="{escape(t["name"])[:90]}",state="{escape(t["state"])}",'
                  f'category="{escape(t.get("category") or "none")}"')
        progress.append(f"qbit_torrent_progress{{{labels}}} {t['progress']}")
        ratio.append(f"qbit_torrent_ratio{{{labels}}} {round(t.get('ratio', 0), 3)}")
        size.append(f"qbit_torrent_size_bytes{{{labels}}} {t.get('size', 0)}")
    push("qbit_torrents", progress + ratio + size)


def indexer_usage():
    """Grabs per indexer over the last 90 days. This is what makes "an indexer is down" alertable:
    without it every flaky indexer pages you, including the ones that have never grabbed anything.
    The label is `name`, matching prowlarr_indexer_up, so the two can be joined in PromQL."""
    from collections import Counter
    cut = datetime.now(timezone.utc) - timedelta(days=90)
    counts = Counter()
    for app, port, api, *_ in ARRS:
        try:
            key = api_key(app)
            for page in (1, 2, 3):
                records = get(f"http://localhost:{port}/api/{api}/history"
                              f"?page={page}&pageSize=200&sortKey=date&sortDirection=descending",
                              key)["records"]
                if not records:
                    break
                for record in records:
                    if record["eventType"] != "grabbed":
                        continue
                    if parse_time(record["date"]) < cut:
                        continue
                    indexer = (record.get("data") or {}).get("indexer", "?")
                    counts[indexer.replace(" (Prowlarr)", "")] += 1
        except Exception as exc:
            failures.append(f"{app} grabs: {exc}")

    lines = ["# HELP arr_indexer_grabs_90d Grabs per indexer in the last 90 days",
             "# TYPE arr_indexer_grabs_90d gauge"]
    for name, n in counts.items():
        lines.append(f'arr_indexer_grabs_90d{{name="{escape(name)}"}} {n}')
    push("arr_indexer_usage", lines)


def audio_languages(movie_file):
    """The audio languages Radarr reports for the file, as it names them.

    Radarr already hands these over as names, so nothing is translated here. The raw per-stream
    codes in `mediaInfo.audioLanguages` are the other candidate, but they arrive as ISO 639-2
    ("jpn/eng"), which would need a lookup table to be readable, and they buy nothing: across the
    library the two agree on every file that carries language tags at all, and the 7 that do not
    have only this field to go on anyway.
    """
    names = sorted({l["name"] for l in (movie_file.get("languages") or []) if l.get("name")})
    return names or ["Unknown"]


def media_audio():
    """One series per (film, audio language), so a dashboard can filter by language.

    The full list travels along as the `languages` label on every one of a film's series, so the
    table can show "English, Spanish" on a row that was matched by `language="Spanish"` alone.
    Filtering and displaying want different shapes and this carries both.
    """
    lines = ["# HELP arr_media_audio 1 per film and audio language present in the file",
             "# TYPE arr_media_audio gauge"]
    try:
        movies = get("http://localhost:7878/api/v3/movie", api_key("radarr"))
        for movie in movies:
            f = movie.get("movieFile")
            if not movie.get("hasFile") or not f:
                continue
            langs = audio_languages(f)
            info = f.get("mediaInfo") or {}
            codec = info.get("audioCodec") or "?"
            channels = info.get("audioChannels")
            q = ((f.get("quality") or {}).get("quality") or {}).get("name", "Unknown")
            for lang in langs:
                lines.append(
                    f'arr_media_audio{{title="{escape(movie["title"])[:90]}",'
                    f'language="{escape(lang)}",languages="{escape(", ".join(langs))[:120]}",'
                    f'language_count="{len(langs)}",codec="{escape(codec)}",'
                    f'channels="{escape(channels if channels is not None else "?")}",'
                    f'quality="{escape(q)}"}} 1')
    except Exception as exc:
        failures.append(f"radarr audio: {exc}")

    push("arr_media_audio", lines)


def when_better(movie, today):
    """Best honest answer to "when will this improve?".

    There is no date for "when a good release gets uploaded", but there IS one for "when the film
    becomes available at all": a title still in cinemas cannot have a Bluray, no matter how often we
    search. Radarr carries TMDB's dates, so use them.
    """
    for field, label in (("digitalRelease", "digital"), ("physicalRelease", "physical")):
        value = movie.get(field)
        if not value:
            continue
        date = parse_time(value)
        if date > today:
            return f"{label} {date:%Y-%m-%d}"
        return "out, waiting for a good release"
    if movie.get("status") == "inCinemas":
        cinema = movie.get("inCinemas")
        since = f" since {parse_time(cinema):%Y-%m-%d}" if cinema else ""
        return f"in cinemas{since}, no digital date"
    if movie.get("status") == "announced":
        return "not released yet"
    return "unknown"


def waiting_on():
    """Everything Radarr is waiting for, in one list, with what it is waiting for.

      downloading  in the queue right now, so the answer to "when" is an ETA
      missing       monitored with no file at all
      upgrade       has a file, but below the profile cutoff

    "When" for the last two is not a date anyone can give: it depends on a release appearing. What
    can be given is when the film is even available in that quality, which is what when_better does.
    """
    lines = ["# HELP arr_waiting 1 for a title Radarr is waiting on; kind says what it is waiting for",
             "# TYPE arr_waiting gauge"]
    try:
        key = api_key("radarr")
        profiles = {p["id"]: p for p in get("http://localhost:7878/api/v3/qualityprofile", key)}
        cutoff_name = {}
        for pid, profile in profiles.items():
            target = profile["cutoff"]
            cutoff_name[pid] = next(
                ((i.get("name") or i["quality"]["name"]) for i in profile["items"]
                 if (i.get("id") or i.get("quality", {}).get("id")) == target), str(target))

        today = datetime.now(timezone.utc)
        movies = get("http://localhost:7878/api/v3/movie", key)
        by_title, current, expected = {}, {}, {}
        for movie in movies:
            by_title[movie["title"]] = movie
            f = movie.get("movieFile")
            if movie.get("hasFile") and f:
                current[movie["title"]] = ((f.get("quality") or {}).get("quality") or {}).get("name", "Unknown")
            expected[movie["title"]] = when_better(movie, today)

        rows = {}
        # downloading wins over the other two: it is already happening
        for item in get("http://localhost:7878/api/v3/queue?pageSize=100&includeMovie=true", key)["records"]:
            title = (item.get("movie") or {}).get("title") or item.get("title", "?")
            eta = item.get("timeleft") or ""
            state = item.get("trackedDownloadState") or item.get("status") or ""
            quality = ((item.get("quality") or {}).get("quality") or {}).get("name", "?")
            # an ETA is only honest while bytes are moving: a queue item with nothing downloaded
            # after hours is stalled, and saying "downloading" would hide that
            if eta and eta not in ("00:00:00", "0:00:00"):
                when = f"eta {eta}"
            elif item.get("sizeleft") and item.get("sizeleft") == item.get("size"):
                when = "stalled, no data yet"
            else:
                when = state or "in queue"
            rows[title] = {"kind": "downloading", "current": current.get(title, "none"),
                           "target": quality, "expected": when}

        for movie in movies:
            title = movie["title"]
            if title in rows or not movie.get("monitored") or movie.get("hasFile"):
                continue
            rows[title] = {"kind": "missing", "current": "none",
                           "target": cutoff_name.get(movie.get("qualityProfileId"), "?"),
                           "expected": expected.get(title, "unknown")}

        for record in get("http://localhost:7878/api/v3/wanted/cutoff?pageSize=200", key)["records"]:
            title = record["title"]
            if title in rows:
                continue
            rows[title] = {"kind": "upgrade", "current": current.get(title, "none"),
                           "target": cutoff_name.get(record.get("qualityProfileId"), "?"),
                           "expected": expected.get(title, "unknown")}

        for title, row in rows.items():
            profile = profiles.get((by_title.get(title) or {}).get("qualityProfileId"), {}).get("name", "?")
            lines.append(f'arr_waiting{{app="radarr",title="{escape(title)[:90]}",'
                         f'kind="{row["kind"]}",current="{escape(row["current"])}",'
                         f'target="{escape(row["target"])}",expected="{escape(row["expected"])}",'
                         f'profile="{escape(profile)}"}} 1')
    except Exception as exc:
        failures.append(f"radarr waiting: {exc}")

    push("arr_upgrade_queue", lines)


def library_sizes():
    """Size per title, with its quality as a label, so the dashboard can answer where the disk went.

    Radarr knows both the size and the quality of every file; the disk_file_bytes metric from
    disk-usage-metrics.sh only knows paths, and guessing quality from a filename is a losing game.
    Sonarr series get quality "mixed" (a season is many files, often several qualities).

    The plain "how many do I have" counts ride along here rather than being derived from the size
    series: counting those would silently answer "titles that happen to have a file", so a movie
    Radarr is still hunting for would go missing from the total without anything looking wrong.
    """
    lines = ["# HELP arr_media_size_bytes Size on disk per title, with its quality",
             "# TYPE arr_media_size_bytes gauge"]
    counts = {}
    try:
        movies = get("http://localhost:7878/api/v3/movie", api_key("radarr"))
        for movie in movies:
            f = movie.get("movieFile")
            if not movie.get("hasFile") or not f:
                continue
            q = ((f.get("quality") or {}).get("quality") or {}).get("name", "Unknown")
            lines.append(f'arr_media_size_bytes{{app="radarr",'
                         f'title="{escape(movie["title"])[:90]}",quality="{escape(q)}"}} '
                         f'{f.get("size", 0)}')
        counts[("radarr", "total")] = len(movies)
        counts[("radarr", "with_file")] = sum(1 for m in movies if m.get("hasFile"))
    except Exception as exc:
        failures.append(f"radarr library: {exc}")

    try:
        shows = get("http://localhost:8989/api/v3/series", api_key("sonarr"))
        for series in shows:
            size = (series.get("statistics") or {}).get("sizeOnDisk", 0)
            if not size:
                continue
            lines.append(f'arr_media_size_bytes{{app="sonarr",'
                         f'title="{escape(series["title"])[:90]}",quality="mixed"}} {size}')
        counts[("sonarr", "total")] = len(shows)
        counts[("sonarr", "with_file")] = sum(
            1 for x in shows if (x.get("statistics") or {}).get("episodeFileCount", 0))
        counts[("sonarr", "episodes")] = sum(
            (x.get("statistics") or {}).get("episodeFileCount", 0) for x in shows)
    except Exception as exc:
        failures.append(f"sonarr library: {exc}")

    lines += ["# HELP arr_library_titles Titles in each app; kind separates the whole list from what is on disk",
              "# TYPE arr_library_titles gauge"]
    for (app, kind), n in sorted(counts.items()):
        lines.append(f'arr_library_titles{{app="{app}",kind="{kind}"}} {n}')

    push("arr_library", lines)


def maintainerr_pending():
    """Films Maintainerr has queued for deletion: watched, waiting out the grace period.

    This is the first stage of the retention policy, and the only one nothing else can see. Radarr
    still has the film, the torrent is still hardlinked, and seed-cleanup.py will not look at it
    until Maintainerr deletes the library copy.

    The list comes from the paginated media endpoint rather than the `media` array on /api/collections,
    which is capped: with three films queued it returned two, so the count was wrong and one title was
    missing. Each row carries its own `mediaData`, so titles need no lookup anywhere else.

    Its port is not published, so the call goes through the container, and its own server listens on
    IPv4 only: localhost inside there resolves to ::1 and is refused.
    """
    def api(path):
        result = subprocess.run(
            ["docker", "exec", "maintainerr", "wget", "-qO-",
             f"http://127.0.0.1:6246/api/{path}"],
            capture_output=True, text=True, timeout=30)
        if result.returncode != 0:
            raise RuntimeError(result.stderr.strip()[:120] or "wget failed")
        return json.loads(result.stdout)

    try:
        collections = api("collections")
    except Exception as exc:
        failures.append(f"maintainerr: {exc}")
        return

    counts = ["# HELP maintainerr_pending_media Films queued for deletion, per collection",
              "# TYPE maintainerr_pending_media gauge"]
    sizes = ["# HELP maintainerr_pending_bytes Disk held by films queued for deletion",
             "# TYPE maintainerr_pending_bytes gauge"]
    since = ["# HELP maintainerr_pending_since_timestamp When each film entered the queue",
             "# TYPE maintainerr_pending_since_timestamp gauge"]
    per_film = ["# HELP maintainerr_pending_film_bytes Disk each queued film would give back",
                "# TYPE maintainerr_pending_film_bytes gauge"]

    for collection in collections:
        label = f'collection="{escape(collection.get("title", "?"))}"'
        items = []
        for page in range(1, 21):
            try:
                batch = (api(f"collections/media/{collection['id']}/content/{page}") or {}).get("items")
            except Exception as exc:
                failures.append(f"maintainerr media page {page}: {exc}")
                break
            if not batch:
                break
            items += batch

        counts.append(f"maintainerr_pending_media{{{label}}} {len(items)}")
        sizes.append(f"maintainerr_pending_bytes{{{label}}} {collection.get('totalSizeBytes') or 0}")
        for item in items:
            data = item.get("mediaData") or {}
            title = data.get("title") or f"plex {item.get('mediaServerId')}"
            labels = f'{label},title="{escape(title)[:90]}"'
            added = item.get("addDate")
            if added:
                stamp = parse_time(added.replace(" ", "T")).timestamp()
                since.append(f"maintainerr_pending_since_timestamp{{{labels}}} {int(stamp)}")
            per_film.append(f"maintainerr_pending_film_bytes{{{labels}}} {item.get('sizeBytes') or 0}")

    push("maintainerr_pending", counts + sizes + since + per_film)


def tree_bytes_and_links(path):
    """Total bytes under a path, and the highest link count among its real payload files. Small
    files are ignored for the link count: an nfo or a subtitle is never hardlinked, so counting
    them would report every release as unshared."""
    total, links = 0, 0
    if path.is_file():
        stat = path.stat()
        return stat.st_size, stat.st_nlink
    for root, _, names in os.walk(path):
        for name in names:
            try:
                stat = os.stat(os.path.join(root, name))
            except OSError:
                continue
            total += stat.st_size
            if stat.st_size > MIN_ORPHAN_BYTES // 2:
                links = max(links, stat.st_nlink)
    return total, links


def orphans():
    """The two ways media ends up managed by nothing at all.

    A queue item the *arr cannot attribute to a movie or series never imports. It waits for a
    human, and the *arr only re-announces it when it restarts, so one deletion made while a
    download was in flight raised Telegram alerts eleven days later and nothing else ever said so.

    Data in downloads that no torrent claims is invisible to seed-cleanup.py, which only ever looks
    at torrents that exist. While the library still shares the file it wastes nothing, which is why
    `linked` is a label and not a filter: the day the retention policy deletes the film, that
    leftover name becomes the last reference to bytes nobody will ever reclaim, and it is exactly
    then that it starts counting under linked="no".
    """
    queue_rows = []
    for app, port, id_field, unknown_arg in QUEUES:
        try:
            records = get(f"http://localhost:{port}/api/v3/queue?pageSize=200&{unknown_arg}=true",
                          api_key(app))["records"]
        except Exception as exc:
            failures.append(f"{app} queue: {exc}")
            continue
        for record in records:
            stuck = record.get("trackedDownloadState") in ("importBlocked", "importFailed")
            if not stuck and record.get(id_field):
                continue
            reason = ""
            for message in record.get("statusMessages") or []:
                reason = ((message.get("messages") or [None])[0]) or message.get("title", "")
                break
            queue_rows.append((app, record.get("title", "?"),
                               record.get("trackedDownloadState", "?"), reason,
                               record.get("size", 0)))

    lines = ["# HELP arr_orphan_queue_bytes A queue item the arr cannot import without a human",
             "# TYPE arr_orphan_queue_bytes gauge"]
    for app, title, state, reason, size in queue_rows:
        labels = (f'app="{app}",title="{escape(title)[:90]}",state="{escape(state)}",'
                  f'reason="{escape(reason)[:70]}"')
        lines.append(f"arr_orphan_queue_bytes{{{labels}}} {size}")
    lines += ["# HELP arr_orphan_queue_items Queue items waiting for a human, per app",
              "# TYPE arr_orphan_queue_items gauge"]
    for app, _, _, _ in QUEUES:
        lines.append(f'arr_orphan_queue_items{{app="{app}"}} '
                     f'{sum(1 for row in queue_rows if row[0] == app)}')

    data_rows = []
    items = qbit_list()
    if items is not None and DOWNLOADS.is_dir():
        claimed = set()
        for torrent in items:
            content = torrent.get("content_path") or ""
            if not content:
                continue
            path = Path(content.replace("/data/", f"{DATA_ROOT}/", 1))
            try:
                claimed.add(path.relative_to(DOWNLOADS).parts[0])
            except ValueError:
                continue
        for entry in sorted(DOWNLOADS.iterdir()):
            if entry.name in SKIP_ENTRIES or entry.name in claimed:
                continue
            size, link_count = tree_bytes_and_links(entry)
            if size >= MIN_ORPHAN_BYTES:
                data_rows.append((entry.name, size, link_count))

    lines += ["# HELP arr_orphan_data_bytes Data in downloads that no torrent claims any more",
              "# TYPE arr_orphan_data_bytes gauge"]
    for name, size, link_count in data_rows:
        shared = "yes" if link_count >= 2 else "no"
        lines.append(f'arr_orphan_data_bytes{{name="{escape(name)[:90]}",linked="{shared}"}} {size}')
    lines += ["# HELP arr_orphan_data_total_bytes Orphan data, split by whether the library shares it",
              "# TYPE arr_orphan_data_total_bytes gauge"]
    for shared in ("yes", "no"):
        total = sum(size for _, size, link_count in data_rows
                    if (link_count >= 2) == (shared == "yes"))
        lines.append(f'arr_orphan_data_total_bytes{{linked="{shared}"}} {total}')
    push("arr_orphans", lines)


def prowlarr_indexers():
    """1 when working, 0 while Prowlarr has it disabled after failures. One series per indexer, so a
    state timeline shows them dropping out and coming back."""
    try:
        key = api_key("prowlarr")
        indexers = get("http://localhost:9696/api/v1/indexer", key)
        status = get("http://localhost:9696/api/v1/indexerstatus", key)
    except Exception as exc:
        failures.append(f"prowlarr: {exc}")
        return

    now = datetime.now(timezone.utc)
    disabled = {s["indexerId"]: s for s in status
                if s.get("disabledTill") and parse_time(s["disabledTill"]) > now}

    lines = ["# HELP prowlarr_indexer_up 1 when the indexer is usable, 0 while disabled after failures",
             "# TYPE prowlarr_indexer_up gauge"]
    for i in indexers:
        up = 0 if i["id"] in disabled else 1
        lines.append(f'prowlarr_indexer_up{{name="{escape(i["name"])}",'
                     f'enabled="{str(i["enable"]).lower()}"}} {up}')
    lines += ["# HELP prowlarr_indexers_down How many indexers are disabled right now",
              "# TYPE prowlarr_indexers_down gauge",
              f"prowlarr_indexers_down {len(disabled)}"]
    push("prowlarr_indexers", lines)


def prowlarr_indexer_activity():
    """Queries, grabs and failures per indexer, as counters, so Grafana can show WHEN an indexer is
    being used rather than only whether it answers.

    `prowlarr_indexer_up` says an indexer is reachable. It says nothing about whether anything is
    being asked of it, which is the question that matters for a private tracker: an indexer that is
    up, queried 997 times and has never returned a grab is a different problem from one that is
    down, and the availability timeline shows them identically.

    /api/v1/indexerstats with no date range returns all-time totals, which is what makes these
    counters rather than gauges: increase() over a window gives the rate, and a Prowlarr history
    prune shows up as a counter reset, which rate() already handles. Passing a date range instead
    would hand Prometheus a pre-averaged number over a window it did not choose.
    """
    try:
        stats = get("http://localhost:9696/api/v1/indexerstats", api_key("prowlarr"))
    except Exception as exc:
        failures.append(f"prowlarr stats: {exc}")
        return

    queries = ["# HELP prowlarr_indexer_queries_total Searches sent to this indexer, all time",
               "# TYPE prowlarr_indexer_queries_total counter"]
    grabs = ["# HELP prowlarr_indexer_grabs_total Grabs taken from this indexer, all time",
             "# TYPE prowlarr_indexer_grabs_total counter"]
    failed = ["# HELP prowlarr_indexer_failed_queries_total Searches this indexer failed, all time",
              "# TYPE prowlarr_indexer_failed_queries_total counter"]
    slow = ["# HELP prowlarr_indexer_response_ms Average response time this indexer answers in",
            "# TYPE prowlarr_indexer_response_ms gauge"]

    for i in stats.get("indexers", []):
        label = f'{{name="{escape(i["indexerName"])}"}}'
        queries.append(f'prowlarr_indexer_queries_total{label} {i.get("numberOfQueries", 0)}')
        grabs.append(f'prowlarr_indexer_grabs_total{label} {i.get("numberOfGrabs", 0)}')
        failed.append(f'prowlarr_indexer_failed_queries_total{label} '
                      f'{i.get("numberOfFailedQueries", 0)}')
        slow.append(f'prowlarr_indexer_response_ms{label} {i.get("averageResponseTime", 0)}')

    push("prowlarr_indexer_activity", queries + grabs + failed + slow)


if __name__ == "__main__":
    quality_changes()
    qbit_torrents()
    indexer_usage()
    library_sizes()
    media_audio()
    waiting_on()
    prowlarr_indexers()
    prowlarr_indexer_activity()
    maintainerr_pending()
    orphans()
    if failures:
        sys.exit("; ".join(failures))
