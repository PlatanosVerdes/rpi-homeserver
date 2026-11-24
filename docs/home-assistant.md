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
| Integrations, devices, users, dashboards | `appdata/homeassistant/.storage` | the UI |
| Automations, scripts, scenes | `appdata/homeassistant/{automations,scripts,scenes}.yaml` | the UI |
| History (the recorder database) | `appdata/homeassistant/home-assistant_v2.db` | HA |
| Custom integrations from HACS | `appdata/homeassistant/custom_components` | HACS |

Everything under `appdata/` is in the nightly backup ([backups.md](backups.md)). Device passwords
and tokens are typed into the UI once and kept in `.storage`; they are not read from `.env`.

`configuration.yaml` is read-only on purpose: the UI cannot edit it anyway, and a hand edit inside
the container would be lost on the next recreate. The container's entrypoint creates the three
files it includes, because HA only writes them when it generates a configuration of its own.

## Why it is on the host network

HA finds devices with mDNS and SSDP, which are multicast and do not cross the Docker bridge. On
`media-network` it would see nothing. Caddy reaches it through `host.docker.internal:8123`, and
`configuration.yaml` trusts `172.16.0.0/12` as a proxy: without that, HA rejects every request Caddy
forwards with a 400.

## The recorder and the SD card

`appdata` is on the SD card. The recorder commits every 30 s instead of every 5 s and keeps ten days
of history, which is enough for "when did the robot last run" and cheap on the card. Long-term
statistics (energy, daily totals) are kept apart by HA and are not purged.

## First start

1. Deploy, then open `https://home.<domain>` and create the owner account.
2. Set the home location: sunrise and sunset, and the phone app's "home" zone, come from it.
3. **Settings → Devices & services** lists what HA found on the LAN. Add the ones you want.

Devices change IP when the router's DHCP lease expires, and an integration set up by IP then loses
them. Reserve an address in the router for every device HA talks to.

## Adding a device that HA does not ship

Some integrations are community ones, installed through HACS. HACS installs into `appdata`:

```bash
docker exec -it homeassistant bash -c "wget -O - https://get.hacs.xyz | bash -"
docker restart homeassistant
```

Then **Settings → Devices & services → Add → HACS**, and log in with GitHub. What HACS installs is
not pinned in `versions.env` and Renovate does not see it: HACS shows its own updates in the UI.

## The phone app

The companion app reports where the phone is, which is what arrive-home and leave-home rules use.
It reaches HA through the tailnet: set its internal URL to the Pi's LAN address
(`http://<STATIC_IP>:8123`) and its external URL to `https://home.<domain>`. When Tailscale is off on
the phone away from home, location updates queue on the phone until it can reach HA again.

## What it does not do

- **No add-ons.** Mosquitto, Zigbee2MQTT, Piper or Whisper would each be a service of their own in
  compose, like any other.
- **No Bluetooth.** `/run/dbus` is not mounted, so the Bluetooth integration finds no adapter.
- **Not on the internet.** There is no Cloudflare route. Cloud voice assistants (Alexa, Google) need
  one, or Nabu Casa; until then they cannot call HA.
- **Not on the second node.** It is not in streamhalla's service list, and the LAN it watches is the
  raspi's.
