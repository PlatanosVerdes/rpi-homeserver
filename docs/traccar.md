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

Teltonika's Configurator (Windows only, over USB or Bluetooth) reaches the same settings, plus the
ones SMS does not. There is no phone app that configures an FMB.

**Set the device's Model in the panel** (for example `FMB920`). Traccar decodes the FMB's event IOs
(towing, unplug, jamming, crash, auto geofence) as alarms only when the model matches, and reads it
when the tracker connects. Without it those events arrive as plain attributes and nothing alerts.

## Alerts on the phone through Home Assistant

Home Assistant already knows where the phone is and can send critical push notifications, which
ring through silent mode. The **Traccar Server** integration brings each tracker in as a
`device_tracker` and fires its events as `traccar_<event>` (for example `traccar_alarm`,
`traccar_device_moving`), which automations turn into notifications.

1. In the Traccar panel, add a user for Home Assistant: read-only, device read-only, commands
   limited, and link it to the devices. Log in as that user and issue a token with a long
   expiration (`POST /api/session/token` with `expiration`; without one it lasts seven days).
   Revoke it with `POST /api/session/token/revoke`.
2. In Home Assistant: Settings > Devices & services > Add > Traccar Server, host `127.0.0.1`, port
   `8082`, no SSL, that token. HA is on the host network and Traccar's panel is published on the
   Pi's loopback for it. In the integration's options, pick the events to fire (`alarm`,
   `deviceMoving`, `deviceOffline`).
3. Automations, in the HA UI: an `alarm` event becomes a critical notification with a map link; a
   `deviceMoving` event becomes one only when the phone is far from the vehicle (ask the phone for
   a fresh location first, it may be stale); `deviceOffline` a normal one.

"Moves while the phone is far" replaces a fixed garage geofence, which fires on every ride.

**Changing the tracker from Home Assistant.** Save the commands in Traccar (Settings > Saved
commands, type *Custom*, for example `setparam 11500:3;7036:1`) and link them to the Home Assistant
user: with *limit commands* it may send those and nothing else. `rest_command.traccar_saved_command`
(in `configuration.yaml`) sends one by id, with the user's token in HA's `secrets.yaml` as
`traccar_api_auth: "Bearer <token>"`. Traccar queues the command while the tracker sleeps and
delivers it when it next connects.

## Settings that work on a parked vehicle

Tested on an FMB920 powered from a vehicle battery, with alerts that matter for theft and as little
battery drain as possible. Parameter ids are Teltonika's (`getparam <id>` / `setparam <id>:<value>`
by SMS or as a Traccar custom command).

| Setting | Value | Why |
| :--- | :--- | :--- |
| Ignition source (101) | `4`, power voltage only | Ignition is ON between Low (105) `13200` and High (104) `30000` mV: the alternator lifts the battery above 13.2 V. With the accelerometer also selected, any movement counts as ignition and the towing alarm never fires |
| Sleep mode (102) | `4`, Ultra sleep, timeout (103) 5 min | Wakes on movement; draws the least. Periodic wake-up (60110) `720` min gives a sign of life twice a day |
| Network ping (1003) | `0` | `1` sends a keep-alive byte every second: battery, data, and a log line per second in Traccar |
| Towing (11600) | High, activation 1 min, 0.4 g, 5° | Movement with ignition off |
| Unplug (11500) | Panic, SMS to predefined number 1 (7036 = 1) | Panic also sends an SMS, which arrives even when the house is offline |
| Jamming (11300) | High | No SMS: a jammer blocks it too |
| Crash (11400) | High, 4500 mG, 50 ms (11402, 11401) | 2400 mG / 20 ms fired on the tug of unplugging the jack. Published motorcycle work uses 2.5-5 g, and real systems add a gyro |
| Auto geofence (20000) | High, radius 150 m (20004) | GNSS drifts tens of metres in a garage |

The Unplug SMS reports the state, not only the cut: `Val:1` is power lost, `Val:0` power back.

**Pausing for tests.** In Home Assistant a template switch ("vigilance") is the inverse of an
`input_boolean` (the pause). Pausing turns the alert automations off and sends a saved command that
drops the tracker's SMS (`setparam 11500:2;7036:0`); resuming, by hand or after three hours, turns
them on and restores it (`setparam 11500:3;7036:1`). The alerts carry no pause condition of their own,
so their switches show what is really armed.

**Low battery.** A numeric-state automation on the external voltage: between 5 and 12.2 V for 30
minutes with ignition off. Below 5 V is a power cut, already an alarm; the 30 minutes skip the dip of
cranking. A bench supply at 12.0 V trips it.

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
