#!/usr/bin/env python3
"""Push every https://*.platanosverdes.com host declared in Caddy into Pi-hole's custom DNS.

Pi-hole's custom DNS records live only in its own appdata, so the entire *.platanosverdes.com
mapping described in CLAUDE.md existed only by hand-editing the Pi-hole UI. This derives the
hostname list from the Caddy config instead of keeping a second list that could drift from it:
this repo's Caddyfile + config/caddy/services/*.caddy, plus rpi-services' own fragment at
EXT_CADDY_PATH if that repo is present.

Additive only, via Pi-hole's array-item endpoints (PUT/DELETE one entry at a time): a hostname
is only touched if it is one we derived from Caddy, and only written when its IP is stale. A
hand-added entry unrelated to any service here (another Tailscale node, a personal bookmark)
is never read as "ours" and is never deleted, even if it stops matching anything in Caddy.
"""
import glob
import json
import re
import sys
from pathlib import Path
from urllib.error import HTTPError
from urllib.parse import quote
from urllib.request import Request, urlopen

ROOT = Path(__file__).resolve().parent.parent
HOST_RE = re.compile(r"^https://([\w.-]+\.platanosverdes\.com)\s*\{", re.MULTILINE)
PIHOLE_BASE = "http://localhost:8081/api"


def env(name):
    for line in (ROOT / ".env").read_text().splitlines():
        if line.startswith(f"{name}="):
            return line.split("=", 1)[1].strip().strip('"')
    return None


def desired_hostnames():
    sources = [ROOT / "config/caddy/Caddyfile", *ROOT.glob("config/caddy/services/*.caddy")]
    ext = env("EXT_CADDY_PATH")
    if ext:
        sources += [Path(p) for p in glob.glob(f"{ext}/*.caddy")]
    names = set()
    for path in sources:
        if path.exists():
            names |= set(HOST_RE.findall(path.read_text()))
    return names


def auth():
    token = env("PIHOLE_TOKEN")
    req = Request(f"{PIHOLE_BASE}/auth", method="POST",
                  data=json.dumps({"password": token}).encode(),
                  headers={"Content-Type": "application/json"})
    return json.loads(urlopen(req, timeout=15).read())["session"]["sid"]


def call(path, sid, method="GET"):
    req = Request(f"{PIHOLE_BASE}{path}?sid={sid}", method=method)
    try:
        raw = urlopen(req, timeout=15).read()
        return json.loads(raw) if raw.strip() else None
    except HTTPError as e:
        body = e.read()
        if e.code == 400 and b"item_already_present" in body:
            return None
        raise RuntimeError(f"{method} {path} -> {e.code}: {body[:200]}") from None


def main():
    tailscale_ip = env("TAILSCALE_IP")
    if not tailscale_ip:
        print("sync-pihole-dns: TAILSCALE_IP not set in .env, skipping", file=sys.stderr)
        return
    wanted = desired_hostnames()
    if not wanted:
        print("sync-pihole-dns: no *.platanosverdes.com hosts found in Caddy config, skipping",
              file=sys.stderr)
        return

    try:
        sid = auth()
    except Exception as exc:
        print(f"sync-pihole-dns: could not reach Pi-hole's API, skipping: {exc}", file=sys.stderr)
        return
    current = call("/config/dns/hosts", sid)["config"]["dns"]["hosts"] or []
    by_host = {}
    for entry in current:
        ip, _, host = entry.partition(" ")
        by_host[host] = ip

    added, updated, unchanged = [], [], []
    for host in sorted(wanted):
        existing_ip = by_host.get(host)
        if existing_ip == tailscale_ip:
            unchanged.append(host)
            continue
        if existing_ip is not None:
            stale_entry = f"{existing_ip} {host}"
            call(f"/config/dns/hosts/{quote(stale_entry, safe='')}", sid, "DELETE")
            updated.append(host)
        else:
            added.append(host)
        call(f"/config/dns/hosts/{quote(f'{tailscale_ip} {host}', safe='')}", sid, "PUT")

    left_alone = len(current) - len(by_host.keys() & wanted)
    print(f"pihole DNS: {len(added)} added, {len(updated)} re-pointed, {len(unchanged)} unchanged, "
          f"{left_alone} unrelated entries left untouched")


if __name__ == "__main__":
    main()
