#!/usr/bin/env python3
"""Clear Prowlarr's failure backoff on an indexer whose site is answering again.

Prowlarr backs a failing indexer off for up to 24 hours. That is right when a site is genuinely
gone, and wrong when the outage was a 40-minute block from the ISP: the site comes back, nothing
searches it until the backoff expires, and `prowlarr_indexer_up` reads that same `disabledTill`, so
the "an indexer is down" alert keeps firing over a site that works. On 2026-08-23 C411 sat unusable
for six hours after its site had recovered, and clearing it was a manual indexer test.

The site is reached for first, and Prowlarr is only asked to test the indexer once the site answers
over a certificate that validates. A tracker that is really down therefore costs one cheap HTTPS
request per run instead of a search, which matters on a private tracker: they ban for hammering.

The probe goes out directly, while Prowlarr may reach the same site through a proxy or
FlareSolverr. An indexer configured that way can be usable to Prowlarr while this sees nothing, and
then this waits rather than testing. That is the safe direction to be wrong in.

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
