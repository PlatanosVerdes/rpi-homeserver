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
# Cumulative totals and "waiting since" live here: Pushgateway is replaced on every push, so it
# cannot be the thing that remembers. In appdata, so the daily backup carries it.
STATE_FILE = Path(os.environ.get("SEED_STATE", PROJECT_DIR / "appdata/seed-cleanup/state.json"))
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
    # A dry run is an inspection, so it must not move the numbers the panels and the staleness
    # alert read: it would report zero removals and a fresh run that never happened.
    if DRY_RUN:
        return
    request = urllib.request.Request(f"{PUSHGATEWAY}/metrics/job/{job}",
                                     data=("\n".join(lines) + "\n").encode(), method="PUT")
    try:
        urllib.request.urlopen(request, timeout=10).close()
    except (urllib.error.URLError, OSError) as exc:
        failures.append(f"pushgateway unreachable: {exc}")


def escape(value):
    return str(value).replace("\\", "\\\\").replace('"', '\\"').replace("\n", " ")


def read_state():
    try:
        return json.loads(STATE_FILE.read_text())
    except (OSError, ValueError):
        return {"removed_total": 0, "freed_bytes_total": 0, "waiting_since": {}}


def write_state(state):
    if DRY_RUN:
        return
    try:
        STATE_FILE.parent.mkdir(parents=True, exist_ok=True)
        STATE_FILE.write_text(json.dumps(state, indent=1))
    except OSError as exc:
        failures.append(f"state file: {exc}")


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
    return goal_progress(torrent, goal) >= 100


def goal_progress(torrent, goal):
    """How far along the goal a torrent is, 0-100.

    Either hours or ratio clears the debt, so the progress is whichever is further along: a torrent
    at 20% of the hours and 80% of the ratio is 80% done, not 50%. Reporting the hours alone would
    say "199 h to go" about something a busy evening of upload could finish tonight.
    """
    min_hours = goal.get("min_seed_hours", 0)
    min_ratio = goal.get("min_ratio", 0)
    if min_hours <= 0 and min_ratio <= 0:
        return 100.0
    by_hours = (torrent["seeding_time"] / 3600) / min_hours * 100 if min_hours > 0 else 0
    by_ratio = torrent["ratio"] / min_ratio * 100 if min_ratio > 0 else 0
    return min(100.0, max(by_hours, by_ratio))


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
    """Split into (remove, waiting, in_library, keep) with the reason each one landed there.

    in_library is decided by the link count alone, not by the guards, so a film that was imported an
    hour ago still counts as being in the library. Ordering it after the guards made the funnel on
    the dashboard not add up: a fresh import belonged to no stage at all.
    """
    shared = collections.Counter(t["content_path"] for t in torrents)
    min_age_hours = rules.get("min_age_hours", 24)
    remove, waiting, in_library, keep = [], [], [], []

    for t in torrents:
        entry = dict(t)
        entry["tracker_host"] = tracker_host(t)
        name, goal = goal_for(t, rules)
        entry["goal_name"] = name
        entry["goal"] = goal

        counts = link_counts(host_path(t["content_path"], data_root))
        if counts is not None and max(counts) > 1:
            in_library.append({**entry, "why": "still in the library"})
            keep.append(in_library[-1])
            continue

        if t["progress"] < 1 or t["state"] in BUSY_STATES:
            keep.append({**entry, "why": "not finished seeding-ready"})
            continue
        if too_young(t, min_age_hours):
            keep.append({**entry, "why": "finished too recently, Radarr may still import it"})
            continue
        if shared[t["content_path"]] > 1:
            keep.append({**entry, "why": "another torrent shares these files"})
            continue
        if counts is None:
            keep.append({**entry, "why": "files not readable from the host"})
            continue

        if goal_met(t, goal):
            remove.append({**entry, "why": "watched and tracker paid"})
        else:
            waiting.append({**entry, "why": "watched, seed goal pending"})

    return remove, waiting, in_library, keep


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


def gauge(name, help_text, samples):
    """samples is a list of (labels-or-None, value)."""
    lines = [f"# HELP {name} {help_text}", f"# TYPE {name} gauge"]
    for labels, value in samples:
        lines.append(f"{name}{{{labels}}} {value}" if labels else f"{name} {value}")
    return lines


def metrics(remove, waiting, in_library, freed, status, state, torrents, rules):
    def per_torrent(items, value):
        out = []
        for t in items:
            labels = f'name="{escape(t["name"])[:90]}",tracker="{escape(t["tracker_host"])}"'
            out.append((labels, value(t)))
        return out

    lines = []
    lines += gauge("seed_cleanup_last_run_timestamp", "When the cleanup last ran",
                   [(None, int(time.time()))])
    lines += gauge("seed_cleanup_last_status", "Last run status (0=ok, 1=error)",
                   [(None, status)])
    lines += gauge("seed_cleanup_removed_torrents", "Torrents removed on the last run",
                   [(None, len(remove))])
    lines += gauge("seed_cleanup_freed_bytes", "Bytes freed on the last run", [(None, freed)])
    lines += gauge("seed_cleanup_removed_total", "Torrents removed since this started counting",
                   [(None, state["removed_total"])])
    lines += gauge("seed_cleanup_freed_bytes_total", "Bytes freed since this started counting",
                   [(None, state["freed_bytes_total"])])
    lines += gauge("seed_cleanup_waiting_torrents", "Watched torrents still owing seed time",
                   [(None, len(waiting))])
    lines += gauge("seed_cleanup_waiting_bytes", "Bytes held by torrents still owing seed time",
                   [(None, sum(t["size"] for t in waiting))])
    lines += gauge("seed_cleanup_library_torrents", "Torrents whose film is still in the library",
                   [(None, len(in_library))])
    lines += gauge("seed_cleanup_library_bytes", "Bytes shared with the library",
                   [(None, sum(t["size"] for t in in_library))])

    lines += gauge("seed_cleanup_waiting_seed_hours", "Seeding hours done, per waiting torrent",
                   [(f'name="{escape(t["name"])[:90]}",tracker="{escape(t["tracker_host"])}",'
                     f'goal_hours="{t["goal"].get("min_seed_hours", 0)}"',
                     round(t["seeding_time"] / 3600, 2)) for t in waiting])
    lines += gauge("seed_cleanup_waiting_size_bytes", "Size per waiting torrent",
                   per_torrent(waiting, lambda t: t["size"]))
    lines += gauge("seed_cleanup_waiting_hours_remaining",
                   "Seeding hours still owed, per waiting torrent",
                   per_torrent(waiting, lambda t: round(
                       max(0.0, t["goal"].get("min_seed_hours", 0) - t["seeding_time"] / 3600), 2)))
    lines += gauge("seed_cleanup_waiting_ratio", "Share ratio per waiting torrent",
                   per_torrent(waiting, lambda t: round(t["ratio"], 3)))
    lines += gauge("seed_cleanup_waiting_percent",
                   "How far along its goal each waiting torrent is, by hours or by ratio",
                   per_torrent(waiting, lambda t: round(goal_progress(t, t["goal"]), 1)))
    lines += gauge("seed_cleanup_waiting_since_timestamp",
                   "When the film left the library, per waiting torrent",
                   per_torrent(waiting, lambda t: state["waiting_since"].get(t["hash"], 0)))

    trackers = {}
    for t in torrents:
        host = tracker_host(t)
        name, goal = goal_for(t, rules)
        entry = trackers.setdefault(host, {"private": bool(t.get("private")), "goal": goal,
                                           "count": 0, "bytes": 0, "waiting": 0})
        entry["count"] += 1
        entry["bytes"] += t["size"]
    for t in waiting:
        if t["tracker_host"] in trackers:
            trackers[t["tracker_host"]]["waiting"] += 1

    def per_tracker(value):
        return [(f'tracker="{escape(host)}",private="{str(e["private"]).lower()}"', value(e))
                for host, e in sorted(trackers.items())]

    lines += gauge("seed_cleanup_goal_hours", "Seeding hours this tracker's torrents must do",
                   per_tracker(lambda e: e["goal"].get("min_seed_hours", 0)))
    lines += gauge("seed_cleanup_goal_ratio", "Ratio that also clears the goal, 0 if none",
                   per_tracker(lambda e: e["goal"].get("min_ratio", 0)))
    lines += gauge("seed_cleanup_tracker_torrents", "Torrents per tracker",
                   per_tracker(lambda e: e["count"]))
    lines += gauge("seed_cleanup_tracker_bytes", "Bytes per tracker",
                   per_tracker(lambda e: e["bytes"]))
    lines += gauge("seed_cleanup_tracker_waiting", "Torrents owing seed time, per tracker",
                   per_tracker(lambda e: e["waiting"]))
    push("seed_cleanup", lines)


def main():
    try:
        rules = json.loads(RULES_FILE.read_text())
    except (OSError, ValueError) as exc:
        sys.exit(f"cannot read {RULES_FILE}: {exc}")

    state = read_state()

    try:
        torrents = json.loads(qbit("torrents/info"))
    except Exception as exc:
        metrics([], [], [], 0, 1, state, [], rules)
        sys.exit(f"qbittorrent: {exc}")

    remove, waiting, in_library, _ = classify(torrents, rules, env("DATA_ROOT", "/mnt/data"))

    report("would remove" if DRY_RUN else "removing", remove)
    report("waiting on seed", waiting)

    freed = delete(remove)
    apply_tag(waiting, WAITING_TAG, add=True)
    apply_tag([t for t in torrents if WAITING_TAG in (t.get("tags") or "")
               and t["hash"] not in {w["hash"] for w in waiting}], WAITING_TAG, add=False)

    # First time each one is seen owing seed time, so the dashboard can say how long it has been
    # stuck. Hashes that stopped waiting are dropped rather than kept forever.
    now = int(time.time())
    state["waiting_since"] = {t["hash"]: state["waiting_since"].get(t["hash"], now)
                              for t in waiting}
    if freed:
        state["removed_total"] += len(remove)
        state["freed_bytes_total"] += freed
    write_state(state)

    metrics(remove, waiting, in_library, freed, 1 if failures else 0, state, torrents, rules)
    if failures:
        sys.exit("; ".join(failures))
    if remove and not DRY_RUN:
        print(f"freed {freed / 1e9:.1f} GB")


if __name__ == "__main__":
    main()
