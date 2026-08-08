# Tailscale Setup — Exit Node & Pi-hole DNS

## Overview

Tailscale provides a secure WireGuard-based VPN that lets you access your home server from anywhere. This guide configures the Raspberry Pi as:
- An **Exit Node** (route all internet traffic through it)
- A **DNS server** via Pi-hole (ad-blocking everywhere)

---

## Step 1 — Assign a Static IP

Reserve a static IP for your Raspberry Pi in your router's DHCP settings (use the Pi's MAC address). Update `STATIC_IP` in your `.env`.

---

## Step 2 — Enable IP Forwarding

```bash
echo 'net.ipv4.ip_forward = 1' | sudo tee -a /etc/sysctl.d/99-tailscale.conf
echo 'net.ipv6.conf.all.forwarding = 1' | sudo tee -a /etc/sysctl.d/99-tailscale.conf
sudo sysctl -p /etc/sysctl.d/99-tailscale.conf
```

---

## Step 3 — Start Tailscale

**Exit node only:**
```bash
sudo tailscale up --advertise-exit-node --accept-dns=false
```

**Exit node + expose local LAN to Tailscale devices:**
```bash
sudo tailscale up --advertise-exit-node --advertise-routes=192.168.1.0/24 --accept-lan=true --accept-dns=false
```

> `--accept-dns=false` is **critical** — it prevents Tailscale from overwriting `/etc/resolv.conf`, ensuring Pi-hole and Docker DNS continue to work.

---

## Step 4 — Tailscale Admin Console

1. Go to [login.tailscale.com/admin](https://login.tailscale.com/admin)
2. **Enable Exit Node:** Machines → your Pi → Edit route settings → enable "Use as exit node"
3. **Set Pi-hole as DNS:**
   - DNS tab → Global Nameservers → Add nameserver → Custom
   - Enter your Pi's Tailscale IP (`100.x.x.x`)
   - Enable "Override local DNS"

---

## Step 5 — Pi-hole: Allow Tailscale Subnet

Tailscale requests come from `100.x.x.x` — Pi-hole must allow them:

- Pi-hole → Settings → DNS → Interface Settings → select **"Permit all origins"**

---

## Step 6 — Plex: Treat the Tailnet as Local

Since April 2025 Plex charges for remote playback of personal media: either the server owner has a
Plex Pass (which covers everyone they share with) or each viewer pays a Remote Watch Pass. Watching
over Tailscale hits that paywall by default, and the exit node does **not** help.

Plex decides local vs remote from the **source IP of the connection**, not from your public IP. The
container runs on the host network and `tailscale0` is a `/32`, so a peer arriving from `100.x` is
in neither the server's own subnet nor its `/32` — external, paywalled.

The fix is Settings > Network > **LAN Networks** (`LanNetworksBandwidth`), whose description is
literally "networks that will be considered local":

```
100.64.0.0/10,192.168.1.0/24
```

> Not the same as **"List of IP addresses and networks that are allowed without auth"**
> (`allowedNetworks`). That one only skips authentication and has no effect on the paywall.

This repo keeps the value in `.env` as `PLEX_LAN_NETWORKS` and pushes it on every deploy with
`scripts/sync-plex-prefs.sh`. It has to be a script: neither `lscr.io/linuxserver/plex` nor the
official `plexinc/pms-docker` can set this preference from the compose file. The official image
does expose `ADVERTISE_IP` and `ALLOWED_NETWORKS`, but not LAN Networks, and it applies them only
on the very first container run (`/.firstRunComplete` guard), so it would not converge either.

Verify what the server actually has:

```bash
sudo grep -o 'LanNetworksBandwidth="[^"]*"' \
  "appdata/plex/Library/Application Support/Plex Media Server/Preferences.xml"
```

To undo, run the same request with an empty value:

```bash
source .env
curl -s -X PUT -H "X-Plex-Token: $PLEX_API_TOKEN" \
  "http://localhost:32400/:/prefs?LanNetworksBandwidth="
```

---

## Further Reading

- [Tailscale docs: Block ads on all devices using Raspberry Pi](https://tailscale.com/docs/solutions/block-ads-all-devices-anywhere-using-raspberry-pi)
