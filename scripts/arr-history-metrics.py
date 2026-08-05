#!/usr/bin/env python3
"""Expose recent *arr quality changes as metrics, so they can be looked at in Grafana.

An upgrade is two history events at the same instant: the old file deleted with reason "Upgrade",
and the new one imported. This pairs them and pushes one series per upgrade, with the titles and
qualities as labels, to Pushgateway.

Using labels as an event log is not what Prometheus is for, and that is deliberate: the alternative
(the Infinity datasource querying the *arr APIs live) means a plugin plus each app's API key stored
in Grafana. The set here is bounded to the last KEEP events and PUT replaces the whole group on
every run, so old rows fall out instead of piling up.

Run from cron (see scripts/crontab). No output unless something fails.
"""

import json
import os
import sys
import urllib.error
import urllib.request
from datetime import datetime, timezone
from pathlib import Path

PROJECT_DIR = Path(os.path.expanduser("~/rpi-homeserver"))
PUSHGATEWAY = "http://localhost:9091"
KEEP = 25          # how many recent upgrades to keep exposed
PAIR_WINDOW = 120  # seconds between the delete and the import to call it the same upgrade

APPS = [
    # name, port, api version, deleted-event type, extra query args, title extractor
    ("radarr", 7878, "v3", "movieFileDeleted", "includeMovie=true",
     lambda r: (r.get("movie") or {}).get("title", "?")),
    ("sonarr", 8989, "v3", "episodeFileDeleted", "includeSeries=true&includeEpisode=true",
     lambda r: (r.get("series") or {}).get("title", "?")),
]


def api_key(app):
    config = PROJECT_DIR / "appdata" / app / "config.xml"
    text = config.read_text(errors="ignore")
    start = text.index("<ApiKey>") + len("<ApiKey>")
    return text[start:text.index("</ApiKey>", start)]


def history(app, port, api, extra, key):
    url = f"http://localhost:{port}/api/{api}/history?pageSize=200&sortKey=date&sortDirection=descending&{extra}"
    request = urllib.request.Request(url, headers={"X-Api-Key": key})
    with urllib.request.urlopen(request, timeout=15) as resp:
        return json.loads(resp.read())["records"]


def parse_time(value):
    return datetime.fromisoformat(value.replace("Z", "+00:00"))


def quality(record):
    return ((record.get("quality") or {}).get("quality") or {}).get("name", "?")


def escape(value):
    return value.replace("\\", "\\\\").replace('"', '\\"').replace("\n", " ")


def collect():
    rows = []
    for app, port, api, deleted_event, extra, title_of in APPS:
        try:
            records = history(app, port, api, extra, api_key(app))
        except (urllib.error.URLError, OSError, ValueError, KeyError) as exc:
            print(f"{app}: could not read history: {exc}", file=sys.stderr)
            continue

        imported = [r for r in records if r["eventType"] == "downloadFolderImported"]
        for record in records:
            if record["eventType"] != deleted_event:
                continue
            if (record.get("data") or {}).get("reason") != "Upgrade":
                continue
            when = parse_time(record["date"])
            # the import that replaced it: same title, within a couple of minutes
            match = next((i for i in imported
                          if title_of(i) == title_of(record)
                          and abs((parse_time(i["date"]) - when).total_seconds()) <= PAIR_WINDOW), None)
            if not match:
                continue
            rows.append({
                "app": app,
                "title": title_of(record),
                "from": quality(record),
                "to": quality(match),
                "at": when,
            })
    rows.sort(key=lambda r: r["at"], reverse=True)
    return rows[:KEEP]


def main():
    rows = collect()
    body = [
        "# HELP arr_quality_change_timestamp Unix time a file was replaced by a different quality",
        "# TYPE arr_quality_change_timestamp gauge",
    ]
    for row in rows:
        labels = ",".join(f'{k}="{escape(str(row[k]))}"' for k in ("app", "title", "from", "to"))
        body.append(f"arr_quality_change_timestamp{{{labels}}} {int(row['at'].timestamp())}")
    body.append("# HELP arr_quality_changes_tracked How many upgrades this exporter is exposing")
    body.append("# TYPE arr_quality_changes_tracked gauge")
    body.append(f"arr_quality_changes_tracked {len(rows)}")
    body.append("")

    # PUT, not POST: replaces the whole group, so upgrades that fell out of the window disappear
    request = urllib.request.Request(f"{PUSHGATEWAY}/metrics/job/arr_history",
                                    data="\n".join(body).encode(), method="PUT")
    try:
        urllib.request.urlopen(request, timeout=10).close()
    except (urllib.error.URLError, OSError) as exc:
        sys.exit(f"pushgateway unreachable: {exc}")


if __name__ == "__main__":
    main()
