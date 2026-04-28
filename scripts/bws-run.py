#!/usr/bin/env python3
"""
Loads Bitwarden SM secrets and exec's the given command with them injected.

Usage: bws-run.py <command> [args...]

- Uses the official bitwarden-sdk (no external bws CLI needed)
- Resolves Bitwarden API hosts via Google DNS (8.8.8.8) to bypass Tailscale MagicDNS
- JSON-valued secrets are expanded into individual env vars
- Retries up to 3 times on transient network failures
"""

import json
import os
import socket
import struct
import sys
import time

# ── DNS via Google (8.8.8.8) ─────────────────────────────────────────────────
# Tailscale MagicDNS (100.100.1.1) can intermittently fail for external domains.
# We patch socket.getaddrinfo so Python's SSL/HTTP stack resolves Bitwarden hosts
# using 8.8.8.8 instead.

_real_getaddrinfo = socket.getaddrinfo
_dns_cache: dict = {}


def _resolve_via_8888(hostname: str) -> str:
    """Resolve a hostname using Google DNS (8.8.8.8) via raw UDP."""
    tid = os.urandom(2)
    # DNS query header: ID, flags=standard query, QDCOUNT=1
    query = tid + b"\x01\x00\x00\x01\x00\x00\x00\x00\x00\x00"
    for label in hostname.encode().split(b"."):
        query += bytes([len(label)]) + label
    query += b"\x00\x00\x01\x00\x01"  # QNAME terminator + QTYPE A + QCLASS IN

    sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    sock.settimeout(3)
    try:
        sock.sendto(query, ("8.8.8.8", 53))
        data, _ = sock.recvfrom(512)
    finally:
        sock.close()

    # Skip header (12 bytes) + question section
    pos = 12
    while data[pos] != 0:
        pos += data[pos] + 1
    pos += 5  # null terminator + QTYPE + QCLASS

    # Walk answer records, return first A record
    ancount = struct.unpack(">H", data[6:8])[0]
    for _ in range(ancount):
        if data[pos] & 0xC0 == 0xC0:  # pointer
            pos += 2
        else:
            while data[pos] != 0:
                pos += data[pos] + 1
            pos += 1
        rtype, _, _, rdlen = struct.unpack(">HHIH", data[pos : pos + 10])
        pos += 10
        if rtype == 1 and rdlen == 4:  # A record
            return socket.inet_ntoa(data[pos : pos + 4])
        pos += rdlen

    raise OSError(f"No A record for {hostname}")


def _patched_getaddrinfo(host, port, *args, **kwargs):
    if isinstance(host, str) and "." in host and not host[0].isdigit():
        if host not in _dns_cache:
            try:
                _dns_cache[host] = _resolve_via_8888(host)
            except Exception:
                pass  # fall back to system resolver
        if host in _dns_cache:
            host = _dns_cache[host]
    return _real_getaddrinfo(host, port, *args, **kwargs)


socket.getaddrinfo = _patched_getaddrinfo
# ─────────────────────────────────────────────────────────────────────────────

ORG_ID = "7339ed5f-0c9b-4620-95ae-b3dc0138d981"


def load_secrets(token: str) -> dict[str, str]:
    from bitwarden_sdk import BitwardenClient, DeviceType, client_settings_from_dict

    client = BitwardenClient(
        client_settings_from_dict(
            {
                "apiUrl": "https://api.bitwarden.com",
                "deviceType": DeviceType.SDK,
                "identityUrl": "https://identity.bitwarden.com",
                "userAgent": "bitwarden/sdk-sm",
            }
        )
    )
    client.auth().login_access_token(token, ".bitwarden")

    ids = [str(s.id) for s in client.secrets().list(ORG_ID).data.data]
    all_secrets = client.secrets().get_by_ids(ids).data.data

    env_vars: dict[str, str] = {}
    for secret in all_secrets:
        try:
            obj = json.loads(secret.value)
            if isinstance(obj, dict):
                for k, v in obj.items():
                    env_vars[k] = v if isinstance(v, str) else json.dumps(v)
            else:
                env_vars[secret.key] = secret.value
        except (json.JSONDecodeError, TypeError):
            env_vars[secret.key] = secret.value

    return env_vars


def main():
    if len(sys.argv) < 2:
        print(f"Usage: {sys.argv[0]} <command> [args...]", file=sys.stderr)
        sys.exit(1)

    token = os.environ.get("BWS_ACCESS_TOKEN", "")
    if not token:
        print("bws-run: BWS_ACCESS_TOKEN not set", file=sys.stderr)
        sys.exit(1)

    last_err: Exception | None = None
    for attempt in range(1, 4):
        try:
            secrets = load_secrets(token)
            break
        except Exception as exc:
            last_err = exc
            print(f"bws-run: attempt {attempt}/3 failed: {exc}", file=sys.stderr)
            if attempt < 3:
                time.sleep(3)
    else:
        print(f"bws-run: giving up after 3 attempts", file=sys.stderr)
        sys.exit(1)

    os.execvpe(sys.argv[1], sys.argv[1:], {**os.environ, **secrets})


if __name__ == "__main__":
    main()
