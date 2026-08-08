# Plex Remote Access

## What this is for

Since April 2025, Plex charges to play **your own** media when you are away from home. Playing it
while sitting on your own network is still free. So the entire problem is this: when you connect
from outside through the VPN, Plex has to be convinced that you are still "at home".

It does not happen by itself, and the piece of Tailscale that sounds like it should do it (the exit
node) does not.

```
browser ──► 192.168.1.180:32400   Plex says "local"   free
        └─► 100.x.x.x:32400       Plex says "remote"  Remote Watch Pass prompt
```

Both addresses are the same Raspberry Pi. Plex treats them differently, and that is the whole story.

## Words you need first

These four terms appear everywhere below. Skip if you already know them.

| Term | What it means here |
| :--- | :--- |
| **RFC1918 address** | The address ranges reserved for private networks: `192.168.x.x`, `10.x.x.x`, `172.16–31.x.x`. If an address starts like that, it is "inside somebody's house or office" |
| **CGNAT range** | `100.64.0.0/10`, a range reserved for carriers. Tailscale hands out its addresses from here, so a Tailscale address is **not** RFC1918, and software that only recognises RFC1918 will not treat it as local |
| **Tailnet** | Your private Tailscale network: every device you have installed it on, able to reach each other from anywhere |
| **Exit node vs subnet router** | Two different jobs. An **exit node** sends your *internet* traffic out through that machine. A **subnet router** makes the *other devices on that machine's local network* reachable through the tunnel. Wanting the second and enabling the first is the classic mistake |

## How Plex decides local vs remote

Not by the source address of the connection, which is the intuitive guess and is wrong.

Plex Media Server reports every address it listens on up to plex.tv, and tags each one with a
`local` flag. The **client** (the browser, the app) downloads that list, picks a connection, and
believes the flag. Plex only flags RFC1918 addresses as local, so the Tailscale address is
permanently `local=false` and no setting on the server changes that.

You can ask plex.tv what it is publishing:

```bash
source .env
curl -s -H 'Accept: application/json' -H 'X-Plex-Client-Identifier: diag' \
  "https://plex.tv/api/v2/resources?includeHttps=1&X-Plex-Token=$PLEX_API_TOKEN" |
  jq -r '.[] | select(.provides|test("server")) | .connections[] | "\(.address) local=\(.local)"'
```

```
192.168.1.180  local=true      <- the client must end up using one of these
192.168.1.154  local=true
172.19.0.1     local=true
100.x.x.x      local=false     <- Tailscale, CGNAT, never flagged local
203.0.113.42   local=false     <- the home public address
```

So the goal is not "convince Plex the tunnel is local". It is **make the address Plex already calls
local reachable through the tunnel**.

## Step 1 — Make the home address reachable from the tailnet

By default it is not. An exit node carries your traffic to the internet; it deliberately does not
carry it to the network the exit node itself sits on. Reaching `192.168.1.180` from outside needs a
subnet route: the Pi announcing "I can pass traffic to this address", and you approving it.

```bash
sudo tailscale set --advertise-exit-node --advertise-routes=192.168.1.180/32,192.168.1.154/32
```

Then approve them in the admin console under Machines → raspi → Edit route settings. Nothing
appears there until the command above has been run.

Three details that all cause real breakage if ignored:

| Choice | Why it matters |
| :--- | :--- |
| `tailscale set`, never `tailscale up` | `up` resets every setting you did not name on the line, including `--accept-dns=false`. Losing that one breaks Pi-hole and Docker DNS on this box (see [tailscale.md](tailscale.md)) |
| `--advertise-exit-node` in the same command | `--advertise-routes` replaces the whole route list. On its own it silently removes the exit node |
| `/32` of the Pi's own addresses, not `192.168.1.0/24` | `/32` means "this one address". Announcing the whole `192.168.1.0/24` subnet would collide with every café and hotel network that also uses `192.168.1.x`. It also means a guest given these routes reaches the Pi and nothing else in the house |

This setting lives on the Pi, not in git. Undo it with
`sudo tailscale set --advertise-exit-node --advertise-routes=` and unapprove in the console.

## Step 2 — Tell Plex the tunnel counts as local

There is a second, independent decision. Step 1 fixes what the *client* believes. The *server* also
classifies each connection, and traffic arriving through the tunnel keeps the client's `100.x`
address as its source, so as far as Plex is concerned an outsider just connected.

Settings → Network → **LAN Networks**, described by Plex as "networks that will be considered
local":

```
100.64.0.0/10,192.168.1.0/24
```

Here that value is `PLEX_LAN_NETWORKS` in `.env`, pushed to Plex on every deploy by
`scripts/sync-plex-prefs.sh`. It has to be a script because **no Plex Docker image can set this
from the compose file**:

| Image | What it can set |
| :--- | :--- |
| `lscr.io/linuxserver/plex` (used here) | Nothing relevant. Only `PUID`, `PGID`, `TZ`, `VERSION`, `PLEX_CLAIM`, `UMASK` |
| `plexinc/pms-docker` (official) | `ADVERTISE_IP` and `ALLOWED_NETWORKS`, but not LAN Networks, and only on the very first container start, after which a `/.firstRunComplete` marker makes it skip them forever |

> **Two settings with confusingly similar names.** *LAN Networks* is the one above. *"List of IP
> addresses and networks that are allowed without auth"* (`allowedNetworks`) only skips the login
> prompt and has nothing to do with any of this.

## Step 3 — Never put Plex behind the reverse proxy

Every other service here is reached through Caddy. Plex must not be, and it is worth knowing why so
nobody "tidies it up" later.

Plex decides who you are from the address of the network socket, and ignores the `X-Forwarded-For`
header that proxies use to pass the real client along. Behind a `reverse_proxy`, every single client
arrives as Caddy's container address, and the whole scheme collapses.

So both Plex entries in the Caddyfile are `redir`, which sends the browser to the real address
instead of fetching on its behalf. Homepage links to that same address. The redirects are `302`
rather than `301` because browsers cache a `301` for months, long after the route changed.

There is an alternative, and it is worse: adding `172.16.0.0/12` to LAN Networks would make the
proxied connections count as local too. But then *every* client behind Caddy looks local, per-user
bandwidth limits stop applying to anyone, and the statistics can no longer tell who was where.

## Letting other people watch

The intuitive move is to share the Pi as a Tailscale machine. It does not work, and Tailscale says
why in as many words: *"Shared machines do not advertise subnets to the tailnets they're shared
into, while inviting external users into your tailnet will give them access to subnet routers."*

Without the subnet routes the guest can only reach the `100.x` address, which is the one that
triggers the paywall. Sharing the machine gets them a Plex they have to pay for.

| Option | Cost | What to watch out for |
| :--- | :--- | :--- |
| **Plex Pass on the server owner's account** | One subscription | Nothing. It covers the owner and everyone they share with, from anywhere, with no network work at all |
| **Invite them into the tailnet** as an external user | Free | Write the access policy **before** inviting, or they reach every machine in the house: step 7 of [tailscale.md](tailscale.md) has it. Only works on devices that can run Tailscale, so not on a TV or a Chromecast. And counting as "local" means per-user bandwidth limits no longer apply to them |
| **They buy their own Remote Watch Pass** | One subscription per person | Covers only their account and nobody else's |

For more than a person or two, buy the Plex Pass. Plex documents none of this `local` flag
behaviour, which means it can change without warning: the network route stops working the day Plex
changes how it decides, and a subscription does not.

## Checking it works

```bash
# The pretty URL should bounce to the 192.168.x address, not proxy it
curl -sk -o /dev/null -w '%{redirect_url}\n' https://plex.platanosverdes.com/

# The LAN Networks preference is really set on the server
sudo grep -o 'LanNetworksBandwidth="[^"]*"' \
  "appdata/plex/Library/Application Support/Plex Media Server/Preferences.xml"

# The subnet routes reached this client, and the address answers
route -n get 192.168.1.180 | grep interface     # macOS: expect a utun (tunnel) interface
curl -s -o /dev/null -w '%{http_code}\n' http://192.168.1.180:32400/identity
```

## If it stops working

- **The paywall is back.** Hard reload the client first. The web app remembers which connection it
  chose, and opening it from the `100.x` address pins it to the remote one.
- **`192.168.1.180` times out.** Either the routes are advertised but never approved in the admin
  console, or this client is ignoring routes: check `tailscale debug prefs` for `RouteAll: true`.
- **Everything broke right after someone ran `tailscale up`.** That command resets what it was not
  told to keep: the routes are gone and so is `--accept-dns=false`. Re-run the `tailscale set` line
  from step 1, then check `/etc/resolv.conf` still points at `127.0.0.1`.
- **LAN Networks is empty again.** The container rewrites its preferences file when it shuts down.
  `bash scripts/sync-plex-prefs.sh` puts it back, and any deploy does that automatically.

## References

- [Machine sharing | Tailscale](https://tailscale.com/kb/1084/sharing)
- [Subnet routers | Tailscale](https://tailscale.com/kb/1019/subnets)
- [Remote Watch Pass Overview | Plex Support](https://support.plex.tv/articles/remote-watch-pass-overview/)
- [Requirements for Remote Playback of Personal Media | Plex Support](https://support.plex.tv/articles/requirements-for-remote-playback-of-personal-media/)
- [Important 2025 Plex Updates | Plex](https://www.plex.tv/blog/important-2025-plex-updates/)
