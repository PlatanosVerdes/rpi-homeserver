#!/usr/bin/env python3
"""Converge Radarr/Sonarr custom formats and quality profiles to config/arr/<app>/*.json.

These were built by hand through each app's own UI (language scoring, the 4K/good/wasteful/
unwanted split, which qualities each profile allows) and live only in that app's own database
under appdata/ — nothing in a compose file or .env captures them, so losing appdata silently
loses hours of tuning. Matched by name, so re-running this on every deploy is a no-op unless the
committed JSON changed.

Custom formats must sync first: quality profiles score them by name, and the id a format gets
on THIS install is only known after it exists here.
"""
import json
import subprocess
import sys
from pathlib import Path
from urllib.error import HTTPError
from urllib.request import Request, urlopen

ROOT = Path(__file__).resolve().parent.parent
APPS = [("radarr", 7878), ("sonarr", 8989)]


def api_key(app):
    conf = Path(f"/home/raspi/rpi-homeserver/appdata/{app}/config.xml")
    # Not running here (e.g. a secondary Pi with only the essential/moni profile): nothing to do.
    if subprocess.run(["sudo", "test", "-f", str(conf)]).returncode != 0:
        return None
    out = subprocess.run(["sudo", "grep", "-oP", "(?<=<ApiKey>)[^<]+", str(conf)],
                         capture_output=True, text=True, check=True)
    return out.stdout.strip()


def call(base, path, key, method="GET", body=None):
    data = json.dumps(body).encode() if body is not None else None
    req = Request(f"{base}{path}", data=data, method=method,
                  headers={"X-Api-Key": key, "Content-Type": "application/json"})
    try:
        raw = urlopen(req, timeout=30).read()
        return json.loads(raw) if raw.strip() else None
    except HTTPError as e:
        raise RuntimeError(f"{method} {path} -> {e.code}: {e.read().decode()[:300]}") from None


def sync_custom_formats(base, key, wanted):
    existing = {f["name"]: f["id"] for f in call(base, "/customformat", key)}
    name_to_id = dict(existing)
    for cf in wanted:
        body = {"name": cf["name"],
                "includeCustomFormatWhenRenaming": cf["includeCustomFormatWhenRenaming"],
                "specifications": cf["specifications"]}
        if cf["name"] in existing:
            body["id"] = existing[cf["name"]]
            call(base, f"/customformat/{body['id']}", key, "PUT", body)
        else:
            created = call(base, "/customformat", key, "POST", body)
            name_to_id[cf["name"]] = created["id"]
    return name_to_id


def sync_quality_profiles(base, key, wanted, format_ids):
    existing = {p["name"]: p["id"] for p in call(base, "/qualityprofile", key)}
    for qp in wanted:
        format_items = [{"format": format_ids[fi["formatName"]], "name": fi["formatName"],
                         "score": fi["score"]} for fi in qp["formatItems"]]
        body = {"name": qp["name"], "upgradeAllowed": qp["upgradeAllowed"],
                "cutoff": qp["cutoff"], "minFormatScore": qp["minFormatScore"],
                "cutoffFormatScore": qp["cutoffFormatScore"],
                "minUpgradeFormatScore": qp["minUpgradeFormatScore"],
                "items": qp["items"], "formatItems": format_items}
        if qp["name"] in existing:
            body["id"] = existing[qp["name"]]
            call(base, f"/qualityprofile/{body['id']}", key, "PUT", body)
        else:
            call(base, "/qualityprofile", key, "POST", body)


def main():
    failures = []
    for app, port in APPS:
        conf_dir = ROOT / "config" / "arr" / app
        cf_path = conf_dir / "custom-formats.json"
        qp_path = conf_dir / "quality-profiles.json"
        if not cf_path.exists():
            continue
        try:
            key = api_key(app)
            if key is None:
                continue
            base = f"http://localhost:{port}/api/v3"
            wanted_formats = json.loads(cf_path.read_text())
            format_ids = sync_custom_formats(base, key, wanted_formats)
            wanted_profiles = json.loads(qp_path.read_text())
            sync_quality_profiles(base, key, wanted_profiles, format_ids)
            print(f"[{app}] {len(wanted_formats)} custom formats, "
                  f"{len(wanted_profiles)} quality profiles synced")
        except Exception as exc:
            failures.append(f"{app}: {exc}")

    if failures:
        print("sync-arr-config: failures:\n  " + "\n  ".join(failures), file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
