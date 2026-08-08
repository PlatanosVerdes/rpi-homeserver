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

## Step 6 — Plex over the Tailnet

Plex charges for remote playback, and a tailnet connection does not count as local on its own: the
exit node does not reach the Pi's LAN, so the Pi advertises `192.168.1.180/32,192.168.1.154/32` as
subnet routes for it.

→ See [plex-remote-access.md](plex-remote-access.md) for the whole picture, including why
`tailscale up` must never be used to change this and what breaks if it is.

---

## Step 7 — Giving Someone Else Access

Before inviting anyone, understand what the tailnet grants by default: **everything**. With no
policy of its own a tailnet is allow-all, so an invited person reaches every device on every port,
including the other machines in the house and SSH on the Pi. Write the policy first, invite second.

### Invite, do not share

Two different features, and only one of them works here:

| | What the other person gets |
| :--- | :--- |
| **Share a machine** (Machines → Share) | The device itself, and it can be used as an exit node. **Not its subnet routes**, which Tailscale states outright: *"Shared machines do not advertise subnets to the tailnets they're shared into"* |
| **Invite an external user** (Settings → Users → Invite) | A place in the tailnet, subnet routes included, restricted by whatever the policy says |

Sharing the machine is the intuitive choice and it is the wrong one: without the subnet routes the
guest can only reach Plex on the `100.x` address, which is the one that triggers the paywall (see
[plex-remote-access.md](plex-remote-access.md)).

### The policy

Paste into Access controls, replacing the two placeholder addresses:

```json
{
  "acls": [
    {"action": "accept", "src": ["owner@example.com"], "dst": ["*:*"]},
    {"action": "accept", "src": ["owner@example.com"], "dst": ["autogroup:internet:*"]},

    {"action": "accept",
     "src": ["friend@example.com"],
     "dst": ["100.x.x.x:*", "192.168.1.180:*", "192.168.1.154:*"]}
  ],

  "tests": [
    {"src": "friend@example.com",
     "accept": ["192.168.1.180:32400"],
     "deny":   ["100.y.y.y:22"]}
  ]
}
```

| Rule | Why it is there |
| :--- | :--- |
| Owner → `*:*` | Defining any ACL flips the tailnet to **deny by default**. Without this line you lock yourself out of your own machines |
| Owner → `autogroup:internet:*` | **The exit node stops working without it.** Allowing the exit node device as a destination is not the same thing: *"That only permits connections to the exit node, such as SSH. It does not permit using the device as an internet gateway"* |
| Friend → the Pi's three addresses (`100.x.x.x` is `TAILSCALE_IP`, `100.y.y.y` any other machine) | The tailnet address plus the two advertised `/32`s, so Plex resolves to a `local=true` connection. All ports, so they also get Grafana and the rest of the Pi. Narrow the ports if that is not wanted |
| `tests` | Tailscale refuses to save a policy whose tests fail, so the guest being locked out of the other machines is enforced, not assumed |

Press **Preview** before saving. A policy that denies your own account is recoverable only from
another admin session.

### Why the /32 routes matter here

The guest rule hands over the advertised subnet routes, and those are only `192.168.1.180/32` and
`192.168.1.154/32`, both of which are the Pi itself. Nothing else on the home LAN is reachable
through them. Had the Pi advertised `192.168.1.0/24`, that same rule would have handed the guest
every device in the house.

### What this does not touch

Devices on the home LAN without Tailscale (a TV, a phone on the wifi) never enter the tailnet at
all, so no policy applies to them and local playback is unaffected. A machine that *does* run
Tailscale at home reaches the Pi through the tunnel because of the `/32` routes, so it is the
owner's own rule above that keeps it working.

---

## Further Reading

- [Tailscale docs: Block ads on all devices using Raspberry Pi](https://tailscale.com/docs/solutions/block-ads-all-devices-anywhere-using-raspberry-pi)
