#!/usr/bin/env python3
"""Read what a private tracker says about the account, which is the only number that decides anything.

The client's own byte counters are not the account. They reset when a torrent is removed, they know
nothing about freeleech, and trusting them is what made DigitalCore look doomed on a day its site
showed ratio 2.76. So this logs in and reads the site.

TorrentLeech disables an account whose global ratio drops under 0.4: the warning gives seven days
and then an automatic re-check. The useful number is therefore not the ratio, which says nothing
about how much room is left, but the headroom before that line:

    buffer   = uploaded - min_ratio x downloaded
    headroom = buffer / min_ratio        # GB of non-freeleech downloads that still fit

Freeleech never moves `downloaded`, which is why the grabber can run at any ratio and why headroom,
not ratio, is what decides whether the *arrs may take a paid torrent.

Hit & run is measured here instead of scraped, because the client knows it sooner: a torrent clears
by seeding 240 h or reaching ratio 1.0, and both are in `torrents/info` before the site recomputes.

Credentials come from Prowlarr, which already holds them for the same site, so no secret is
duplicated. The session cookie is kept on disk and only replaced when it stops working: this is a
tracker that banned 500 users for hammering its IRC, and there is no reason to hand it 48 logins a
day for a number that moves in gigabytes.

Run from cron every 30 minutes (see scripts/crontab). Silent unless something fails.
"""

import html
import http.cookiejar
import json
import os
import re
import subprocess
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
PROWLARR = "http://localhost:9696"
UA = ("Mozilla/5.0 (X11; Linux aarch64) AppleWebKit/537.36 (KHTML, like Gecko) "
      "Chrome/126.0.0.0 Safari/537.36")
# A torrent in one of these is not announcing, so its seeding clock is not running.
STOPPED_STATES = {"pausedUP", "pausedDL", "stoppedUP", "stoppedDL", "error", "missingFiles",
                  "unknown"}
UNITS = {"B": 1, "KB": 1024, "MB": 1024 ** 2, "GB": 1024 ** 3, "TB": 1024 ** 4, "PB": 1024 ** 5}

failures = []


def prowlarr_key():
    text = (PROJECT_DIR / "appdata/prowlarr/config.xml").read_text(errors="ignore")
    return text.split("<ApiKey>")[1].split("</ApiKey>")[0]


def credentials(indexer_name):
    """Whatever Prowlarr uses to log into the same site. One place for the password, not two."""
    request = urllib.request.Request(f"{PROWLARR}/api/v1/indexer",
                                     headers={"X-Api-Key": prowlarr_key()})
    with urllib.request.urlopen(request, timeout=20) as response:
        indexers = json.loads(response.read())
    for indexer in indexers:
        if indexer["name"].lower() == indexer_name.lower():
            fields = {f["name"]: f.get("value") for f in indexer.get("fields") or []}
            return fields.get("username"), fields.get("password"), fields.get("alt2fatoken") or ""
    raise RuntimeError(f"no indexer named {indexer_name} in prowlarr")


def opener_for(tracker):
    STATE_DIR.mkdir(parents=True, exist_ok=True)
    jar_path = STATE_DIR / f"{tracker}-cookies.txt"
    jar = http.cookiejar.MozillaCookieJar(str(jar_path))
    if jar_path.is_file():
        try:
            jar.load(ignore_discard=True, ignore_expires=True)
        except Exception:
            jar_path.unlink()
    opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
    opener.addheaders = [("User-Agent", UA), ("Accept-Language", "en-US,en;q=0.9")]
    return opener, jar


def flatten(page):
    """The profile is a table of label/value pairs, and every value sits on the line after its
    label once the markup is gone. Parsing that beats a selector per number on a site whose markup
    is rebuilt by JS."""
    text = re.sub(r"<script.*?</script>", " ", page, flags=re.S)
    text = re.sub(r"<[^>]+>", "\n", text)
    return [html.unescape(line).strip() for line in text.split("\n") if line.strip()]


def value_after(lines, label):
    wanted = label.lower().rstrip(":")
    for index, line in enumerate(lines):
        if line.lower().rstrip(":") == wanted and index + 1 < len(lines):
            return lines[index + 1]
    return None


def to_bytes(text):
    match = re.match(r"([\d.,]+)\s*([KMGTP]?B)", (text or "").strip(), re.I)
    if not match:
        return None
    number = match.group(1).replace(",", ".")
    return float(number) * UNITS[match.group(2).upper()]


def to_float(text):
    if not text:
        return None
    cleaned = text.strip().replace(",", ".")
    match = re.match(r"-?[\d.]+", cleaned)
    return float(match.group()) if match else None


def profile(tracker, config):
    """Fetch the profile, logging in only when the stored cookie has stopped working."""
    user, password, token = credentials(config.get("prowlarr_indexer", tracker))
    if not user or not password:
        raise RuntimeError("prowlarr holds no credentials for this site")
    opener, jar = opener_for(tracker)
    site = config["site"].rstrip("/")
    url = f"{site}/profile/{urllib.parse.quote(user)}/view"

    def read():
        with opener.open(url, timeout=30) as response:
            return response.read().decode("utf-8", "replace")

    try:
        page = read()
    except Exception:
        page = ""
    if "account/logout" not in page:
        form = urllib.parse.urlencode({"username": user, "password": password,
                                       "alt2FAToken": token}).encode()
        with opener.open(f"{site}/user/account/login/", form, timeout=30) as response:
            landing = response.read().decode("utf-8", "replace")
        if "account/logout" not in landing:
            raise RuntimeError("login rejected")
        jar.save(ignore_discard=True, ignore_expires=True)
        page = read()

    lines = flatten(page)
    stats = {
        "uploaded": to_bytes(value_after(lines, "uploaded")),
        "downloaded": to_bytes(value_after(lines, "downloaded")),
        "ratio": to_float(value_after(lines, "ratio")),
        "points": to_float(value_after(lines, "TL Points")),
        "class": value_after(lines, "Class"),
        "warned_until": value_after(lines, "Warned until"),
    }
    if stats["uploaded"] is None or stats["downloaded"] is None:
        raise RuntimeError("logged in but the profile held no byte counters")
    return stats


def qbit(endpoint):
    """Same trick as the other scripts: from the host the WebUI would need credentials, from inside
    the container it trusts localhost."""
    command = ["docker", "exec", "qbittorrent", "curl", "-sf", "--max-time", "30",
               f"http://localhost:8080/api/v2/{endpoint}"]
    result = subprocess.run(command, capture_output=True, text=True, timeout=60)
    if result.returncode != 0:
        raise RuntimeError(result.stderr.strip()[:120] or f"{endpoint} failed")
    return json.loads(result.stdout)


def hit_and_run(config, torrents):
    """Per torrent, not per account: one uncleared torrent is a warning, three is a disabled account.

    A torrent clears by seeding min_seed_hours or reaching min_ratio, so what is owed is the hours
    still to go on the ones that have neither.
    """
    rule = config.get("hit_and_run") or {}
    min_hours = rule.get("min_seed_hours", 240)
    min_ratio = rule.get("min_ratio", 1.0)
    hosts = set(config.get("tracker_hosts") or [])
    pending, worst_hours = [], 0.0
    for torrent in torrents:
        url = torrent.get("tracker") or ""
        host = url.split("://", 1)[-1].split("/", 1)[0].split(":", 1)[0]
        if host not in hosts:
            continue
        # The obligation starts when the download finishes, so something still downloading owes
        # nothing yet. Counting it made the worst-case number read 240 h the moment a grab arrived.
        if torrent.get("progress", 0) < 1:
            continue
        hours = torrent.get("seeding_time", 0) / 3600
        if torrent.get("ratio", 0) >= min_ratio or hours >= min_hours:
            continue
        left = min_hours - hours
        # Owing hours is normal. Owing them while the clock is stopped is what gets an account
        # disabled: the site counts seeding time from announces, and a paused or broken torrent
        # does not announce.
        stopped = torrent.get("state") in STOPPED_STATES
        pending.append((torrent.get("name", "?"), left, torrent.get("ratio", 0), stopped))
        worst_hours = max(worst_hours, left)
    return pending, worst_hours


def tracker_host(torrent):
    url = torrent.get("tracker") or ""
    return url.split("://", 1)[-1].split("/", 1)[0].split(":", 1)[0]


def client_side(config, torrents):
    """What the client knows per tracker, which is every tracker, not only the ones that log in.

    Three of the five accounts here cannot be read (an API key that blocks profile data, a captcha,
    a site in maintenance), so without this a dashboard has one populated row and four empty ones.
    None of these numbers is the tracker's own accounting, and they are labelled `source="client"`
    for exactly that reason.

    The leech-bonus estimate applies DigitalCore's published formula to what is actually seeding:
    1% per 10 GB, only 50 GiB counted per torrent, scaled by `1 + (1/seeders)` for scarcity. It is an
    estimate of a number only that site can compute, and it is the only way to see the effect of a
    cross-seed the day it lands.
    """
    hosts = set(config.get("tracker_hosts") or [])
    rows = [t for t in torrents if tracker_host(t) in hosts]
    seeding = [t for t in rows if t.get("progress", 0) >= 1]
    bonus_gb = sum(min(t["size"], 50 * 1024 ** 3) * (1 + 1 / max(t.get("num_complete") or 0, 1))
                   for t in seeding) / 1024 ** 3
    return {
        "torrents": len(rows),
        "seeding": len(seeding),
        "bytes_on_disk": sum(t["size"] for t in rows),
        "uploaded_bytes": sum(t.get("uploaded") or 0 for t in rows),
        "leech_bonus_percent": min(100.0, bonus_gb / 10),
    }


def readings():
    """Numbers read off a site by eye, because three of these accounts cannot be read any other way.

    Kept in git with the date they were read, so a panel can show both the figure and how stale it is.
    A number with no timestamp is worse than no number.
    """
    path = Path(os.environ.get("TRACKER_READINGS",
                               PROJECT_DIR / "config/trackers/readings.json"))
    try:
        return json.loads(path.read_text())
    except Exception:
        return {}


def escape(value):
    return str(value).replace("\\", "\\\\").replace('"', '\\"').replace("\n", " ")


def push(job, lines):
    request = urllib.request.Request(f"{PUSHGATEWAY}/metrics/job/{job}",
                                     data=("\n".join(lines) + "\n").encode(), method="PUT")
    try:
        urllib.request.urlopen(request, timeout=10).close()
    except (urllib.error.URLError, OSError) as exc:
        failures.append(f"{job}: pushgateway unreachable: {exc}")


def warning_seconds(text):
    """`Warned until 2026-09-03 20:25:46`, in the site's own timezone, which it reports as UTC."""
    if not text:
        return 0
    try:
        when = datetime.strptime(text.strip()[:19], "%Y-%m-%d %H:%M:%S").replace(tzinfo=timezone.utc)
    except ValueError:
        return 0
    return max(0.0, (when - datetime.now(timezone.utc)).total_seconds())


def main():
    rules = json.loads(RULES_FILE.read_text())
    try:
        torrents = qbit("torrents/info")
    except Exception as exc:
        failures.append(f"qbittorrent: {exc}")
        torrents = []

    lines = [
        "# HELP tracker_up 1 when the site answered with the account page",
        "# TYPE tracker_up gauge",
        "# HELP tracker_ratio The ratio the site itself reports",
        "# TYPE tracker_ratio gauge",
        "# HELP tracker_min_ratio The ratio under which this site disables the account",
        "# TYPE tracker_min_ratio gauge",
        "# HELP tracker_uploaded_bytes Uploaded, as counted by the site",
        "# TYPE tracker_uploaded_bytes gauge",
        "# HELP tracker_downloaded_bytes Downloaded, as counted by the site (freeleech excluded)",
        "# TYPE tracker_downloaded_bytes gauge",
        "# HELP tracker_buffer_bytes uploaded - min_ratio x downloaded",
        "# TYPE tracker_buffer_bytes gauge",
        "# HELP tracker_headroom_bytes Non-freeleech downloads that still fit above the line",
        "# TYPE tracker_headroom_bytes gauge",
        "# HELP tracker_points Bonus points the site grants for seeding",
        "# TYPE tracker_points gauge",
        "# HELP tracker_warning_seconds Seconds left on an active warning, 0 when there is none",
        "# TYPE tracker_warning_seconds gauge",
        "# HELP tracker_hnr_pending Torrents owing a hit & run obligation right now",
        "# TYPE tracker_hnr_pending gauge",
        "# HELP tracker_hnr_hours_worst Seeding hours the furthest-behind torrent still owes",
        "# TYPE tracker_hnr_hours_worst gauge",
        "# HELP tracker_hnr_at_risk Torrents owing hours whose seeding clock is not running",
        "# TYPE tracker_hnr_at_risk gauge",
        "# HELP tracker_last_run_timestamp When this script last read the site",
        "# TYPE tracker_last_run_timestamp gauge",
        "# HELP tracker_client_torrents Torrents in the client for this tracker",
        "# TYPE tracker_client_torrents gauge",
        "# HELP tracker_client_seeding Of those, how many are complete and seeding",
        "# TYPE tracker_client_seeding gauge",
        "# HELP tracker_client_bytes_on_disk What this tracker's torrents occupy locally",
        "# TYPE tracker_client_bytes_on_disk gauge",
        "# HELP tracker_client_uploaded_bytes Uploaded per the client, which is not the site's count",
        "# TYPE tracker_client_uploaded_bytes gauge",
        "# HELP tracker_leech_bonus_percent Estimated leech bonus from what is seeding, DigitalCore's formula",
        "# TYPE tracker_leech_bonus_percent gauge",
        "# HELP tracker_read_ratio Ratio read off the site by eye, for accounts that cannot be scraped",
        "# TYPE tracker_read_ratio gauge",
        "# HELP tracker_read_uploaded_bytes Uploaded, read off the site by eye",
        "# TYPE tracker_read_uploaded_bytes gauge",
        "# HELP tracker_read_downloaded_bytes Downloaded, read off the site by eye",
        "# TYPE tracker_read_downloaded_bytes gauge",
        "# HELP tracker_read_headroom_bytes Non-freeleech downloads that fit, from the read figures",
        "# TYPE tracker_read_headroom_bytes gauge",
        "# HELP tracker_read_points Bonus points, read off the site by eye",
        "# TYPE tracker_read_points gauge",
        "# HELP tracker_read_hnr Hit and run count the site itself shows",
        "# TYPE tracker_read_hnr gauge",
        "# HELP tracker_read_timestamp When those figures were read",
        "# TYPE tracker_read_timestamp gauge",
        "# HELP tracker_deadline_seconds Seconds left on a deadline the site imposes, 0 when none",
        "# TYPE tracker_deadline_seconds gauge",
    ]
    detail = ["# HELP tracker_hnr_torrent_hours_left Hours of seeding a torrent still owes",
              "# TYPE tracker_hnr_torrent_hours_left gauge"]

    all_readings = readings()
    state = {"read_at": datetime.now(timezone.utc).timestamp(), "trackers": {}}
    for tracker, config in rules.items():
        label = f'tracker="{escape(tracker)}"'
        min_ratio = config.get("min_ratio", 0.4)
        pending, worst = hit_and_run(config, torrents)
        detail_rows = sorted(pending, key=lambda row: -row[1])[:25]

        # Every tracker gets these, readable account or not.
        client = client_side(config, torrents)
        lines += [f'tracker_client_torrents{{{label}}} {client["torrents"]}',
                  f'tracker_client_seeding{{{label}}} {client["seeding"]}',
                  f'tracker_client_bytes_on_disk{{{label}}} {client["bytes_on_disk"]}',
                  f'tracker_client_uploaded_bytes{{{label}}} {client["uploaded_bytes"]}',
                  ]
        # Only where the mechanic exists: on a site with no leech bonus the number is noise.
        if config.get("leech_bonus"):
            lines.append(f'tracker_leech_bonus_percent{{{label}}} '
                         f'{client["leech_bonus_percent"]:.1f}')

        read = (all_readings.get(tracker) or {})
        if read:
            up = read.get("uploaded_gb")
            down = read.get("downloaded_gb")
            if up is not None and down is not None:
                lines += [f'tracker_read_uploaded_bytes{{{label}}} {up * 1024 ** 3:.0f}',
                          f'tracker_read_downloaded_bytes{{{label}}} {down * 1024 ** 3:.0f}']
                if min_ratio > 0:
                    buffer_read = (up - min_ratio * down) * 1024 ** 3
                    lines.append(f"tracker_read_headroom_bytes{{{label}}} "
                                 f"{buffer_read / min_ratio:.0f}")
            for key, metric in (("ratio", "tracker_read_ratio"), ("points", "tracker_read_points"),
                                ("hit_and_run", "tracker_read_hnr")):
                if read.get(key) is not None:
                    lines.append(f"{metric}{{{label}}} {read[key]}")
            if read.get("read_at"):
                try:
                    when = datetime.fromisoformat(read["read_at"]).replace(tzinfo=timezone.utc)
                    lines.append(f"tracker_read_timestamp{{{label}}} {when.timestamp():.0f}")
                except ValueError:
                    failures.append(f"{tracker}: unreadable read_at {read['read_at']}")
            if read.get("deadline"):
                try:
                    when = datetime.fromisoformat(read["deadline"]).replace(tzinfo=timezone.utc)
                    left = (when - datetime.now(timezone.utc)).total_seconds()
                    lines.append(f"tracker_deadline_seconds{{{label}}} {max(0.0, left):.0f}")
                except ValueError:
                    failures.append(f"{tracker}: unreadable deadline {read['deadline']}")

        # A site nobody can log into still has torrents in the client, and the hit & run clock is
        # the half that gets accounts banned. So the obligations are measured for every tracker in
        # the file, and only the account numbers need a `site`.
        if not config.get("site"):
            # A site with no ratio rule gets no line and no headroom: emitting zero for both put
            # BTSCHOOL at the top of a "tightest headroom" panel it has no business being in.
            if min_ratio > 0:
                lines.append(f"tracker_min_ratio{{{label}}} {min_ratio}")
            lines += [f"tracker_hnr_pending{{{label}}} {len(pending)}",
                      f"tracker_hnr_hours_worst{{{label}}} {worst:.1f}",
                      f"tracker_hnr_at_risk{{{label}}} {sum(1 for row in pending if row[3])}"]
            for name, hours_left, ratio, stopped in detail_rows:
                detail.append(f'tracker_hnr_torrent_hours_left{{{label},'
                              f'name="{escape(name)[:90]}",ratio="{ratio:.2f}",'
                              f'seeding="{"no" if stopped else "yes"}"}} {hours_left:.1f}')
            continue

        try:
            stats = profile(tracker, config)
        except Exception as exc:
            failures.append(f"{tracker}: {exc}")
            lines.append(f"tracker_up{{{label}}} 0")
            continue

        buffer_bytes = stats["uploaded"] - min_ratio * stats["downloaded"]
        lines += [
            f"tracker_up{{{label}}} 1",
            f'tracker_ratio{{{label}}} {stats["ratio"] if stats["ratio"] is not None else 0}',
            f"tracker_min_ratio{{{label}}} {min_ratio}",
            f'tracker_uploaded_bytes{{{label}}} {stats["uploaded"]:.0f}',
            f'tracker_downloaded_bytes{{{label}}} {stats["downloaded"]:.0f}',
            f"tracker_buffer_bytes{{{label}}} {buffer_bytes:.0f}",
            f"tracker_headroom_bytes{{{label}}} {buffer_bytes / min_ratio:.0f}",
            f'tracker_points{{{label}}} {stats["points"] or 0}',
            f'tracker_warning_seconds{{{label}}} {warning_seconds(stats["warned_until"]):.0f}',
            f"tracker_hnr_pending{{{label}}} {len(pending)}",
            f"tracker_hnr_hours_worst{{{label}}} {worst:.1f}",
            f"tracker_hnr_at_risk{{{label}}} {sum(1 for row in pending if row[3])}",
            f'tracker_class_info{{{label},class="{escape(stats["class"] or "?")}"}} 1',
        ]
        state["trackers"][tracker] = {
            "ratio": stats["ratio"], "min_ratio": min_ratio,
            "uploaded_bytes": stats["uploaded"], "downloaded_bytes": stats["downloaded"],
            "buffer_bytes": buffer_bytes, "headroom_bytes": buffer_bytes / min_ratio,
            "hnr_pending": len(pending), "hnr_at_risk": sum(1 for row in pending if row[3]),
            "warning_seconds": warning_seconds(stats["warned_until"]),
        }
        for name, hours_left, ratio, stopped in detail_rows:
            detail.append(f'tracker_hnr_torrent_hours_left{{{label},'
                          f'name="{escape(name)[:90]}",ratio="{ratio:.2f}",'
                          f'seeding="{"no" if stopped else "yes"}"}} {hours_left:.1f}')

    lines.append(f"tracker_last_run_timestamp {datetime.now(timezone.utc).timestamp():.0f}")
    push("tracker_stats", lines)
    # Pushgateway is replaced on every push and Prometheus is one more thing that can be down, so
    # the number tracker-control.py acts on is written here, with the time it was read.
    STATE_DIR.mkdir(parents=True, exist_ok=True)
    (STATE_DIR / "state.json").write_text(json.dumps(state, indent=2) + "\n")
    push("tracker_hnr", detail)

    if failures:
        print("\n".join(failures), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
