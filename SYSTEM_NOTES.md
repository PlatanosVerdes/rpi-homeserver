# System Notes — Host-Level Configuration

Everything outside Docker that must be configured on the Raspberry Pi OS itself. Run these once during initial setup or after a fresh OS install.

---

## 1. Docker Log Limits

Prevents the SD card / disk from filling up with container logs (especially Prometheus and Prowlarr).

**File:** `/etc/docker/daemon.json`

```json
{
  "log-driver": "json-file",
  "log-opts": {
    "max-size": "10m",
    "max-file": "3"
  }
}
```

```bash
sudo systemctl restart docker
```

After applying this, no individual `compose-*.yml` file needs a `logging:` block.

Container logs are also shipped to VictoriaLogs and kept there for 30 days, so this cap only
governs how far back `docker logs` reaches. See [docs/logging.md](docs/logging.md).

### The systemd journal

Debian ships `journald.conf` fully commented out, which leaves the default cap at 10% of the
filesystem: on a 117 GB SD card, room for roughly 11 GB of journal. Set it explicitly.

**File:** `/etc/systemd/journald.conf`

```
SystemMaxUse=200M
SystemMaxFileSize=50M
```

```bash
sudo systemctl restart systemd-journald
journalctl --disk-usage
```

### The repo's own log files

The cron scripts append to `apply.log`, `backup.log` and friends in the repo root, and nothing
rotates them by default: `apply.log` gains a few lines every deploy, forever. A host file, not in
git:

**File:** `/etc/logrotate.d/rpi-homeserver`

```
/home/raspi/rpi-homeserver/*.log {
    weekly
    rotate 4
    maxsize 20M
    compress
    delaycompress
    missingok
    notifempty
    copytruncate
    su raspi raspi
}
```

`copytruncate` is not optional: the cron jobs hold these files open with `>>`, so rotating by
rename would leave them writing into a deleted inode and the new file would stay empty forever.
Dry run with `sudo logrotate -d /etc/logrotate.d/rpi-homeserver`.

---

## 2. External Disk Mount

The `DATA_ROOT` variable in `.env` points to the external disk. Mount it permanently by UUID so it survives reboots.

```bash
lsblk -f                          # find UUID and fstype (e.g. ext4)
sudo mkdir -p /mnt/data
```

Add to `/etc/fstab`:
```
UUID=your-disk-uuid  /mnt/data  ext4  defaults,noatime,nofail  0  2
```

`nofail` prevents a boot hang if the disk is unplugged.

```bash
sudo mount -a                     # apply without rebooting
```

---

## 3. IP Forwarding (for Tailscale exit node)

Required for the Pi to route traffic on behalf of Tailscale devices.

```bash
echo 'net.ipv4.ip_forward = 1' | sudo tee -a /etc/sysctl.d/99-tailscale.conf
echo 'net.ipv6.conf.all.forwarding = 1' | sudo tee -a /etc/sysctl.d/99-tailscale.conf
sudo sysctl -p /etc/sysctl.d/99-tailscale.conf
```

---

## 4. node_exporter Textfile Collector

Required for `tailscale-metrics` to push custom `.prom` files.

```bash
sudo mkdir -p /var/lib/node_exporter/textfile_collector
sudo chown -R $USER:$USER /var/lib/node_exporter
```

The `node-exporter` container in `compose-mon.yml` mounts this directory. `tailscale-metrics` writes to it via the host cron.

---

## 5. Cron Jobs

```bash
crontab -e
```

Active cron entries:

```
# Auto-deploy: pull git + docker compose up every 15 min
*/15 * * * * /home/raspi/rpi-homeserver/scripts/apply.sh >> /home/raspi/rpi-homeserver/apply.log 2>&1

# Tailscale metrics: write .prom file every minute for node_exporter
* * * * * /home/raspi/rpi-homeserver/services/tailscale-metrics/tailscale-metrics >> /home/raspi/rpi-homeserver/tailscale-metrics.log 2>&1
```

> The `tailscale-metrics` binary must be compiled first: `cd services/tailscale-metrics && make build`

---

## 6. Tailscale

```bash
# Install
curl -fsSL https://tailscale.com/install.sh | sh

# Exit node only
sudo tailscale up --advertise-exit-node --accept-dns=false

# Exit node + subnet routing (exposes local LAN 192.168.1.x to Tailscale devices)
sudo tailscale up \
  --advertise-exit-node \
  --advertise-routes=192.168.1.0/24 \
  --accept-dns=false
```

`--accept-dns=false` is critical — prevents Tailscale from overwriting `/etc/resolv.conf` and breaking Pi-hole + Docker DNS.

Then in [Tailscale admin console](https://login.tailscale.com/admin):
1. Machines → your Pi → Edit route settings → enable exit node
2. DNS tab → Global Nameservers → custom → Pi's Tailscale IP → Override local DNS

→ Full guide: [docs/tailscale.md](docs/tailscale.md)
→ Reference: https://tailscale.com/docs/solutions/block-ads-all-devices-anywhere-using-raspberry-pi

---

## 7. Static IP

Reserve a static local IP for the Pi in your router's DHCP settings (bind to Pi's MAC address). Set `STATIC_IP` in `.env` to match.

---

## 8. User Permissions

All containers run with `PUID`/`PGID` from `.env`. Get your values:

```bash
id -u   # → PUID
id -g   # → PGID
```

Ensure the user owns all `appdata/` and `config/` directories:

```bash
sudo chown -R $USER:$USER ~/rpi-homeserver/appdata ~/rpi-homeserver/config
```

## qBittorrent queue limits

`MaxActiveTorrents` and `MaxActiveUploads` raised from 18/15 to **40/40** on 2026-08-05, applied
live through the WebUI API (`/api/v2/app/setPreferences`).

Why: with 36 torrents and 18 active slots, half of them sat in `queuedUP`, which is **not seeding**.
Since seeding time only accumulates while active, the 5-day share limit set on each torrent took
roughly twice as long to reach, so torrents took twice as long to retire themselves. Right after the
change the queue went from 18 waiting to 0.

The limit is 40 rather than unlimited so a much larger library cannot open hundreds of connections on
a Pi.

> Everything in this section and the next now lives in `config/qbittorrent/preferences.json` and is
> reapplied on every deploy by `scripts/sync-qbit-config.sh`. It used to be appdata-only, which is
> why these notes existed at all. **Change the values there, not in the WebUI:** the next deploy
> converges the app back to whatever is committed. What is written below is the reasoning, which the
> JSON cannot hold.

## qBittorrent global download limit

`dl_limit` set to **10 MiB/s** (10485760 B/s) on 2026-08-08. It was unlimited.

Why: that night the 02:00 `cutoff-search.sh` run found upgrades for 7 films at once and handed
qBittorrent ~226 GB. Load average hit **16** on a 4-core Pi and stayed there for 100 minutes, with 8
processes blocked on I/O. Nothing actually failed — no blackbox probe missed, latency went from 0.03s
to 0.25s — but everything was sluggish for an hour and a half.

**The line is not the constraint and never was.** Speedtest gives a steady 770 Mbps (96 MB/s, 30-day
minimum 716), so even the burst that caused this was using 40% of the fibre. What cannot keep up is
the storage: `/mnt/data` is mergerfs over two USB spinning disks, and mergerfs is FUSE, so every
written byte crosses userspace and costs CPU. That is where the 1.0-1.5 cores of kernel time came
from, alongside the seeking of three torrents writing to platters at once.

Where 10 comes from, correlating download rate against load over 14 days:

| MB/s | cores busy (of 4) | load5 median |
| ---: | ---: | ---: |
| 5-8 | 1.39 | 2.43 |
| 8-12 | 1.82 | 3.39 |
| 12-16 | 2.10 | 5.24 |
| 25-40 | 3.03 | 12.08 |

Load crosses the core count somewhere around 10 MB/s, so that is the ceiling worth holding: it keeps
the run queue under 4 while still clearing a 50 GB release in about 85 minutes. Since the fibre is
nowhere near the limiting factor, buying speed above this point costs responsiveness and saves
nothing that matters.

The knob is the rate, not the concurrency. `max_active_downloads` stays at 3: with a global cap in
force, three torrents share the same 10 MiB/s that one would have had, so lowering it changes how
scattered the writes are but not how many bytes get written.

`async_io_threads` lowered from 10 to **4** at the same time. It is how many disk requests libtorrent
keeps in flight. Ten is a sensible default for an SSD, which has no moving parts and gets faster the
deeper its queue; on platters every concurrent request is a different place on the surface, so ten
threads across three torrents leaves the head travelling instead of writing. This one is reasoning
from how the hardware works, **not** something measured on this Pi — worth a look at the load graph
after a busy night, and putting it back is a one-line edit to the JSON.

`alt_dl_limit` (also 10 MiB/s) is unrelated and deliberately left alone: it only applies while
`scheduler_enabled` is true, and it is false, so it is dead config — mentioned here only so it is not
mistaken for the limit in force.

## Prowlarr indexers

Also state that lives in `appdata/` and cannot be restored from git. A backup of every indexer,
credentials included, is written to `appdata/prowlarr/indexers-backup.json`.

Three indexers were removed on 2026-08-05 because they could not work at all: their Cardigann
definition no longer ships with Prowlarr **and** their site failed the connection test.

| Removed | Why |
| :--- | :--- |
| Elitetorrent-wf | definition dropped upstream, server unreachable |
| MoviesDVDR | definition dropped upstream, server unreachable |
| RoTorrent (API) | definition dropped upstream, API returns 404 |

A missing definition on its own is *not* a reason to delete: Frozen Layer has none either and still
works, because Prowlarr keeps running a definition it already loaded. What it does mean is that the
indexer cannot be re-added if it is ever deleted.

There is no drop-in replacement for the two Spanish trackers. Of the Spanish definitions Prowlarr
still ships, every one that carries films or series is private, so adding one needs an account first.

### Why LimeTorrents is in Sonarr but not in Radarr

Not a sync failure, and forcing a sync will not fix it. Radarr validates an indexer on save by
running an **empty** query in its own categories, the way an RSS refresh would. LimeTorrents returns
nothing for an empty movie query while returning results for a real search term, so Radarr answers
`400 No Results in configured categories` and refuses the indexer even with `forceSave=true`.
Prowlarr logs the rejection and moves on.

## Maintainerr

Also `appdata`-only state, not reproducible from git. Set up on 2026-08-05 to delete a movie 7 days
after it was watched.

The collection is called **`Pending deletion`** (`Películas` until 2026-08-19, which read as a copy
of the Plex library of the same name). The name is not cosmetic: Maintainerr creates a real Plex
collection from it, visible on the Plex home, holding whatever is queued for deletion.

- **Download client wired to qBittorrent** (`download_client_url=http://qbittorrent:8080`, user
  `admin`, `delete_data=true`). Without this, Maintainerr only removed the file from Radarr; the
  torrent kept seeding untouched. Deleting now also removes the torrent, respecting its own seed-time
  limit (Maintainerr's fallback ratio only kicks in when the client enforces no limit of its own,
  which is not our case).
- **Fixed a double grace period on the collection.** The rule means "last viewed more than N days
  ago" (an unwatched title has no `lastViewedAt`, so it never matches). The collection
  used to wait a *second* 7 days after the item entered it before deleting, doubling the real delay.
  `deleteAfterDays` is now `0`: the rule's own threshold is the only wait. That threshold is
  `customVal.value` in `rules[0].ruleJson` (seconds) — currently **1209600 = 14 days**, changed from
  the original 7 on 2026-08-05.
- **The collection now deletes through Radarr, not through Plex** (`arrAction=1`
  `UNMONITOR_DELETE_ALL`, `radarrSettingsId=1`, both in the `collection` table of
  `appdata/maintainerr/maintainerr.sqlite`). It used to be `arrAction=0` with `radarrSettingsId`
  unset, so Maintainerr had no Radarr instance to act on and fell back to deleting the item through
  Plex: the file left the disk, but the movie stayed **monitored** in Radarr. `cutoff-search.sh`
  then found it in `wanted/missing` at 05:00 and grabbed it again. Cars 3 rode that loop from
  2026-08-11 to 2026-08-19 — deleted around noon, re-downloaded at 05:00, a 5-20 GB Ultra-HD
  release every night, and it left one seeding copy per round in `/mnt/data/downloads`. Unmonitoring
  as part of the delete is what takes it off the missing list for good.
- **`cleanupLeftoverFolders` is on.** Deleting through Radarr removes the file and leaves the movie
  folder behind, so `films/` was collecting empty shells.
- **The download client cleanup only reaches downloads Radarr still knows about.** Maintainerr asks
  Radarr for the download ids of the movie before deleting, and a torrent that finished days ago is
  no longer in Radarr's queue, so nothing gets removed and the copy keeps seeding. Cars 3 left four
  of them, 27 GB, over its re-download loop. Check `/mnt/data/downloads` by hand after a cleanup of
  something old; on a private tracker, mind its H&R rules before deleting.
- media server is Plex (`media_server_type=plex`); it is not wired to Jellyfin/Emby.
