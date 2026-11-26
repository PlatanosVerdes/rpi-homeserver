#!/usr/bin/env python3
"""Keep DDNS_HOSTNAME's A record in Cloudflare pointing at the home line's public IPv4.

For what must reach the house directly rather than through the tunnel, such as a GPS tracker
dialling Traccar on a raw TCP port. The ISP hands out a new address now and then, and a device
configured with the old one goes silent without anyone noticing.

The record is DNS only (not proxied, Cloudflare does not relay raw TCP) with a 60 s TTL, and it
is created on the first run. Uses CF_API_TOKEN, the token Caddy already holds for DNS-01.
No-op unless DDNS_HOSTNAME is set. Run from cron (see scripts/crontab); silent unless it changes
something.
"""

import ipaddress
import json
import sys
import urllib.request
from pathlib import Path

PROJECT_DIR = Path(__file__).resolve().parents[2]
CF_API = "https://api.cloudflare.com/client/v4"
IP_SOURCES = ["https://api.ipify.org", "https://ipv4.icanhazip.com"]


def read_env(path):
    env = {}
    for line in path.read_text().splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        value = value.strip()
        if len(value) >= 2 and value[0] == value[-1] and value[0] in "'\"":
            value = value[1:-1]
        env[key.strip()] = value
    return env


def public_ip():
    for url in IP_SOURCES:
        try:
            with urllib.request.urlopen(url, timeout=10) as resp:
                ip = ipaddress.ip_address(resp.read().decode().strip())
        except (OSError, ValueError):
            continue
        if ip.version == 4 and ip.is_global:
            return str(ip)
    return None


def cloudflare(token, path, data=None, method=None):
    request = urllib.request.Request(
        f"{CF_API}/{path}",
        headers={"Authorization": f"Bearer {token}", "Content-Type": "application/json"},
        data=json.dumps(data).encode() if data is not None else None,
        method=method)
    with urllib.request.urlopen(request, timeout=20) as resp:
        body = json.load(resp)
    if not body.get("success"):
        raise RuntimeError(f"Cloudflare {path}: {body.get('errors')}")
    return body["result"]


def main():
    env = read_env(PROJECT_DIR / ".env")
    hostname, token, domain = env.get("DDNS_HOSTNAME"), env.get("CF_API_TOKEN"), env.get("DOMAIN")
    if not hostname:
        return 0
    if not token or not domain:
        print("ddns: CF_API_TOKEN or DOMAIN not set in .env", file=sys.stderr)
        return 1

    ip = public_ip()
    if not ip:
        print("ddns: could not find out the public IPv4, leaving the record alone", file=sys.stderr)
        return 1

    zone = cloudflare(token, f"zones?name={domain}")[0]["id"]
    records = cloudflare(token, f"zones/{zone}/dns_records?type=A&name={hostname}")
    record = {"type": "A", "name": hostname, "content": ip, "ttl": 60, "proxied": False}
    if not records:
        cloudflare(token, f"zones/{zone}/dns_records", record, "POST")
        print(f"ddns: created {hostname} -> {ip}")
    elif records[0]["content"] != ip or records[0]["proxied"]:
        cloudflare(token, f"zones/{zone}/dns_records/{records[0]['id']}", record, "PUT")
        print(f"ddns: {hostname} {records[0]['content']} -> {ip}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
