# Plex Remote Access

Since April 2025 Plex charges for playing personal media **remotely**: the server owner buys a Plex
Pass, which covers everyone they share with, or each viewer buys their own Remote Watch Pass.
Playing **locally** stays free, so the whole job is making a tailnet connection count as local. It
is not automatic, and the exit node does not do it.

```
browser ──► 192.168.1.180:32400   local=true   free
        └─► 100.x.x.x:32400   local=false  Remote Watch Pass prompt
```

---

## What decides remote vs local

Not the source IP of the connection. Plex publishes every address it listens on to plex.tv, each
with its own `local` flag, and the **client** reads that flag to decide. Only RFC1918 addresses are
ever flagged local, so the `100.64.0.0/10` Tailscale address is permanently `local=false` and no
server preference changes it.

Ask plex.tv what it is publishing:

```bash
source .env
curl -s -H 'Accept: application/json' -H 'X-Plex-Client-Identifier: diag' \
  "https://plex.tv/api/v2/resources?includeHttps=1&X-Plex-Token=$PLEX_API_TOKEN" |
  jq -r '.[] | select(.provides|test("server")) | .connections[] | "\(.address) local=\(.local)"'
```

```
192.168.1.180  local=true      <- the client must use this one
192.168.1.154  local=true
172.19.0.1     local=true
100.x.x.x  local=false     <- CGNAT range, never flagged local
203.0.113.42   local=false
```

## Reaching the local address from the tailnet

An exit node routes traffic to the **internet**, not to its own LAN, so it does not make
`192.168.1.180` reachable. That needs subnet routes, advertised on the Pi and then approved under
Machines → raspi → Edit route settings:

```bash
sudo tailscale set --advertise-exit-node --advertise-routes=192.168.1.180/32,192.168.1.154/32
```

| Choice | Why |
| :--- | :--- |
| `tailscale set`, never `tailscale up` | `up` resets every unspecified pref, including `--accept-dns=false`, which breaks Pi-hole and Docker DNS (see [tailscale.md](tailscale.md)) |
| `--advertise-exit-node` in the same call | `--advertise-routes` alone replaces the route list and drops the exit node |
| `/32` of the Pi's own IPs, not `192.168.1.0/24` | A whole-subnet route collides with every foreign network that also uses `192.168.1.x`. Two host routes cannot |

This lives on the host, not in git. Undo with `sudo tailscale set --advertise-exit-node
--advertise-routes=` plus unapproving them in the admin console.

## Server side: LAN Networks

Traffic to `192.168.1.180` arrives through the tunnel keeping the client's `100.x` source address
(confirmed in `Plex Media Server.log`), so the server's own classification still needs
Settings → Network → **LAN Networks**, whose description is literally "networks that will be
considered local":

```
100.64.0.0/10,192.168.1.0/24
```

That is `PLEX_LAN_NETWORKS` in `.env`, pushed on every deploy by `scripts/sync-plex-prefs.sh`. It
has to be a script because **no Plex image can set this from compose**:

| Image | Preference support |
| :--- | :--- |
| `lscr.io/linuxserver/plex` (used here) | None. Only `PUID`, `PGID`, `TZ`, `VERSION`, `PLEX_CLAIM`, `UMASK` |
| `plexinc/pms-docker` (official) | `ADVERTISE_IP` and `ALLOWED_NETWORKS` only, no LAN Networks, and they are applied once and then skipped forever behind a `/.firstRunComplete` guard |

> Do not confuse LAN Networks with **"List of IP addresses and networks that are allowed without
> auth"** (`allowedNetworks`). That one only skips authentication. Neither of them silences the
> paywall on its own: that is the client-side `local` flag above.

## Never proxy Plex

Plex classifies a client by the socket's peer address and ignores `X-Forwarded-For`, so a
`reverse_proxy` makes every client arrive as Caddy's container IP on `media-network`. Both Plex
routes in the Caddyfile are therefore `redir` to `{env.STATIC_IP}:32400`, and Homepage links to the
same address. The redirects are 302 on purpose: a 301 would sit in browser caches long after the
route changed.

Adding `172.16.0.0/12` to LAN Networks would silence the paywall behind the proxy instead, at the
cost of making *every* client behind Caddy look local: per-user bandwidth limits stop applying and
the stats can no longer tell local from remote. The redirect keeps the distinction intact.

## Sharing the server with other people

Sharing the Pi as a Tailscale **machine** does not work, and Tailscale says so outright: *"Shared
machines do not advertise subnets to the tailnets they're shared into, while inviting external users
into your tailnet will give them access to subnet routers."* The recipient can use it as an exit
node, but an exit node never reaches the Pi's own LAN, so they land on the `100.x` address and get
the paywall on their account.

| Option | What it costs | Catch |
| :--- | :--- | :--- |
| **Plex Pass on the owner's account** | One subscription | None. Covers the owner and every shared user, remotely, with no network setup |
| **Invite the friend into the tailnet** as an external user | Free | Needs an ACL written **before** inviting, or they reach every machine in the house: step 7 of [tailscale.md](tailscale.md) has the policy. Only works on devices that can run Tailscale, so no TVs or Chromecasts. As "local" they escape the per-user bandwidth limits |
| **Their own Remote Watch Pass** | One subscription per viewer | Covers only that account, does not extend to anyone else |

Prefer the Plex Pass for more than a person or two. Plex documents none of this `local` flag
behaviour, so treat it as something that can change without warning: the network route stops
working the day Plex changes how it decides, and the subscription does not.

## Verifying

```bash
# What the server is publishing (should be local=true on 192.168.1.x)
curl -sk -o /dev/null -w '%{redirect_url}\n' https://plex.platanosverdes.com/

# The preference actually stuck
sudo grep -o 'LanNetworksBandwidth="[^"]*"' \
  "appdata/plex/Library/Application Support/Plex Media Server/Preferences.xml"

# The subnet routes arrived on the client
route -n get 192.168.1.180 | grep interface     # macOS, expect a utun interface
curl -s -o /dev/null -w '%{http_code}\n' http://192.168.1.180:32400/identity
```

## Troubleshooting

- **Paywall still shows after the routes were approved.** Hard reload the client. The web app caches
  which connection it picked, and reloading from the `100.x` origin forces the non-local one.
- **`192.168.1.180` times out.** The routes are advertised but not approved, or the client is not
  accepting routes: check `tailscale debug prefs` for `RouteAll: true` on the client side.
- **It broke after running `tailscale up`.** That resets `--accept-dns=false` and drops the routes.
  Re-run the `tailscale set` line above and check `/etc/resolv.conf` still points at `127.0.0.1`.
- **Plex was restarted and LAN Networks is empty.** The container rewrites `Preferences.xml` on
  shutdown. `bash scripts/sync-plex-prefs.sh` puts it back, and a deploy does it automatically.

## References

- [Machine sharing | Tailscale](https://tailscale.com/kb/1084/sharing)
- [Subnet routers | Tailscale](https://tailscale.com/kb/1019/subnets)
- [Remote Watch Pass Overview | Plex Support](https://support.plex.tv/articles/remote-watch-pass-overview/)
- [Requirements for Remote Playback of Personal Media | Plex Support](https://support.plex.tv/articles/requirements-for-remote-playback-of-personal-media/)
- [Important 2025 Plex Updates | Plex](https://www.plex.tv/blog/important-2025-plex-updates/)
