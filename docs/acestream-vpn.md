# Acestream through the VPN

The engine pulls live streams from a public P2P swarm, from home, over the ISP's line. Two
things follow from that, and only one of them is about privacy:

- every peer in the swarm sees this house's IP, and so does anyone logging those swarms;
- the line itself interferes. Trackers and indexers go unreachable during football windows,
  with a self-signed `core1.netops.test` answering on any SNI — the interception happens
  before the request leaves, so no application setting fixes it.

Routing the engine through NordVPN answers both: the traffic leaves the Pi already encrypted
and comes out of the provider.

## How it is wired

`gluetun-ace` holds the tunnel and `aceserve` joins its network namespace:

```
jellyfin ──► acestream-proxy :6879 ──► gluetun-ace :6878 ──► [ aceserve ] ══VPN══► swarm
                                            ▲
LAN / tailnet ──── 192.168.1.180:6878 ──────┘   (farnsworth hands this URL to VLC)
```

`aceserve` has no network interfaces of its own any more — it used to be on host networking.
Everything that used to reach it at `172.19.0.1:6878`, the bridge gateway, now reaches it at
`gluetun-ace:6878` over `media-network`: `ACESERVE_BASE_URL` in the warmup proxy and
`ACESERVE_URL` in the updater's health checks.

Consequences worth knowing before touching this:

- **Ports belong to gluetun.** A container in another container's namespace cannot publish
  anything, so `6878:6878` is declared on `gluetun-ace`. That is what keeps farnsworth's
  `STREAM_BASE` (`http://${STATIC_IP}:6878`) working with no change.
- **`FIREWALL_INPUT_PORTS=6878` is not optional.** Publishing the port is not enough: gluetun
  firewalls its own namespace, and an arriving connection is inbound as far as it is concerned.
  Without it the port answers nothing and the symptom looks like a dead engine.
- **The tunnel is a kill switch.** With no route out except the VPN, the engine stops when the
  tunnel stops instead of falling back to the naked line. That is the point, and it also means
  a `gluetun-ace` restart takes `aceserve` with it: `docker compose up -d` puts both back, which
  is what the deploy already runs.
- **DNS moves too.** gluetun resolves over DoT inside the tunnel, so the engine no longer asks
  the Pi-hole. For this container that is the intended behaviour: the interception described
  above is exactly what it needs to avoid.

## What it does not fix

Nord forwards no port, as policy. The engine can open connections but nothing in the swarm can
open one to it, so it seeds nothing and its peer list is whatever it managed to dial. Playback
works that way, but a channel can take longer to start.

That is what the warmup proxy is for — it retries HTTP 500 for 45 s while the swarm forms — and
it is the number to raise first if channels start timing out after this change. If they still
do, the honest reading is that this provider is the wrong tool and one that forwards a port
(Proton, PIA) is the fix, not more retries. See
[seeding-and-ratio.md](seeding-and-ratio.md) for the provider comparison.

## Credentials

Both modes are in `.env`, and only one is read:

| `VPN_TYPE` | Reads | Where it comes from |
| :--- | :--- | :--- |
| `openvpn` | `NORDVPN_USER`, `NORDVPN_PASS`, `VPN_PROTOCOL` | Nord dashboard → Manual setup → service credentials |
| `wireguard` | `WIREGUARD_PRIVATE_KEY` | an access token with *Get service credentials*, then `curl -s -u token:<TOKEN> https://api.nordvpn.com/v1/users/services/credentials \| jq -r .nordlynx_private_key` |

WireGuard is the one to want here: OpenVPN encrypts on the CPU, and on a Pi that ceiling lands
on exactly the traffic being tunnelled. The service credentials cannot produce the private key,
which is why both sets exist.
