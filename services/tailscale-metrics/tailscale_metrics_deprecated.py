#!/usr/bin/env python3
"""
Combines local tailscale status + Tailscale Admin API to produce Prometheus metrics.
- Local status: real-time online/offline, traffic bytes
- API: which devices advertise/enable exit node routes

Run via cron every minute:
  * * * * * /usr/bin/python3 /path/to/tailscale_metrics.py
Reads TAILSCALE_API_KEY from /home/raspi/rpi-homeserver/.env
"""

import json
import os
import re
import subprocess
import sys
import time
import urllib.request

OUTPUT_FILE = "/var/lib/node_exporter/textfile_collector/tailscale.prom"
TMP_FILE    = OUTPUT_FILE + ".tmp"
ENV_FILE    = "/home/raspi/rpi-homeserver/.env"


def load_env_key(path, key):
    try:
        with open(path) as f:
            for line in f:
                line = line.strip()
                if line.startswith(key + "="):
                    val = line.split("=", 1)[1].strip().strip('"').strip("'")
                    return val
    except Exception:
        pass
    return None


def tailscale_status():
    result = subprocess.run(
        ["tailscale", "status", "--json"],
        capture_output=True, text=True, timeout=10
    )
    return json.loads(result.stdout)


def api_devices(api_key):
    url = "https://api.tailscale.com/api/v2/tailnet/-/devices?fields=all"
    req = urllib.request.Request(url)
    # Basic auth: api_key as username, empty password
    import base64
    creds = base64.b64encode(f"{api_key}:".encode()).decode()
    req.add_header("Authorization", f"Basic {creds}")
    with urllib.request.urlopen(req, timeout=10) as resp:
        return json.loads(resp.read()).get("devices", [])


def main():
    api_key = load_env_key(ENV_FILE, "TAILSCALE_API_KEY")

    try:
        status = tailscale_status()
    except Exception as e:
        print(f"error running tailscale status: {e}", file=sys.stderr)
        sys.exit(1)

    # Build exit-node info from API if key available
    exit_node_providers = set()   # hostnames that advertise exit node (approved)
    api_ok = False
    if api_key:
        try:
            devices = api_devices(api_key)
            for dev in devices:
                name = dev.get("name", "").split(".")[0]
                enabled = dev.get("enabledRoutes", [])
                if "0.0.0.0/0" in enabled or "::/0" in enabled:
                    exit_node_providers.add(name)
            api_ok = True
        except Exception as e:
            print(f"warning: tailscale API error: {e}", file=sys.stderr)

    lines = []
    lines.append("# HELP tailscale_peer_online 1 if peer is currently reachable, 0 if offline")
    lines.append("# TYPE tailscale_peer_online gauge")
    lines.append("# HELP tailscale_peer_rx_bytes Total bytes received from peer")
    lines.append("# TYPE tailscale_peer_rx_bytes counter")
    lines.append("# HELP tailscale_peer_tx_bytes Total bytes sent to peer")
    lines.append("# TYPE tailscale_peer_tx_bytes counter")
    lines.append("# HELP tailscale_peer_is_exit_node 1 if peer is an approved exit node provider")
    lines.append("# TYPE tailscale_peer_is_exit_node gauge")
    lines.append("# HELP tailscale_peer_is_active_exit_node 1 if THIS device is currently routing through this peer")
    lines.append("# TYPE tailscale_peer_is_active_exit_node gauge")
    lines.append("# HELP tailscale_scrape_timestamp Unix timestamp of last successful scrape")
    lines.append("# TYPE tailscale_scrape_timestamp gauge")
    lines.append(f"tailscale_scrape_timestamp {int(time.time())}")

    for _key, peer in status.get("Peer", {}).items():
        dns_name = peer.get("DNSName", "").rstrip(".")
        hostname = dns_name.split(".")[0] if dns_name else peer.get("HostName", "unknown")
        os_name  = peer.get("OS", "unknown")
        ip       = peer.get("TailscaleIPs", [""])[0]
        online   = 1 if peer.get("Online", False) else 0
        rx       = peer.get("RxBytes", 0)
        tx       = peer.get("TxBytes", 0)
        # ExitNode=True means THIS device is currently routing through this peer
        active_exit = 1 if peer.get("ExitNode", False) else 0
        # exit node provider status comes from API (approved routes)
        is_exit = 1 if (api_ok and hostname in exit_node_providers) else 0

        labels = f'hostname="{hostname}",ip="{ip}",os="{os_name}"'
        lines.append(f"tailscale_peer_online{{{labels}}} {online}")
        lines.append(f"tailscale_peer_rx_bytes{{{labels}}} {rx}")
        lines.append(f"tailscale_peer_tx_bytes{{{labels}}} {tx}")
        lines.append(f"tailscale_peer_is_exit_node{{{labels}}} {is_exit}")
        lines.append(f"tailscale_peer_is_active_exit_node{{{labels}}} {active_exit}")

    with open(TMP_FILE, "w") as f:
        f.write("\n".join(lines) + "\n")

    os.replace(TMP_FILE, OUTPUT_FILE)


if __name__ == "__main__":
    main()
