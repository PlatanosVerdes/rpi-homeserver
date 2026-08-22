# Tailscale — Remote Access, Exit Node & Pi-hole DNS

## What this is for

The Raspberry Pi sits at home behind a router, and the internet cannot reach it. Tailscale solves
that by building a small private network of your own devices, called a **tailnet**: your laptop,
your phone and the Pi all get a permanent address in the `100.x.x.x` range and can talk to each
other from anywhere, encrypted, without opening a single port on the router.

This guide sets the Pi up as three things. They are related but separate, and mixing them up is the
usual source of confusion:

| Role | What it does | What it does **not** do |
| :--- | :--- | :--- |
| **Tailnet member** | The Pi is reachable at its `100.x.x.x` address from any of your devices | Nothing else is reachable through it |
| **Exit node** | Your other devices can send their *internet* traffic out through your home connection, as if browsing from home | It does **not** give access to the other machines on the home network, not even to the Pi's own `192.168.x.x` address |
| **Subnet router** | Makes specific addresses *on the home network* reachable through the tunnel | Only the addresses you explicitly advertise and approve |

That last distinction is the one that catches people out: "I turned on the exit node, why can't I
reach my NAS at 192.168.1.50?" Because that is the subnet router's job, and it is configured
separately.

Pi-hole is then set as the DNS server for the whole tailnet, so ad-blocking applies on every device
and the `*.platanosverdes.com` names resolve from anywhere.

---

## Step 1 — Assign a Static IP

Give the Pi an address on the home network that never changes, by reserving it in the router's DHCP
settings against the Pi's MAC address. Everything else here points at that address, so it moving
would quietly break the lot. Put it in `STATIC_IP` in `.env`.

---

## Step 2 — Enable IP Forwarding

By default Linux only accepts traffic addressed to itself and drops anything meant to pass through.
Both the exit node and the subnet router are exactly that: passing traffic through. This turns it
on permanently.

```bash
echo 'net.ipv4.ip_forward = 1' | sudo tee -a /etc/sysctl.d/99-tailscale.conf
echo 'net.ipv6.conf.all.forwarding = 1' | sudo tee -a /etc/sysctl.d/99-tailscale.conf
sudo sysctl -p /etc/sysctl.d/99-tailscale.conf
```

---

## Step 3 — Start Tailscale

`up` is for the first login and nothing else, and it is the last time it should be used on this
machine:

```bash
sudo tailscale up --accept-dns=false
```

Everything after that is `set`, which changes only the flags it is given. This is the line the Pi
is actually running:

```bash
sudo tailscale set --advertise-exit-node --advertise-routes=192.168.1.180/32,192.168.1.154/32 --accept-dns=false
```

| Flag | Why |
| :--- | :--- |
| `--advertise-exit-node` | Offers the home connection as an exit node. It has to be repeated in any command that touches `--advertise-routes`, because that flag replaces the whole route list and would otherwise drop it |
| `--advertise-routes=192.168.1.180/32,192.168.1.154/32` | The Pi's own wlan0 and eth0 addresses, and nothing else on the LAN. These are what keep Plex free from outside (see [plex-remote-access.md](plex-remote-access.md)). A `192.168.1.0/24` here would hand over every device in the house, and collide with every café and hotel network using the same range |
| `--accept-dns=false` | See below |

> `--accept-dns=false` is **critical**, and specific to this machine. Tailscale normally rewrites
> `/etc/resolv.conf` so a device uses the tailnet's DNS server. That server *is* this Pi, so letting
> it happen here points Pi-hole at itself and takes DNS down for the house and for every container.
>
> This is also why these docs insist on `tailscale set` rather than `tailscale up`: `up` resets
> everything you did not name on the command line, and this flag is the one that hurts.

The routes show up in the admin console only after the `set` command has run, and they carry no
traffic until they are approved there (step 4).

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
