#!/usr/bin/env python3
"""Pull the configuration that lives only inside an app, so git has a copy of it.

Most of this repo pushes configuration into the apps (scripts/sync/*). This does the opposite, for
the two whose settings are pure logic with no credentials in them and which no file here described:

  autobrr      its filters: what is worth grabbing, the size band, the daily cap
  maintainerr  its rule group: watched more than two days ago, then delete

Deliberately one-way, unlike the sync scripts that push git into an app: a wrong autobrr filter
costs disk, a wrong Maintainerr rule deletes films, so pushing a deletion rule from a file nobody
reviewed is not a trade worth making. What it offers instead is `--check`, which apply.sh runs on
every deploy to say out loud that git and the app have drifted apart.

  config-export.py            write the files, for a human to read and commit
  config-export.py --check    exit 1 and print what differs, changing nothing

Prowlarr stays out on purpose: every indexer carries a passkey and this repo is public. autobrr
redacts its own: the API answers an indexer's `rsskey` with the literal `<redacted>`, so what lands
here is the shape of the setup and not the way in.
"""

import difflib
import json
import os
import subprocess
import sys
from pathlib import Path

PROJECT_DIR = Path(os.path.expanduser("~/rpi-homeserver"))
# Fields that move on their own. Without this the check would report drift on every deploy, which
# teaches everyone to ignore the line.
VOLATILE = {"created_at", "updated_at", "last_run", "last_run_data", "lastRun", "addDate",
            "created", "updated", "dateAdded", "media", "totalSizeBytes",
            # Maintainerr keeps its running totals in the same object as its settings
            "handledMediaAmount", "handledMediaSizeBytes", "lastDurationInSeconds",
            # autobrr reports these as 0 whatever the filter holds, so they say nothing
            "actions_count", "actions_enabled_count"}


def env(name):
    for line in (PROJECT_DIR / ".env").read_text().splitlines():
        if line.startswith(f"{name}="):
            return line.split("=", 1)[1].strip()
    raise RuntimeError(f"{name} is not in .env")


def inside(container, command, timeout=30):
    result = subprocess.run(["docker", "exec", container] + command,
                            capture_output=True, text=True, timeout=timeout)
    if result.returncode != 0:
        raise RuntimeError(result.stderr.strip()[:160] or f"{container}: command failed")
    return json.loads(result.stdout)


def strip(value):
    """Drop the fields that move on their own, at any depth."""
    if isinstance(value, dict):
        return {k: strip(v) for k, v in sorted(value.items()) if k not in VOLATILE}
    if isinstance(value, list):
        return [strip(v) for v in value]
    return value


def autobrr_filters():
    token = env("AUTOBRR_API_KEY")

    def api(path):
        return inside("autobrr", ["curl", "-sf", "-H", f"X-API-Token: {token}",
                                  f"http://localhost:7474/api/{path}"])

    ids = sorted(f["id"] for f in api("filters"))
    return [strip(api(f"filters/{i}")) for i in ids]


def maintainerr_rules():
    # Its own server listens on IPv4 only, so localhost inside the container is refused.
    return strip(inside("maintainerr", ["wget", "-qO-", "http://127.0.0.1:6246/api/rules"]))


SOURCES = [
    ("config/autobrr/filters.json", autobrr_filters),
    ("config/maintainerr/rules.json", maintainerr_rules),
]


def main():
    check = "--check" in sys.argv[1:]
    drifted = []
    for relative, fetch in SOURCES:
        path = PROJECT_DIR / relative
        try:
            wanted = json.dumps(fetch(), indent=2, sort_keys=True) + "\n"
        except Exception as exc:
            print(f"{relative}: cannot read the live config: {exc}", file=sys.stderr)
            return 2
        current = path.read_text() if path.is_file() else ""
        if wanted == current:
            continue
        if not check:
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(wanted)
            print(f"{relative}: written")
            continue
        drifted.append(relative)
        print(f"{relative}: the live config no longer matches what is committed")
        for line in list(difflib.unified_diff(current.splitlines(), wanted.splitlines(),
                                              "committed", "live", lineterm=""))[:40]:
            print(f"  {line}")
    if drifted:
        print("Run scripts/ops/config-export.py and commit, or undo the change in the app. "
              f"({', '.join(drifted)})")
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
