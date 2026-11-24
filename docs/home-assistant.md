# Home Assistant

Home automation: lights, plugs, the robot vacuum and the rules between them. It runs as Home
Assistant **Container** on the main node only (profile `homeassistant`, group `home`), at
`https://home.<domain>` over the tailnet and `http://home.pi` on the LAN.

Container, not Home Assistant OS: HA OS wants the whole machine and would replace this stack. The
price is no Supervisor, so no add-ons (see [What it does not do](#what-it-does-not-do)).

## Where things live

| What | Where | Who changes it |
| :--- | :--- | :--- |
| `configuration.yaml` | `config/homeassistant/` in git, mounted read-only | a commit; the deploy restarts HA when it changes |
| HTTP server settings (reverse proxy) | `appdata/homeassistant/.storage/http` | the UI, once (see below) |
| Integrations, devices, users, dashboards | `appdata/homeassistant/.storage` | the UI |
| Automations, scripts, scenes | `appdata/homeassistant/{automations,scripts,scenes}.yaml` | the UI |
| History (the recorder database) | `appdata/homeassistant/home-assistant_v2.db` | HA |
| Community integrations from the Marketplace | `appdata/homeassistant/custom_components` | the Marketplace |

Everything under `appdata/` is in the nightly backup ([backups.md](backups.md)). Device passwords
and tokens are typed into the UI once and kept in `.storage`; they are not read from `.env`.

`configuration.yaml` is read-only on purpose: the UI cannot edit it anyway, and a hand edit inside
the container would be lost on the next recreate. The container's entrypoint creates the three
files it includes, because HA only writes them when it generates a configuration of its own.

## Why it is on the host network

HA finds devices with mDNS and SSDP, which are multicast and do not cross the Docker bridge. On
`media-network` it would see nothing. Caddy reaches it through `host.docker.internal:8123`.

## Behind Caddy: the reverse proxy setting

Until HA trusts Caddy as a proxy, every request through `https://home.<domain>` or `http://home.pi`
gets `400: Bad Request`, and the log says "A request from a reverse proxy was received from
172.19.x.x, but your HTTP integration is not set-up for reverse proxies".

The setting is not in `configuration.yaml`. HA keeps its HTTP settings in `.storage/http`: an `http:`
block in YAML is read once, staged as a *pending* config, and dropped if nobody confirms it in the UI
within five minutes. After that first read HA ignores the YAML for good, so putting it back does
nothing. Set it once from the LAN, where no proxy is involved:

1. Open `http://<STATIC_IP>:8123` directly.
2. **Settings → System → Network → HTTP server**: enable *Use X-Forwarded-For*, trusted proxies
   `172.16.0.0/12` (the Docker bridges Caddy can come from), save. HA restarts.
3. Within five minutes, open `https://home.<domain>` and confirm the new configuration when HA asks.
   Without the confirmation it goes back to the old settings on its own.

It is kept in `appdata`, so it survives upgrades and recreates, and comes back with a restore.

## The recorder and the SD card

`appdata` is on the SD card. The recorder commits every 30 s instead of every 5 s and keeps ten days
of history, which is enough for "when did the robot last run" and cheap on the card. Long-term
statistics (energy, daily totals) are kept apart by HA and are not purged.

## First start

1. Deploy, then open `http://<STATIC_IP>:8123` and create the owner account.
2. Trust Caddy as a proxy ([above](#behind-caddy-the-reverse-proxy-setting)); from then on use
   `https://home.<domain>`.
3. Set the home location: sunrise and sunset, and the phone app's "home" zone, come from it.
4. **Settings → Devices & services** lists what HA found on the LAN. Add the ones you want.

Devices change IP when the router's DHCP lease expires, and an integration set up by IP then loses
them. Reserve an address in the router for every device HA talks to.

## Adding a device that HA does not ship

Community integrations come from the **Marketplace** (Settings → Marketplace, or `/marketplace`),
which replaced HACS in 2026.11: the HACS installer refuses to run on 2026.11 and newer. Search the
catalog, or use **Add from link** with the GitHub repository when it is not listed. Installing is a
download into `appdata/homeassistant/custom_components`; no shell in the container is needed.

What it installs is not pinned in `versions.env` and Renovate does not see it: the Marketplace shows
its own updates. An integration installs its Python packages the first time it is set up, which is
a memory peak on top of HA itself; that is why `mem_limit` is 1536m and not less.

## The phone app

The companion app reports where the phone is, which is what arrive-home and leave-home rules use.
It reaches HA through the tailnet: set its internal URL to the Pi's LAN address
(`http://<STATIC_IP>:8123`) and its external URL to `https://home.<domain>`. When Tailscale is off on
the phone away from home, location updates queue on the phone until it can reach HA again.

## What it does not do

- **No add-ons.** Mosquitto, Zigbee2MQTT, Piper or Whisper would each be a service of their own in
  compose, like any other.
- **No working Bluetooth.** On the host network HA sees the Pi's adapter, but without `/run/dbus`
  mounted and the `NET_ADMIN` and `NET_RAW` capabilities it cannot use it: the entry fails with
  "DBus service not found". Delete it in the UI, and *Ignore* it if it is discovered again. For
  Bluetooth devices, see the proxy item in [PENDING.md](../PENDING.md).
- **Not on the internet.** There is no Cloudflare route. Cloud voice assistants (Alexa, Google) need
  one, or Nabu Casa; until then they cannot call HA.
- **Not on the second node.** It is not in streamhalla's service list, and the LAN it watches is the
  raspi's.
