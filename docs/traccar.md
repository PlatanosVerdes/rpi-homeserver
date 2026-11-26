# Traccar: a GPS tracker server

[Traccar](https://www.traccar.org/) receives positions from GPS trackers, shows them on a map and
raises events (movement, power cut, leaving an area). Here it replaces the subscription a tracker
vendor would charge for. It is set up for Teltonika devices (FMB/FMC/FMM series); another brand
needs its protocol added to `PROTOCOLS_ENABLE` and its port published.

## How a position gets here

```
tracker --(mobile data, TCP 5027)--> router --(port forward)--> Pi:5027 --> traccar
you --(tailnet)--> https://traccar.<domain> --> Caddy --> traccar:8082
```

The tracker opens a plain TCP connection to the house, so 5027 has to be reachable from the internet:
the Cloudflare tunnel only carries HTTP. The web panel does not need that and stays on the tailnet
like every other admin panel.

Unknown devices are dropped: anything that connects to 5027 without an IMEI registered in the panel
gets a line in the log and nothing else. That port is the whole public surface.

## Setting it up

1. **A hostname for the house.** The ISP changes the public IPv4 now and then, and a tracker that
   still dials the old one goes silent without an error anywhere. Set `DDNS_HOSTNAME` (for example
   `gps.<domain>`) in the secrets repo: `scripts/sync/ddns.py` creates the record and keeps it on the
   current address every five minutes. It is DNS only, not proxied, because Cloudflare's proxy does
   not relay raw TCP. Check with `dig +short gps.<domain>` against `curl -4 api.ipify.org`.
2. **The router.** Forward TCP 5027 to the Pi's `STATIC_IP`. Test it from outside the house (phone
   on mobile data, or another node): `nc -vz gps.<domain> 5027` must connect.
3. **First login.** Open `https://traccar.<domain>` and register: the first account is the admin.
   Then, in Settings > Server, untick **Registration**, or anyone on the tailnet can make an account.
4. **The device.** Settings > Devices > +, with the tracker's IMEI (on its label, or `getimei` by
   SMS) as the identifier.

## Pointing a Teltonika at it

The SIM needs a data plan and its PIN removed (put it in a phone first). An FMB is 2G only, so the
SIM's network must still run 2G; FMC and FMM models are 4G.

Configure it by SMS from any phone. With no SMS login set, every command starts with **two spaces**:

```
  setparam 2001:<apn>;2004:gps.<domain>;2005:5027;2006:0
```

`2001` is the APN (`2002` and `2003` its user and password, if the operator has them), `2004` the
server, `2005` the port, `2006:0` TCP. `  getgps` and `  getstatus` answer with what it sees.

Two things that make a command go unanswered, both invisible from the phone:

- **iPhones strip the leading spaces** from a text, so the device ignores the command. Set an SMS
  login and password instead (SMS \ Call Settings in the Configurator, parameters `3003` and
  `3004`, five characters each at most); the command is then `<login> <password> getstatus`, no
  spaces in front, and anything without them is dropped.
- **A SIM that has never sent a text has no SMS centre number**, so the device reads the command
  and fails to answer (`+CMS ERROR: 96` in its log). Put the SIM in a phone, set the centre number
  (on an iPhone, dial `**5005*7672*+<number>#`; the operator publishes it) and send one text.

The device log, Status > Maintenance > Log in the Configurator, shows every SMS it reads and every
reply it tries, which is faster than guessing which of the two it is.

Two things that cost a device:

- **The password.** A second-hand unit may have a Bluetooth or SMS password set (Teltonika's
  default is `5555`). Five wrong attempts lock it until Teltonika sends a master code, so ask the
  seller before guessing.
- **Vendor-locked units.** Trackers resold under a brand (Mapit is a Teltonika inside) can come
  with a password only the brand knows, and the brand can disable the unit remotely.

Teltonika's Configurator (Windows, over USB) or the Teltonika Telematics app (Bluetooth) reach the
same settings, plus the ones SMS does not.

## Alerts on Telegram

Set `TRACCAR_TELEGRAM_KEY` (a bot token from @BotFather) and `TRACCAR_TELEGRAM_CHAT_ID` in the
secrets repo, then in the panel: Settings > Notifications > +, pick the event and the **telegram**
channel, and tick the device. The useful ones on a parked vehicle:

| Event | What it catches |
| :--- | :--- |
| Ignition off + Device moving | Lifted onto a van. Needs the ignition wire connected to the tracker |
| Power cut / Alarm | Main battery disconnected; the tracker runs on its backup battery |
| Geofence exit | It left the garage, drawn as a geofence in the panel |
| Device offline | It stopped reporting: jammer, dead battery, or the house lost its IP |

Positions arrive with a map link (`NOTIFICATOR_TELEGRAM_SEND_LOCATION`). Without the two variables
the web channel still works; Telegram events just fail in the log.

## What it does not do

- It does not stop a theft. It tells you where the vehicle went, and only while the tracker has
  power, signal and a working house on the other end.
- It is not monitored from outside the house: when the Pi or the line is down, positions are lost
  (the tracker keeps a backlog and resends it, within its memory).
- No engine cut-off. A Teltonika can drive a relay from its digital output and Traccar can send
  the command, but that is wiring on the vehicle, not something configured here.

## Where things live

| | |
| :--- | :--- |
| Database (H2) | `appdata/traccar/data`, in the nightly backup with the rest of `appdata` |
| Logs | `appdata/traccar/logs`, plus `docker logs traccar` |
| Settings | Environment in `compose-core.yml` (`CONFIG_USE_ENVIRONMENT_VARIABLES`): every key in Traccar's [configuration reference](https://www.traccar.org/configuration-file/) works as an upper-case variable, `web.url` as `WEB_URL` |
