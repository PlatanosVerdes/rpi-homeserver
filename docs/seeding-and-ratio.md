# Seeding and Ratio

Why this box downloads from anywhere and seeds almost nothing on private trackers, what the options
are, and which one people actually pick.

**The policy that decides what gets deleted and when, and the per-tracker rules and configuration,
live in [private-trackers.md](private-trackers.md).** This page is the diagnosis underneath it: the
measurements, why a port forward was impossible on this line, and what changed.

## The measurement that explains everything

Same Pi, same connection, same qBittorrent, same week:

| | Torrents | Uploaded | Downloaded | Ratio | Uploaded anything |
| :--- | ---: | ---: | ---: | ---: | ---: |
| Public trackers | 37 | 397.6 GB | 210.5 GB | **1.889** | **36 of 37** |
| Private trackers | 27 | 3.6 GB | 573.1 GB | **0.006** | 8 of 27 |

Per tracker, the private side is worse than the average suggests: TorrentLeech 0.3 GB up on 365 GB
down (0.001), DigitalCore 1.1 on 178.7 (0.006), c411 2.2 on 29.3 (0.076).

## Why downloading works and seeding does not

They are opposite directions on the network.

- **Downloading is outbound.** You open the connection to whoever has the file. Outbound always
  works, from any network, with nothing configured. It is the same thing a browser does.
- **Seeding is inbound.** The peer who wants your file has to open a connection **to you**.

And BitTorrent has a hard rule: **two closed ends can never talk**. One of them must be reachable.
With no reachable port this box is always the closed end, so the only uploads it can make are to
peers that are open *and* that it dialled out to first.

That is why public swarms hide the problem. Public torrents use **DHT and PEX**, which hand out
hundreds of peers, so there is always someone open to dial. Inception alone uploaded 101 GB that way.

A private tracker's torrents carry the `private` flag, and that flag **disables DHT, PEX and LSD by
design**. The only peers that exist are the ones the tracker returns on announce: few, mostly other
seeders (nobody to upload to), and the handful of real leechers are served in milliseconds by
seedboxes sitting in datacentres with open ports and 10 Gb uplinks.

The crumbs that did get through are exactly the dial-out-and-got-lucky cases:

```
2.23 GB  Spider.Man.Homecoming.2017.BONUS.VOSTFR
0.59 GB  The.Furious.2025.1080p.BluRay.x264-GAZER
0.47 GB  Super.Mario.Galaxy.La.Pelicula.2026
0.24 GB  Marty.Supreme.2025.REPACK.2160p
0.02 GB  Pokemon.Detective.Pikachu.2019
```

Measured twice, before and after fixing the port mismatch: **zero incoming peers**, out of 35 and 58
peers respectively. See [../SYSTEM_NOTES.md](../SYSTEM_NOTES.md) section 10 for the three places that
have to agree on the port number, and how to check inbound from the peer flags rather than from
qBittorrent's status field, which says `connected` throughout either way.

## The options, and what people actually do

These are **alternatives, not steps**. The first and the third are two ways to get the same thing,
and they are mutually exclusive in practice: if the traffic leaves through a VPN, a forward on the
home router is pointless, because peers look for the VPN's address and port, not yours.

| | What it fixes | What it costs | Who picks it |
| :--- | :--- | :--- | :--- |
| **qBittorrent inside gluetun**, on a VPN that forwards a port | ratio **and** the ISP seeing torrent traffic, with no hole in the home router | a subscription, and one evening of setup | most of the homelab world; it is the default recipe |
| **Seedbox** | ratio, completely: a datacentre uplink seeds every new release for you, and home never appears in a swarm | more per month | anyone serious about private trackers |
| ~~**Port forward at home**~~ | **nothing on this line**, see CGNAT below | free | not available here |

The mixed setup is what people with years in this do: **public over the VPN, private on a seedbox.**
More moving parts, best of both.

### This line is behind CGNAT, so no port forward can ever work

Measured on 2026-08-20, after configuring the forward correctly on the router (rule enabled, source
filter open, `57429` to `192.168.1.154` on TCP and UDP) and getting nothing:

```
what the internet sees      79.116.217.174     looks like a normal public address
what the router holds       100.108.161.199    inside 100.64.0.0/10, which is CGNAT
packets arriving at the Pi  0                  tcpdump on port 57429 while probing from outside
```

The router's own WAN address comes from asking it over UPnP, which needs no login:

```bash
# SSDP discovery, then GetExternalIPAddress on the IGD
python3 - <<'EOF'   # full script in the git history of this file
...
EOF
# IGD en 192.168.1.1 -> http://192.168.1.1:52869/gatedesc.xml
# IP de la WAN segun el router: 100.108.161.199
```

With CGNAT the public address is shared with other customers and the carrier's NAT has no rule
sending port 57429 here, so the inbound connection dies upstream of the house. The forward is
configured correctly and simply cannot receive anything. There is no IPv6 escape either: the Pi has
only ULA and Tailscale addresses, no global `2000::/3` and no default IPv6 route, so Digi is not
delivering usable IPv6 on this line.

That leaves three ways to inbound, and the free one is a phone call:

1. **Ask Digi for a public IPv4.** They hand one out on request. The forward already configured then
   starts working with nothing else to change.
2. **A VPN that forwards a port** (Proton or PIA through gluetun). This is the only option that does
   not depend on the ISP at all: the listening port lives at the provider, so CGNAT stops mattering.
3. **A seedbox**, which sidesteps the house entirely.

### How gluetun changes the picture

`gluetun` holds the VPN (WireGuard) and qBittorrent joins its network namespace, so it has no other
route out. If the VPN drops, torrent traffic **stops** instead of leaking to the ISP: that is a real
kill switch, not a checkbox. Gluetun negotiates the forwarded port with the provider and hands it to
qBittorrent, which is where the inbound connections (the ratio) come from, without opening anything
at home. Two consequences for this repo when it happens: the WebUI is published on gluetun instead,
and the *arrs have to talk to `gluetun:8080` rather than `qbittorrent:8080`.

### Provider facts, checked 2026-08-20

- **NordVPN does not offer port forwarding**, and says so as policy: it calls the feature a security
  risk because it lets remote connections reach a device. It does allow P2P on optimised servers, so
  with Nord you can have the privacy half **today** and the ratio half never.
- **ProtonVPN** and **PIA** are the two with *automatic* port forwarding in gluetun (PIA on every
  server except the US ones). **AirVPN** works too, but the port is set by hand once, since it does
  not change.
- The SOCKS5 proxy already configured here (`nl.socks.nordhold.net`, tag `nordvpn`) is used by
  Prowlarr for **searching** c411 and touches no torrent traffic. No commercial proxy fixes this
  anyway: accepting inbound through a proxy needs SOCKS5 BIND, which none of them allow. A proxy
  hides the IP; it does not make the box reachable.

## The risk that is actually present

Verified from outside the network on 2026-08-20: the service domains do **not** resolve publicly and
443 is closed. Nothing is exposed to the internet, and admin access goes through Tailscale. That is
already the right posture, and an open BitTorrent port would not change it: the peer protocol has no
login, no shell and no file access.

The real exposure is the other one: **397 GB seeded across 37 public torrents from the home IP**,
visible to everyone in those swarms, including the firms that log them. That, not an open port, is
the argument for routing torrent traffic through a VPN.

## Kept on purpose

The 16 TorrentLeech torrents (365 GB) stay. If the account comes back, seeding exactly those is how
the ratio gets repaired, so deleting them would throw away the only asset available for that. They
announce `unregistered torrent pass` in the meantime, which means the tracker counts nothing:
`seed-cleanup.py` will still retire them after 240 h of **local** seeding time, so while this lasts
the seeding rule protects nothing on that tracker.

## Unrelated but adjacent: the advertised routes

Tailscale needs no LAN address, and Pi-hole does not either (every record points at the tailnet
address `100.125.71.20`, which never changes; only `local.pi` uses a LAN IP). The one thing that does
depend on a LAN address is the pair of subnet routes advertised so Plex sees the client as local:

```
PrimaryRoutes: ['192.168.1.154/32', '192.168.1.180/32']
```

Those numbers are typed in by hand and nothing checks them. The wifi address is pinned by a DHCP
reservation; the cable one is a plain lease, so if it ever changes, Tailscale keeps announcing an
address the Pi no longer has and **Plex remote breaks silently**. A DHCP reservation for the eth0 MAC
is the no-regrets fix, and the same pinning is what a port forward would need.
