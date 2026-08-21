#!/usr/bin/env python3
"""Move the knobs that decide how hard a tracker is worked, from the one number that matters.

`tracker-stats.py` reads the site and writes the headroom: how many GB of *paid* downloads still fit
before the account crosses the ratio the site disables it at. That number picks a tier, and the tier
sets three things:

  Prowlarr  the indexer's own "Search freeleech only" filter, which every app inherits
  Radarr    requiredFlags = [1] (freeleech only) or [] (anything)
  autobrr   how many freeleech grabs a day the ratio builder may take

**The filter belongs in Prowlarr, not in the *arrs.** `requiredFlags` exists on Radarr's Torznab
indexer and not on Sonarr's, so filtering there would leave series unprotected: with 19 GB of
headroom one 20 GB season pack that is not freeleech is the difference between ratio 0.52 and 0.397,
and 0.397 is a disabled account. TorrentLeech's Cardigann definition carries a `freeleech` checkbox
that adds the site's own FREELEECH facet to every query, so switching it there covers Radarr, Sonarr
and anything else that searches through Prowlarr, and nobody loses an indexer.

Nothing is written when the desired value is already in place, so this is silent on almost every run
and only announces the crossings. It refuses to act on a stale reading: acting on last week's ratio
is worse than not acting.

Run from cron a few minutes after tracker-stats.py (see scripts/crontab).
"""

import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request
from datetime import datetime, timezone
from pathlib import Path

PROJECT_DIR = Path(os.path.expanduser("~/rpi-homeserver"))
PUSHGATEWAY = "http://localhost:9091"
RULES_FILE = Path(os.environ.get("TRACKER_RULES", PROJECT_DIR / "config/trackers/rules.json"))
STATE_DIR = Path(os.environ.get("TRACKER_STATE", PROJECT_DIR / "appdata/tracker-stats"))
ENV_FILE = PROJECT_DIR / ".env"
DRY_RUN = os.environ.get("DRY_RUN") == "1"
MAX_READING_AGE = 3 * 3600         # a reading older than this is not something to act on
FREELEECH_FLAG = 1                 # G_Freeleech, in Radarr's indexer flags
ARRS = {"radarr": 7878, "sonarr": 8989}
PROWLARR = "http://localhost:9696"
DATA_ROOT = Path(os.environ.get("DATA_ROOT", "/mnt/data"))
# The list fields autobrr's own API rejects a filter for omitting, even when they are empty.
AUTOBRR_LISTS = ("resolutions", "sources", "codecs", "containers", "match_hdr", "except_hdr",
                 "match_other", "except_other", "origins", "except_origins", "formats", "quality",
                 "media", "match_release_types", "match_languages", "except_languages")

failures, changes = [], []


def env(name, default=None):
    if ENV_FILE.is_file():
        for line in ENV_FILE.read_text(errors="ignore").splitlines():
            if line.startswith(f"{name}=") and not line.startswith("#"):
                return line.split("=", 1)[1].strip().strip('"').strip("'")
    return default


def prowlarr_key():
    text = (PROJECT_DIR / "appdata/prowlarr/config.xml").read_text(errors="ignore")
    return text.split("<ApiKey>")[1].split("</ApiKey>")[0]


def arr_key(app):
    text = (PROJECT_DIR / "appdata" / app / "config.xml").read_text(errors="ignore")
    return text.split("<ApiKey>")[1].split("</ApiKey>")[0]


def call(url, key=None, headers=None, body=None, method="GET"):
    data = json.dumps(body).encode() if body is not None else None
    head = {"Content-Type": "application/json"}
    if key:
        head["X-Api-Key"] = key
    head.update(headers or {})
    request = urllib.request.Request(url, data=data, headers=head, method=method)
    with urllib.request.urlopen(request, timeout=30) as response:
        raw = response.read()
    return json.loads(raw) if raw[:1] in (b"{", b"[") else None


def tier_for(config, headroom_gb):
    for tier in config.get("tiers") or []:
        ceiling = tier.get("headroom_gb_below")
        if ceiling is None or headroom_gb < ceiling:
            return tier
    return {}


def arr_indexer(app, needle):
    key = arr_key(app)
    indexers = call(f"http://localhost:{ARRS[app]}/api/v3/indexer", key)
    for indexer in indexers:
        if needle.lower() in indexer["name"].lower():
            return key, indexer
    raise RuntimeError(f"{app} has no indexer matching {needle}")


def set_required_flags(app, needle, freeleech_only):
    """Radarr can be told to take freeleech only, which is the whole point of having the flag."""
    key, indexer = arr_indexer(app, needle)
    fields = indexer.get("fields") or []
    field = next((f for f in fields if f["name"] == "requiredFlags"), None)
    if field is None:
        raise RuntimeError(f"{app} indexer {indexer['name']} has no requiredFlags field")
    want = [FREELEECH_FLAG] if freeleech_only else []
    if (field.get("value") or []) == want:
        return
    field["value"] = want
    if DRY_RUN:
        changes.append(f"[dry-run] {app}: requiredFlags -> {want or 'anything'}")
        return
    call(f"http://localhost:{ARRS[app]}/api/v3/indexer/{indexer['id']}?forceSave=true",
         key, body=indexer, method="PUT")
    changes.append(f"{app}: {indexer['name']} -> "
                   f"{'freeleech only' if freeleech_only else 'any torrent'}")


def set_prowlarr_freeleech(needle, only):
    """The one switch every app inherits: the site's FREELEECH facet, applied to every query."""
    key = prowlarr_key()
    indexers = call(f"{PROWLARR}/api/v1/indexer", key)
    indexer = next((i for i in indexers if needle.lower() in i["name"].lower()), None)
    if indexer is None:
        raise RuntimeError(f"prowlarr has no indexer matching {needle}")
    field = next((f for f in indexer.get("fields") or [] if f["name"] == "freeleech"), None)
    if field is None:
        raise RuntimeError(f"prowlarr indexer {indexer['name']} has no freeleech filter")
    if bool(field.get("value")) == only:
        return
    field["value"] = only
    if DRY_RUN:
        changes.append(f"[dry-run] prowlarr: freeleech only -> {only}")
        return
    call(f"{PROWLARR}/api/v1/indexer/{indexer['id']}?forceSave=true", key, body=indexer,
         method="PUT")
    changes.append(f"prowlarr: {indexer['name']} -> "
                   f"{'freeleech results only' if only else 'all results'}")


def set_indexer_enabled(app, needle, enabled):
    """Kept to put an indexer back: taking one away was the wrong answer to a filtering problem."""
    key, indexer = arr_indexer(app, needle)
    switches = ["enableRss", "enableAutomaticSearch", "enableInteractiveSearch"]
    if all(bool(indexer.get(name)) == enabled for name in switches):
        return
    for name in switches:
        indexer[name] = enabled
    if DRY_RUN:
        changes.append(f"[dry-run] {app}: {indexer['name']} -> {'on' if enabled else 'off'}")
        return
    call(f"http://localhost:{ARRS[app]}/api/v3/indexer/{indexer['id']}?forceSave=true",
         key, body=indexer, method="PUT")
    changes.append(f"{app}: {indexer['name']} -> {'enabled' if enabled else 'disabled'}")


def autobrr(path, body=None, method="GET"):
    key = env("AUTOBRR_API_KEY")
    if not key:
        raise RuntimeError("no AUTOBRR_API_KEY in .env")
    # autobrr is not published on the host; Caddy routes it by name.
    return call(f"http://localhost/api/{path}", headers={"X-API-Token": key, "Host": "autobrr"},
                body=body, method=method)


def free_gb():
    stat = os.statvfs(DATA_ROOT)
    return stat.f_bavail * stat.f_frsize / 1024 ** 3


def grabber_enabled(filter_name):
    current = next((f for f in autobrr("filters") if f["name"] == filter_name), None)
    return bool(current and current.get("enabled"))


def set_grab_rate(filter_name, per_day, enabled=True):
    current = next((f for f in autobrr("filters") if f["name"] == filter_name), None)
    if current is None:
        raise RuntimeError(f"autobrr has no filter named {filter_name}")
    if (current.get("max_downloads") == per_day
            and current.get("max_downloads_unit") == "DAY"
            and bool(current.get("enabled")) == enabled):
        return
    if DRY_RUN:
        changes.append(f"[dry-run] autobrr: {filter_name} -> {per_day}/day")
        return
    body = autobrr(f"filters/{current['id']}")
    body["max_downloads"], body["max_downloads_unit"] = per_day, "DAY"
    body["enabled"] = enabled
    # The update rejects nulls, stores no actions, and needs the indexer link resent.
    body["indexers"] = [{"id": i["id"]} for i in body.get("indexers") or []]
    body.pop("actions", None)
    for key in [k for k, v in body.items() if v is None]:
        del body[key]
    for key in AUTOBRR_LISTS:
        body.setdefault(key, [])
    autobrr(f"filters/{current['id']}", body=body, method="PUT")
    changes.append(f"autobrr: {filter_name} -> "
                   f"{f'{per_day} grabs/day' if enabled else 'paused, disk is full'}")


def push(job, lines):
    if DRY_RUN:
        return
    request = urllib.request.Request(f"{PUSHGATEWAY}/metrics/job/{job}",
                                     data=("\n".join(lines) + "\n").encode(), method="PUT")
    try:
        urllib.request.urlopen(request, timeout=10).close()
    except (urllib.error.URLError, OSError) as exc:
        failures.append(f"{job}: pushgateway unreachable: {exc}")


def telegram(text):
    token, chat = env("TELEGRAM_ALERT_BOT_TOKEN"), env("TELEGRAM_ALERT_CHAT_ID")
    if not token or not chat:
        failures.append("no telegram credentials in .env")
        return
    data = urllib.parse.urlencode({"chat_id": chat, "text": text, "parse_mode": "HTML"}).encode()
    try:
        urllib.request.urlopen(f"https://api.telegram.org/bot{token}/sendMessage",
                               data=data, timeout=15).close()
    except Exception as exc:
        failures.append(f"telegram failed: {exc}")


def main():
    rules = json.loads(RULES_FILE.read_text())
    state_file = STATE_DIR / "state.json"
    if not state_file.is_file():
        print("no reading from tracker-stats.py yet", file=sys.stderr)
        return 1
    state = json.loads(state_file.read_text())
    age = datetime.now(timezone.utc).timestamp() - state.get("read_at", 0)
    if age > MAX_READING_AGE:
        print(f"the tracker reading is {age / 3600:.1f} h old, not acting on it", file=sys.stderr)
        return 1

    lines = ["# HELP tracker_tier_grabs_per_day Freeleech grabs a day the current tier allows",
             "# TYPE tracker_tier_grabs_per_day gauge",
             "# HELP tracker_grabber_paused_no_disk 1 while the grabber is off for lack of space",
             "# TYPE tracker_grabber_paused_no_disk gauge",
             "# HELP tracker_tier_freeleech_only 1 while the *arrs may only take freeleech",
             "# TYPE tracker_tier_freeleech_only gauge"]

    for tracker, config in rules.items():
        numbers = (state.get("trackers") or {}).get(tracker)
        if not numbers:
            failures.append(f"{tracker}: nothing read")
            continue
        headroom_gb = numbers["headroom_bytes"] / 1024 ** 3
        tier = tier_for(config, headroom_gb)
        if not tier:
            failures.append(f"{tracker}: no tier matches {headroom_gb:.1f} GB")
            continue
        freeleech_only = bool(tier.get("freeleech_only"))
        per_day = tier.get("grabs_per_day")
        needle = config.get("prowlarr_indexer", tracker)

        # A grab cannot be deleted for the length of the hit & run window, so the rate is a disk
        # budget: below the floor the grabber pauses whatever the ratio says, because filling the
        # array would break imports for everything and none of it could be freed early anyway.
        space, floor = free_gb(), config.get("min_free_gb", 0)
        # Hysteresis, or it flaps: space hovers at the floor while seed-cleanup frees torrents, and
        # every crossing would be a write and a Telegram message.
        paused_now = not bool(grabber_enabled(config["autobrr_filter"]))
        room = space >= (floor + 50 if paused_now else floor)
        if not room:
            failures.append(f"{tracker}: {space:.0f} GB free, under the {floor} GB floor, "
                            f"grabber paused")

        for action in (
            lambda: set_prowlarr_freeleech(needle, freeleech_only),
            lambda: set_required_flags("radarr", needle, freeleech_only),
            # TorrentLeech has series, and Prowlarr is where the filtering happens, so Sonarr keeps
            # the indexer at every tier.
            lambda: set_indexer_enabled("sonarr", needle, True),
            lambda: set_grab_rate(config["autobrr_filter"], per_day, room) if per_day else None,
        ):
            try:
                action()
            except Exception as exc:
                failures.append(f"{tracker}: {exc}")

        label = f'tracker="{tracker}"'
        lines += [f"tracker_tier_grabs_per_day{{{label}}} {per_day if room else 0}",
                  f"tracker_tier_freeleech_only{{{label}}} {int(freeleech_only)}",
                  f"tracker_grabber_paused_no_disk{{{label}}} {int(not room)}"]

        if changes:
            telegram(f"<b>{tracker}</b>: headroom {headroom_gb:.1f} GB, ratio "
                     f"{numbers['ratio']:.3f} (line {numbers['min_ratio']})\n" +
                     "\n".join(f"- {line}" for line in changes))
            print("\n".join(changes))
            changes.clear()

    lines.append("# HELP tracker_control_last_run_timestamp When the knobs were last checked")
    lines.append("# TYPE tracker_control_last_run_timestamp gauge")
    lines.append(f"tracker_control_last_run_timestamp "
                 f"{datetime.now(timezone.utc).timestamp():.0f}")
    push("tracker_control", lines)

    if failures:
        print("\n".join(failures), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
