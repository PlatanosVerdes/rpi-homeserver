#!/usr/bin/env python3
"""Push the media stack's "which one" metrics to Pushgateway, for the Media Pipeline dashboard.

Three things the existing exporters cannot answer:

  arr_quality_change_*   which movie was upgraded, from which quality to which
  qbit_torrent_*         which torrents are in each state, not how many
  arr_indexer_grabs_90d  which indexers actually get used, so alerts can ignore the rest
  arr_media_size_bytes   where the disk went: size per title, tagged with its quality
  prowlarr_indexer_up    which indexers are failing right now, over time

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


def qbit_torrents():
    """Read from inside the container: the WebUI trusts localhost, the host arrives via the Docker
    gateway which is not in its AuthSubnetWhitelist, so this needs no credentials."""
    try:
        result = subprocess.run(
            ["docker", "exec", "qbittorrent", "curl", "-sf",
             "http://localhost:8080/api/v2/torrents/info"],
            capture_output=True, text=True, timeout=30)
        if result.returncode != 0:
            raise RuntimeError(result.stderr.strip()[:120] or "curl failed")
        items = json.loads(result.stdout)
    except Exception as exc:
        failures.append(f"qbittorrent: {exc}")
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


def library_sizes():
    """Size per title, with its quality as a label, so the dashboard can answer where the disk went.

    Radarr knows both the size and the quality of every file; the disk_file_bytes metric from
    disk-usage-metrics.sh only knows paths, and guessing quality from a filename is a losing game.
    Sonarr series get quality "mixed" (a season is many files, often several qualities).
    """
    lines = ["# HELP arr_media_size_bytes Size on disk per title, with its quality",
             "# TYPE arr_media_size_bytes gauge"]
    try:
        for movie in get("http://localhost:7878/api/v3/movie", api_key("radarr")):
            f = movie.get("movieFile")
            if not movie.get("hasFile") or not f:
                continue
            q = ((f.get("quality") or {}).get("quality") or {}).get("name", "Unknown")
            lines.append(f'arr_media_size_bytes{{app="radarr",'
                         f'title="{escape(movie["title"])[:90]}",quality="{escape(q)}"}} '
                         f'{f.get("size", 0)}')
    except Exception as exc:
        failures.append(f"radarr library: {exc}")

    try:
        for series in get("http://localhost:8989/api/v3/series", api_key("sonarr")):
            size = (series.get("statistics") or {}).get("sizeOnDisk", 0)
            if not size:
                continue
            lines.append(f'arr_media_size_bytes{{app="sonarr",'
                         f'title="{escape(series["title"])[:90]}",quality="mixed"}} {size}')
    except Exception as exc:
        failures.append(f"sonarr library: {exc}")

    push("arr_library", lines)


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


if __name__ == "__main__":
    quality_changes()
    qbit_torrents()
    indexer_usage()
    library_sizes()
    prowlarr_indexers()
    if failures:
        sys.exit("; ".join(failures))
