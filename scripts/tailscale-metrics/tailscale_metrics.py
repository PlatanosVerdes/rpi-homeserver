#!/usr/bin/env python3
"""
Queries the local Tailscale daemon for peer status and writes
Prometheus text format to a file for node-exporter textfile collector.
Run via cron every minute:
  * * * * * /usr/bin/python3 /path/to/tailscale_metrics.py
"""

import json
import subprocess
import sys
import time

OUTPUT_FILE = "/var/lib/node_exporter/textfile_collector/tailscale.prom"
TMP_FILE    = OUTPUT_FILE + ".tmp"

def main():
    try:
        result = subprocess.run(
            ["tailscale", "status", "--json"],
            capture_output=True, text=True, timeout=10
        )
        data = json.loads(result.stdout)
    except Exception as e:
        print(f"error: {e}", file=sys.stderr)
        sys.exit(1)

    lines = []
    lines.append("# HELP tailscale_peer_online 1 if peer is currently reachable, 0 if offline")
    lines.append("# TYPE tailscale_peer_online gauge")
    lines.append("# HELP tailscale_peer_rx_bytes Total bytes received from peer")
    lines.append("# TYPE tailscale_peer_rx_bytes counter")
    lines.append("# HELP tailscale_peer_tx_bytes Total bytes sent to peer")
    lines.append("# TYPE tailscale_peer_tx_bytes counter")
    lines.append("# HELP tailscale_peer_is_exit_node 1 if peer advertises as exit node")
    lines.append("# TYPE tailscale_peer_is_exit_node gauge")
    lines.append("# HELP tailscale_peer_using_as_exit_node 1 if this device is routing traffic through this peer")
    lines.append("# TYPE tailscale_peer_using_as_exit_node gauge")
    lines.append("# HELP tailscale_scrape_timestamp Unix timestamp of last successful scrape")
    lines.append("# TYPE tailscale_scrape_timestamp gauge")
    lines.append(f"tailscale_scrape_timestamp {int(time.time())}")

    for _key, peer in data.get("Peer", {}).items():
        dns_name = peer.get("DNSName", "").rstrip(".")
        hostname = dns_name.split(".")[0] if dns_name else peer.get("HostName", "unknown")
        os_name  = peer.get("OS", "unknown")
        ip       = peer.get("TailscaleIPs", [""])[0]
        online   = 1 if peer.get("Online", False) else 0
        rx       = peer.get("RxBytes", 0)
        tx       = peer.get("TxBytes", 0)
        is_exit  = 1 if peer.get("ExitNode", False) else 0
        using_exit = 1 if peer.get("ExitNodeForUs", False) else 0

        labels = f'hostname="{hostname}",ip="{ip}",os="{os_name}"'
        lines.append(f"tailscale_peer_online{{{labels}}} {online}")
        lines.append(f"tailscale_peer_rx_bytes{{{labels}}} {rx}")
        lines.append(f"tailscale_peer_tx_bytes{{{labels}}} {tx}")
        lines.append(f"tailscale_peer_is_exit_node{{{labels}}} {is_exit}")
        lines.append(f"tailscale_peer_using_as_exit_node{{{labels}}} {using_exit}")

    with open(TMP_FILE, "w") as f:
        f.write("\n".join(lines) + "\n")

    import os
    os.replace(TMP_FILE, OUTPUT_FILE)

if __name__ == "__main__":
    main()
