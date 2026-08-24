#!/usr/bin/env python3
"""Clear Prowlarr's failure backoff on an indexer whose site is answering again.

Prowlarr backs a failing indexer off for up to 24 h, which is right when a site is gone and wrong
when the outage was a 40-minute ISP block: nothing searches it until the backoff expires, and the
"indexer is down" alert reads the same `disabledTill`, so it fires over a site that works.

The site is probed first and Prowlarr is only asked to test once it answers over a valid
certificate, so a tracker that is really down costs one HTTPS request per run and never a search:
they ban for hammering. The probe goes out directly while Prowlarr may use a proxy or FlareSolverr,
so an indexer configured that way waits rather than being tested, which is the safe way to be wrong.

Run from cron (see scripts/crontab). Silent unless something happened.
"""

import json
import os
import ssl
import sys
import urllib.error
import urllib.request
from datetime import datetime, timezone
from pathlib import Path

PROJECT_DIR = Path(os.path.expanduser("~/rpi-homeserver"))
PROWLARR = "http://localhost:9696/api/v1"
PROBE_TIMEOUT = 10
UA = "rpi-homeserver/indexer-retry"


def api_key(app):
    text = (PROJECT_DIR / "appdata" / app / "config.xml").read_text(errors="ignore")
    start = text.index("<ApiKey>") + len("<ApiKey>")
    return text[start:text.index("</ApiKey>", start)]


def call(path, key, data=None, method=None):
    request = urllib.request.Request(
        f"{PROWLARR}/{path}",
        headers={"X-Api-Key": key, "Content-Type": "application/json"},
        data=json.dumps(data).encode() if data is not None else None,
        method=method)
    with urllib.request.urlopen(request, timeout=30) as resp:
        body = resp.read()
    return json.loads(body) if body else {}


def site_answers(url):
    """Whether the site answers at all over a valid certificate. A 403 or a maintenance page counts:
    the question is whether the connection gets that far, not whether the site is happy. A wrong or
    self-signed certificate does not, because that is the signature of the connection being
    intercepted rather than the site being up."""
    request = urllib.request.Request(url, headers={"User-Agent": UA})
    try:
        urllib.request.urlopen(request, timeout=PROBE_TIMEOUT).close()
        return True, "answers"
    except urllib.error.HTTPError as exc:
        return True, f"answers HTTP {exc.code}"
    except urllib.error.URLError as exc:
        if isinstance(exc.reason, ssl.SSLError):
            return False, f"TLS: {getattr(exc.reason, 'reason', exc.reason)}"
        return False, f"unreachable: {exc.reason}"
    except OSError as exc:
        return False, f"unreachable: {exc}"


def main():
    key = api_key("prowlarr")
    now = datetime.now(timezone.utc)
    try:
        status = call("indexerstatus", key)
        indexers = {i["id"]: i for i in call("indexer", key)}
    except (urllib.error.URLError, OSError) as exc:
        print(f"prowlarr unreachable: {exc}", file=sys.stderr)
        return 1

    for entry in status:
        till = entry.get("disabledTill")
        if not till:
            continue
        until = datetime.fromisoformat(till.replace("Z", "+00:00"))
        if until <= now:
            continue
        indexer = indexers.get(entry["indexerId"])
        if not indexer:
            continue

        name = indexer["name"]
        left = round((until - now).total_seconds() / 60)
        urls = indexer.get("indexerUrls") or []
        if not urls:
            print(f"{name}: backed off for {left} more min, and no url to probe")
            continue

        answers, why = site_answers(urls[0])
        if not answers:
            continue                       # still down, and the alert is the place that says so

        try:
            call("indexer/test", key, data=indexer, method="POST")
        except urllib.error.HTTPError as exc:
            detail = exc.read()[:200].decode(errors="replace")
            print(f"{name}: site {why}, but Prowlarr's test failed: {exc.code} {detail}")
            continue
        except (urllib.error.URLError, OSError) as exc:
            print(f"{name}: site {why}, but the test call failed: {exc}")
            continue

        print(f"{now:%Y-%m-%d %H:%M} {name}: site {why}, test passed, "
              f"{left} min of backoff cleared")
    return 0


if __name__ == "__main__":
    sys.exit(main())
