#!/usr/bin/env python3
"""Remove a torrent and its data once the film has left the library and the tracker is paid.

Radarr imports by hardlink, so a downloaded file lives under two names: the one qBittorrent seeds
and the one Plex plays. That link count is the whole state machine:

  nlink >= 2   still in the library                  -> leave it, whatever the tracker says
  nlink == 1   watched, Maintainerr deleted its copy
                 public tracker                      -> remove torrent and data now
                 private, seed goal met              -> remove torrent and data now
                 private, seed goal pending          -> keep seeding, tag it, look again next hour

Neither half can do this alone: qBittorrent cannot know what was watched, and Maintainerr looks at a
torrent once, at the moment it deletes the film, and never comes back. So the disk carries the state
and this reads it.

Goals come from config/qbittorrent/seed-rules.json. Whether a tracker is private comes from
qBittorrent itself, so there is no list to keep up to date. Seeding time is qBittorrent's counter,
which only advances while the torrent really seeds: stricter than a tracker's calendar clock, which
is the safe direction to be wrong in.

Guards, because this deletes files: never with nlink > 1, never below 100% progress, never while
checking or moving, never when a second torrent shares the same files, and never within
min_age_hours of finishing. That last one matters: a torrent that just completed also has nlink 1,
because Radarr has not imported it yet. Without the floor, this would delete a download while
unpackerr was still extracting it.

DRY_RUN=1 reports and touches nothing. Metrics go to Pushgateway. Run from cron (scripts/crontab).
"""

import collections
import json
import os
import subprocess
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

PROJECT_DIR = Path(os.path.expanduser("~/rpi-homeserver"))
PUSHGATEWAY = "http://localhost:9091"
RULES_FILE = Path(os.environ.get("SEED_RULES", PROJECT_DIR / "config/qbittorrent/seed-rules.json"))
DRY_RUN = os.environ.get("DRY_RUN") == "1"
WAITING_TAG = "waiting-seed"
CONTAINER_DATA_ROOT = "/data/"

# States where the files are being read or moved. Deleting under any of them risks a half-written
# import or a recheck against files that are already gone.
BUSY_STATES = {"downloading", "stalledDL", "metaDL", "allocating", "checkingDL", "checkingUP",
               "checkingResumeData", "moving", "forcedDL", "queuedDL"}

failures = []


def env(name, default):
    path = PROJECT_DIR / ".env"
    if path.is_file():
        for line in path.read_text(errors="ignore").splitlines():
            if line.startswith(f"{name}="):
                return line.split("=", 1)[1].strip()
    return default


def qbit(endpoint, data=None):
    """The WebUI trusts localhost; from the host it arrives via the Docker gateway, which is not in
    its AuthSubnetWhitelist and would need credentials. Same trick as media-metrics.py."""
    command = ["docker", "exec", "qbittorrent", "curl", "-sf", "--max-time", "30",
               f"http://localhost:8080/api/v2/{endpoint}"]
    for key, value in (data or {}).items():
        command += ["--data-urlencode", f"{key}={value}"]
    result = subprocess.run(command, capture_output=True, text=True, timeout=60)
    if result.returncode != 0:
        raise RuntimeError(result.stderr.strip()[:120] or f"{endpoint} failed")
    return result.stdout


def push(job, lines):
    request = urllib.request.Request(f"{PUSHGATEWAY}/metrics/job/{job}",
                                     data=("\n".join(lines) + "\n").encode(), method="PUT")
    try:
        urllib.request.urlopen(request, timeout=10).close()
    except (urllib.error.URLError, OSError) as exc:
        failures.append(f"pushgateway unreachable: {exc}")


def escape(value):
    return str(value).replace("\\", "\\\\").replace('"', '\\"').replace("\n", " ")


def host_path(content_path, data_root):
    return content_path.replace(CONTAINER_DATA_ROOT, f"{data_root.rstrip('/')}/", 1)


def link_counts(path):
    """Every link count under the torrent's content path, or None when it cannot be read: an
    unreadable path is never a reason to delete."""
    try:
        if os.path.isfile(path):
            return [os.stat(path).st_nlink]
        if not os.path.isdir(path):
            return None
        counts = []
        for root, _, files in os.walk(path):
            for name in files:
                try:
                    counts.append(os.stat(os.path.join(root, name)).st_nlink)
                except OSError:
                    return None
        return counts or None
    except OSError:
        return None


def goal_for(torrent, rules):
    tracker = tracker_host(torrent)
    if not torrent.get("private"):
        return "public", rules.get("public", {})
    override = (rules.get("trackers") or {}).get(tracker)
    return tracker, override or rules.get("private", {})


def tracker_host(torrent):
    url = torrent.get("tracker") or ""
    without_scheme = url.split("://", 1)[-1]
    return without_scheme.split("/", 1)[0].split(":", 1)[0] or "(no tracker)"


def goal_met(torrent, goal):
    hours = torrent["seeding_time"] / 3600
    min_hours = goal.get("min_seed_hours", 0)
    min_ratio = goal.get("min_ratio", 0)
    if min_hours <= 0 and min_ratio <= 0:
        return True
    return (min_hours > 0 and hours >= min_hours) or (min_ratio > 0 and torrent["ratio"] >= min_ratio)


def too_young(torrent, min_age_hours):
    """Hours since the torrent finished. An unknown timestamp counts as too young: this never
    deletes something it cannot date."""
    if min_age_hours <= 0:
        return False
    finished = torrent.get("completion_on") or torrent.get("added_on") or 0
    if finished <= 0:
        return True
    return (time.time() - finished) < min_age_hours * 3600


def classify(torrents, rules, data_root):
    """Split into (remove, waiting, keep) with the reason each one landed there."""
    shared = collections.Counter(t["content_path"] for t in torrents)
    min_age_hours = rules.get("min_age_hours", 24)
    remove, waiting, keep = [], [], []

    for t in torrents:
        entry = dict(t)
        entry["tracker_host"] = tracker_host(t)
        name, goal = goal_for(t, rules)
        entry["goal_name"] = name
        entry["goal"] = goal

        if t["progress"] < 1 or t["state"] in BUSY_STATES:
            keep.append({**entry, "why": "not finished seeding-ready"})
            continue
        if too_young(t, min_age_hours):
            keep.append({**entry, "why": "finished too recently, Radarr may still import it"})
            continue
        if shared[t["content_path"]] > 1:
            keep.append({**entry, "why": "another torrent shares these files"})
            continue

        counts = link_counts(host_path(t["content_path"], data_root))
        if counts is None:
            keep.append({**entry, "why": "files not readable from the host"})
            continue
        if max(counts) > 1:
            keep.append({**entry, "why": "still in the library"})
            continue

        if goal_met(t, goal):
            remove.append({**entry, "why": "watched and tracker paid"})
        else:
            waiting.append({**entry, "why": "watched, seed goal pending"})

    return remove, waiting, keep


def apply_tag(torrents, tag, add):
    if not torrents or DRY_RUN:
        return
    endpoint = "torrents/addTags" if add else "torrents/removeTags"
    try:
        qbit(endpoint, {"hashes": "|".join(t["hash"] for t in torrents), "tags": tag})
    except Exception as exc:
        failures.append(f"tagging: {exc}")


def delete(torrents):
    if not torrents or DRY_RUN:
        return 0
    try:
        qbit("torrents/delete", {"hashes": "|".join(t["hash"] for t in torrents),
                                 "deleteFiles": "true"})
        return sum(t["size"] for t in torrents)
    except Exception as exc:
        failures.append(f"delete: {exc}")
        return 0


def report(label, torrents):
    if not torrents:
        return
    total = sum(t["size"] for t in torrents) / 1e9
    print(f"{label}: {len(torrents)} torrents, {total:.1f} GB")
    for t in sorted(torrents, key=lambda x: -x["size"]):
        hours = t["seeding_time"] / 3600
        print(f"  {t['name'][:60]:62} {t['goal_name'][:22]:24} "
              f"seed={hours:6.1f}h ratio={t['ratio']:.2f} {t['size'] / 1e9:5.1f}GB")


def metrics(remove, waiting, freed, status):
    lines = [
        "# HELP seed_cleanup_last_run_timestamp When the cleanup last ran",
        "# TYPE seed_cleanup_last_run_timestamp gauge",
        f"seed_cleanup_last_run_timestamp {int(time.time())}",
        "# HELP seed_cleanup_last_status Last run status (0=ok, 1=error)",
        "# TYPE seed_cleanup_last_status gauge",
        f"seed_cleanup_last_status {status}",
        "# HELP seed_cleanup_removed_torrents Torrents removed on the last run",
        "# TYPE seed_cleanup_removed_torrents gauge",
        f"seed_cleanup_removed_torrents {len(remove)}",
        "# HELP seed_cleanup_freed_bytes Bytes freed on the last run",
        "# TYPE seed_cleanup_freed_bytes gauge",
        f"seed_cleanup_freed_bytes {freed}",
        "# HELP seed_cleanup_waiting_torrents Watched torrents still owing seed time",
        "# TYPE seed_cleanup_waiting_torrents gauge",
        f"seed_cleanup_waiting_torrents {len(waiting)}",
        "# HELP seed_cleanup_waiting_bytes Bytes held by torrents still owing seed time",
        "# TYPE seed_cleanup_waiting_bytes gauge",
        f"seed_cleanup_waiting_bytes {sum(t['size'] for t in waiting)}",
        "# HELP seed_cleanup_waiting_seed_hours Seeding hours done, per waiting torrent",
        "# TYPE seed_cleanup_waiting_seed_hours gauge",
    ]
    for t in waiting:
        labels = (f'name="{escape(t["name"])[:90]}",tracker="{escape(t["tracker_host"])}",'
                  f'goal_hours="{t["goal"].get("min_seed_hours", 0)}"')
        lines.append(f"seed_cleanup_waiting_seed_hours{{{labels}}} "
                     f"{round(t['seeding_time'] / 3600, 2)}")
    lines += ["# HELP seed_cleanup_waiting_size_bytes Size per waiting torrent",
              "# TYPE seed_cleanup_waiting_size_bytes gauge"]
    for t in waiting:
        labels = (f'name="{escape(t["name"])[:90]}",tracker="{escape(t["tracker_host"])}"')
        lines.append(f"seed_cleanup_waiting_size_bytes{{{labels}}} {t['size']}")
    push("seed_cleanup", lines)


def main():
    try:
        rules = json.loads(RULES_FILE.read_text())
    except (OSError, ValueError) as exc:
        sys.exit(f"cannot read {RULES_FILE}: {exc}")

    try:
        torrents = json.loads(qbit("torrents/info"))
    except Exception as exc:
        metrics([], [], 0, 1)
        sys.exit(f"qbittorrent: {exc}")

    remove, waiting, _ = classify(torrents, rules, env("DATA_ROOT", "/mnt/data"))

    report("would remove" if DRY_RUN else "removing", remove)
    report("waiting on seed", waiting)

    freed = delete(remove)
    apply_tag(waiting, WAITING_TAG, add=True)
    apply_tag([t for t in torrents if WAITING_TAG in (t.get("tags") or "")
               and t["hash"] not in {w["hash"] for w in waiting}], WAITING_TAG, add=False)

    metrics(remove, waiting, freed, 1 if failures else 0)
    if failures:
        sys.exit("; ".join(failures))
    if remove and not DRY_RUN:
        print(f"freed {freed / 1e9:.1f} GB")


if __name__ == "__main__":
    main()
