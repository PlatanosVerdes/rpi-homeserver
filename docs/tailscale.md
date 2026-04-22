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

## Further Reading

- [Tailscale docs: Block ads on all devices using Raspberry Pi](https://tailscale.com/docs/solutions/block-ads-all-devices-anywhere-using-raspberry-pi)
