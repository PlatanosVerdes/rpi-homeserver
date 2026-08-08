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

### What actually decides it

Not the source IP. Plex publishes each of its addresses to plex.tv with a per-connection `local`
flag, and the client reads that flag. Plex only flags RFC1918 addresses as local, so the Tailscale
address is always `local=false` and no server preference changes it:

```bash
source .env
curl -s -H 'Accept: application/json' -H 'X-Plex-Client-Identifier: diag' \
  "https://plex.tv/api/v2/resources?includeHttps=1&X-Plex-Token=$PLEX_API_TOKEN" |
  jq -r '.[] | select(.provides|test("server")) | .connections[] | "\(.address) local=\(.local)"'
```

```
192.168.1.180  local=true      <- what the client must use
100.125.71.20  local=false     <- CGNAT range, never flagged local
```

So the fix is to make the `local=true` address reachable from the tailnet. An exit node does **not**
do this: it routes traffic to the internet, not to its own LAN. That needs subnet routes, advertised
on the Pi and then approved in the admin console:

```bash
sudo tailscale set --advertise-exit-node --advertise-routes=192.168.1.180/32,192.168.1.154/32
```

Use `/32` of the Pi's own addresses rather than `192.168.1.0/24`: advertising the whole subnet
collides with every foreign network that also uses `192.168.1.x`. Pass `--advertise-exit-node` in
the same call or the exit node advertisement is dropped, and use `tailscale set`, never
`tailscale up`, which would reset `--accept-dns=false` and break Pi-hole (see Step 3).

### Server side: LAN Networks

Traffic to `192.168.1.180` arrives through the tunnel keeping the client's `100.x` source address,
so the *server's* own local/remote classification still needs Settings > Network > **LAN Networks**
(`LanNetworksBandwidth`), whose description is literally "networks that will be considered local":

```
100.64.0.0/10,192.168.1.0/24
```

> Not the same as **"List of IP addresses and networks that are allowed without auth"**
> (`allowedNetworks`). That one only skips authentication. On its own it does nothing for the
> paywall, and neither does this one: the paywall is the client-side `local` flag above.

**Plex must not be proxied.** It classifies a client by the socket's peer address and ignores
`X-Forwarded-For`, so a `reverse_proxy` makes every client arrive as Caddy's container IP on
`media-network`. Both Plex routes are therefore `redir` to `{env.STATIC_IP}:32400`, never
`reverse_proxy`, and Homepage links to the same address. The redirects are 302 on purpose: a 301
would sit in browser caches long after the route changed.

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
