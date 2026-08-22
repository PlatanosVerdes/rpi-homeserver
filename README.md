# RPi HomeServer

A modular, Docker-based home server for Raspberry Pi. Uses Docker Compose's `include` feature to keep each concern in a separate, maintainable file.

> **Companion repo:** Personal and custom services live in [rpi-services](https://github.com/PlatanosVerdes/rpi-services) — bots, integrations, and anything too personal or domain-specific to be generic. Both repos share the same Docker network and Caddy instance.

---

## Infrastructure Overview

| Module | Purpose | Key Services |
| :--- | :--- | :--- |
| **Core** | Entry point & Networking | Caddy, Homepage, Pi-hole, Speedtest-tracker |
| **Media** | Streaming & Live TV | Plex, Jellyfin, Overseerr, Bazarr, Maintainerr, Acestream |
| **Arrs** | Automation & Downloads | Radarr, Sonarr, Prowlarr, qBittorrent, FlareSolverr, Unpackerr |
| **Monitoring** | System Health | Prometheus, Grafana, Pushgateway, node-exporter, cAdvisor |

---

## How a change reaches the Pi

Nothing is deployed by hand. A push is the only trigger, and the repo is the source of truth.

```
 cal-bridge      one-pace-downloader      air-tag      PolFerrerAcademyOffers
     │                   │                   │                  │
     └──── push to main ─┴───────────────────┴──────────────────┘
                              │
                    auto-tag.yml → tag vYYYY.MM.DD
                              │
                              ▼
              ┌───────────────────────────────────────┐
              │ rpi-services                          │
              │ bump-app-versions.yml  (every 30 min) │
              │ versions.env → commit + push          │
              └───────────────────────────────────────┘
                              │
   your push ─────────────────┤
                              ▼
                    GitHub webhook (push event)
                              │   HMAC-SHA256 signature
                              ▼
           Cloudflare edge   deploy.<domain>
                              │   outbound tunnel, no ports opened
                              ▼
                    cloudflared ──► 127.0.0.1:9000
                              │   deploy-webhook.py verifies the signature
                              ▼
                    ╔═════════════════════╗
                    ║  scripts/deploy/apply.sh  ║ ◄──── cron every 30 min
                    ╚═════════════════════╝        (self-heal fallback)
```

The cron is not redundant: it restarts containers that died on their own and picks up pushes that
landed while the Pi or the tunnel was down.

### What a deploy actually does

```
scripts/deploy/apply.sh
   │
   ├─ flock ................... another deploy already running? exit
   ├─ git pull rpi-homeserver
   ├─ render alerting ......... template (git) + secrets (.env)
   │                            └─► appdata/grafana-alerting/
   ├─ docker compose up -d --build
   ├─ git pull rpi-services  ──► docker compose up -d --build
   ├─ scripts/deploy/install-crontab.sh ...... homeserver fragment + services fragment
   │                            └─► host crontab (and undoes any manual drift)
   └─ metrics ──► Pushgateway
                        │
                        ▼
                   Prometheus ──► Grafana ──► alert rules ──► Telegram
```

Details: [docs/deploy-webhook.md](docs/deploy-webhook.md) for the webhook and tunnel,
[docs/alerting.md](docs/alerting.md) for the alert rules and the dead man's switch.

---

## Media & Download Policy

The library is deliberately tuned for how it is actually watched, not for maximum
file fidelity. This documents the rules and the reasoning, so the choices below are
not mistaken for misconfiguration.

**Playback targets (use case):**
- **Samsung QLED TV (Tizen) on the LAN** — the primary screen, watched in high resolution.
- **iPad** — secondary, on the go.

Plex runs on the Pi, which is weak at video transcoding, so files should
**direct-play** (little or no transcoding) on these devices.

**What the TV can and cannot play** (verified from Plex logs):
- **Direct-plays:** H.264, HEVC (H.265), 4K, HDR10, EAC3/AC3 audio.
- **Fails** (the TV cannot decode it and the Pi cannot transcode it in time, "Not enough CPU"): **AV1** and **VC-1**.
- **DTS / TrueHD:** trigger an audio-only transcode, which the Pi handles fine.

**Download rules (Radarr / Sonarr custom formats + quality profiles):**
- **Reject AV1** (unplayable here).
- **Prefer x264/H.264 1080p encodes:** excellent quality, small size, universal playback.
- **Quality priority: Bluray-1080p is preferred over Remux-1080p**, with Remux kept as a
  fallback so a title is never left un-downloaded. This is a deliberate inversion of the
  tools' default (which prefers Remux for maximum fidelity): a Remux is a lossless 30-80 GB
  copy of the disc, but that extra quality is imperceptible on a 55-inch TV or an iPad and
  only wastes disk, whereas a Bluray-1080p encode is roughly 99% of the quality at 5-15 GB.
- **Penalize** Remux and heavy lossless audio (DTS-HD): quality the devices cannot benefit from.
- **Reject RAR packs outright** (`RAR pack (reject)`, **-20000**). A packed release is seeded as
  its `.rNN` archives while Plex plays the `.mkv` unpackerr extracts, so the film sits on disk
  twice until the tracker is paid, and no hardlink can ever fix that: the extracted file is new
  bytes. There is nothing in a release title that says "this is packed", so the format matches the
  groups that actually did it here (GAZPROM, CEBRAY, GAZER) and the list grows as more turn up.

  It used to be -300, a nudge rather than a veto, and the nudge lost. Measured on 2026-08-21,
  **Project Hail Mary was occupying 197 GB in 182 files with no shared inode**: two packed
  releases of the same film, 81.7 GB and 57.8 GB of archives, plus the 57.5 GB `.mkv` extracted
  from one of them. That is 7% of the data disk on one title.

  The score is -20000 rather than -6000 for a reason worth knowing: `minFormatScore` is -6000 and a
  release is only rejected when its **total** falls below it, so positives offset the penalty. The
  release that caused the 197 GB was Spanish-audio (+3000 or more), which a -6000 penalty would
  have let through at -3000. -20000 cannot be offset by anything this profile awards.

  **The trade-off, stated plainly:** a film available only as a packed release will now not be
  downloaded at all. Project Hail Mary is exactly that case, and this repo is choosing the empty
  shelf over 197 GB.
- **Language preference:** Castilian Spanish > VOSE (original audio with Spanish subtitles) > English > Latin-American Spanish (avoided).
- **A film is considered finished at `cutoffFormatScore` 2200**, which is Spanish audio plus x264.
  This is the setting that decides when Radarr stops hunting for a better release, and it used to
  be **10000, a score nothing can reach**: the positive formats here add up to at most +6700 and
  several of them are mutually exclusive, so a realistic good release scores about +3200. The
  consequence was that no film was ever "done" and every one of them stayed in permanent upgrade
  search. Each upgrade then replaces the library file and leaves the previous release seeding its
  tracker debt, so the churn was paid for in disk.

  It is a threshold to reach, not a penalty, which is what makes it easy to confuse with
  `minFormatScore`: that one is a floor (reject below it), this one is a target (stop when reached).

  The remaining churn is deliberate: a film that exists only with English audio scores 700 and keeps
  being searched, because Castilian audio is worth +2000 here and is worth waiting for. Dropping
  this to 700 would stop that too, at the cost of never upgrading to a Spanish release.
- **Rule of thumb for smooth playback:** 1080p, x264/H.264, AAC or EAC3/AC3, text subtitles (SRT / mov_text).

**Supporting automation:**
- **Bazarr** auto-downloads Spanish subtitles for the whole library.
- **Maintainerr** auto-cleans watched movies (movies library only) to keep the data pool from filling
  up: two days after you watch one it unmonitors it in Radarr and deletes the library copy.
- **scripts/trackers/seed-cleanup.py** finishes that job on the torrent side, hourly. Radarr imports by hardlink, so
  every download has two names: the one qBittorrent seeds and the one Plex plays. Maintainerr removes
  the second, and this removes the first once the tracker is paid:

  | State | Tracker | What happens |
  | :--- | :--- | :--- |
  | library shares the file | private | nothing, it keeps seeding: it costs no extra disk |
  | library shares the file | public | torrent and its download-side name removed as soon as the library has it |
  | library has its own copy | goal met | download copy removed, film untouched, duplicate reclaimed |
  | library has its own copy | goal pending | keeps seeding until the debt is paid, then as above |
  | film gone from the library | public | torrent and data removed on the next pass |
  | film gone from the library | private, owes its hit & run | keeps seeding, tagged `waiting-seed`, rechecked hourly |
  | film gone from the library | private, hit & run paid, **still uploading** | keeps seeding: it is producing the ratio the account needs |
  | film gone from the library | private, hit & run paid and **gone quiet** | torrent and data removed on the next pass |

  Whether the library still holds a film is asked of the hard link count first and of the *arr second.
  The second question exists because of RAR releases: a hardlink needs the same bytes at both ends,
  and the `.mkv` unpackerr extracts out of 96 `.rNN` archives is a new file, so it shares nothing with
  what qBittorrent seeds. That is also why such an import is dropped as soon as its tracker is paid:
  the film sits on disk twice, and one of the two copies is only there to honour the tracker.

  Seeding a public torrent forever costs no disk, which is why it used to be kept, but it buys
  nothing either: there is no account and no ratio requirement on a public tracker, so the only thing
  it produces is this address sitting in a public swarm for weeks. `drop_when_imported` turns that
  off: the download-side name goes as soon as the library has the file, and since the decision is
  taken on a link count above one, the library copy is untouched and nothing is freed but the
  seeding. Private trackers are unaffected, which is where seeding is the currency.

  **`keep` is the manual override**: tag a torrent `keep` in qBittorrent and this never touches it,
  whatever the goals say. No deploy, no config edit, effective on the next pass. `keep-bonus` is the
  same brake applied automatically by `scripts/trackers/control.py`, for a tracker that pays for holding data.
  It is implemented and deliberately **not** configured: measured on DigitalCore, it bought 18% of
  leech bonus for 179 GB of disk on an account already at ratio 4.21, while the free 9% that comes
  from library hardlinks costs nothing. See [docs/private-trackers.md](docs/private-trackers.md).

  **What decides is the upload rate, not the goal.** Measured on 2026-08-22: torrents that had been
  seeding 40 to 157 hours had uploaded exactly 0.00 GB, while ones a few hours old were doing 0.13 to
  0.82 GB an hour. Upload only happens while somebody still wants the file, so a rule of "remove once
  ratio 1.2 is reached" was deleting the best earner in the client (0.82 GB/h, due in 0.8 days) and
  keeping the worst (0.13 GB/h, due in 12.7). Now each tracker's own hit & run rule decides when a
  torrent *may* go and its upload over the last day decides whether it *should*: still paying means it
  stays, past the goal or not, and quiet means it goes as soon as nothing is owed, freeing the disk
  for a release somebody actually wants. The floor is `min_upload_gb_per_day` per tracker, and a
  torrent with less than 12 hours of history is never judged on it.

  Goals live in `config/qbittorrent/seed-rules.json` (240 h seeding or ratio 1.0 for private, nothing
  for public). Whether a tracker is private comes from qBittorrent, so there is no list to maintain.
  qBittorrent's own share limits stay **off on purpose**: they cannot see the library, so they would
  delete torrents for films you have not watched yet. One deleter, one policy. `DRY_RUN=1` prints
  what it would remove and touches nothing; the "Seed cleanup" row of the Disk usage dashboard shows
  the queue.
- **scripts/trackers/stats.py + scripts/trackers/control.py** keep the account on the right side of the tracker's
  ratio rule without anyone watching it. Every half hour the first logs into TorrentLeech and reads
  what the site says (ratio, uploaded, downloaded, points, any active warning); five minutes later
  the second acts on one derived number:

  ```
  buffer   = uploaded - min_ratio x downloaded
  headroom = buffer / min_ratio      <- GB of non-freeleech downloads that still fit
  ```

  | Headroom | Prowlarr | Radarr | autobrr |
  | :--- | :--- | :--- | :--- |
  | under 25 GB | freeleech results only | `requiredFlags = [1]` | 3 grabs a day |
  | 25 to 100 GB | freeleech results only | `requiredFlags = [1]` | 2 grabs a day |
  | over 100 GB | all results | `requiredFlags = []` | 1 grab a day |

  **The filter lives in Prowlarr because that is the only place that covers everything.**
  `requiredFlags` exists on Radarr's Torznab indexer and not on Sonarr's, and Prowlarr full-syncs
  its indexers to both, so anything switched in an *arr is overwritten on the next sync. The
  tracker's own freeleech facet, applied in Prowlarr, filters every app that searches through it,
  Sonarr included, and nobody loses an indexer. With 18 GB of headroom one 20 GB season pack that is
  not freeleech takes the ratio from 0.52 to 0.397, and 0.4 is the line TorrentLeech disables
  accounts under. The grabber also pauses below a free-space floor whatever the ratio says, because
  a grab cannot be deleted until its hit & run window closes. Thresholds live in
  `config/trackers/rules.json`; `DRY_RUN=1` prints what it would change.
- **Private tracker rules, per site, and what got an account disabled once**:
  [docs/private-trackers.md](docs/private-trackers.md).
- **Why the ratio on private trackers is near zero, and the three ways out** (gluetun on a VPN that
  forwards a port, a seedbox, or a forward at home), with the measurements behind it:
  [docs/seeding-and-ratio.md](docs/seeding-and-ratio.md).
- **Unpackerr** unrars scene releases. Radarr and Sonarr cannot read a `.rar` set, so those
  releases sit in the queue as `importPending` forever and the disk fills up with parts that
  never become a movie. The original archives are left alone so the torrent keeps seeding;
  only the extracted copy is removed once the *arr has imported it.

### Deletion policy

The rule of record. Everything above is its implementation, and any doubt is settled here:

1. **Nothing is deleted before it has been watched.** Watched is Plex's verdict, at 95% of the
   runtime, so a film abandoned in the credits does not count.
2. **A watched film is deleted two days later**, library copy gone and unmonitored in Radarr. The
   delay leaves room to rewatch it, for someone else in the house to see it, or to notice that Plex
   marked something watched by accident. The delete is not reversible.
3. **The torrent is a separate debt and the tracker decides when it is paid.** A public tracker asks
   for nothing, so its torrent goes as soon as the film does. A private one keeps seeding until it
   has given back 240 hours or ratio 1.0, whichever arrives first, because leaving early is how an
   account gets banned. Disk pressure is never a reason to pay less.

Two consequences that read like bugs and are not:

- **A film that is still seeding is normally still in Plex, and costs nothing extra for it.** Radarr
  imports by hardlink, so what qBittorrent seeds and what Plex plays are the same bytes under two
  names. Measured on 2026-08-20: 55 of 64 torrents were exactly that. Of the other nine, one was
  still downloading, four were films the policy had already deleted and whose torrent was only
  paying its seed debt (gone from Plex on purpose), three were RAR releases whose extracted `.mkv`
  shares nothing with the archives, and one was an orphan.
- **A film deleted from the *arr while its download is in flight becomes an orphan.** The download
  finishes with nothing to import into, so it stays in the queue as `importBlocked` with
  `Movie title mismatch, automatic import is not possible`, and Radarr re-sends its
  `onManualInteractionRequired` Telegram alert **on every restart**: one deletion on 2026-08-09 was
  still raising alerts eleven days later, after a reboot. That file never reaches Plex. Its torrent
  still owes the tracker, so it stays until the goal is met. Deleting the queue entry does not stick:
  the *arr rebuilds its queue from the download client, so the item is back on the next refresh
  (measured, not assumed). What clears it is **taking the torrent out of the *arr's category** in
  qBittorrent, which is safe while `auto_tmm` is off (it is: nothing moves on disk), stops the *arr
  from ever seeing it again, and leaves `scripts/trackers/seed-cleanup.py` finishing the job, because that one asks
  about hard links and trackers and never about categories. Both this and data a removed torrent left
  behind are exported by `scripts/metrics/media.py` as `arr_orphan_*` every five minutes, and alerted on
  ("Download nothing can import", "Data in downloads that nothing owns"), because the *arr's own
  notification only fires when it restarts and is easy to lose among the reboot alerts.

### Indexers / trackers

Current: public (**1337x, YTS, The Pirate Bay, LimeTorrents, Nyaa.si** for anime/JP cinema), Spanish
(**Elitetorrent, MoviesDVDR, Frozen Layer**), plus private (**BTSchool**, Chinese NexusPHP — search works
via FlareSolverr/byparr, but the binary `.torrent` download fails through Cloudflare's cookie-replay, so
grabs from it are manual; it also has H&R + a newbie ratio requirement).

**Next tier — pending to evaluate** (more *arr-friendly, no Cloudflare-download problem than BTSchool):
- General / joinable now: **TorrentLeech**, **IPTorrents**, **FileList**.
- Cinephile / top-tier (invite-only): **PassThePopcorn** (movies), **HDBits** (quality), **AnimeBytes**
  (anime & Japanese cinema), **BroadcastTheNet** (TV).
- Modern UNIT3D (clean API, some open signups): **Blutopia**, **Aither**, **ReelFliX**, **LST**, **Fearnopeer**.
- Watch `r/OpenSignups` for when good trackers open registration.

---

## Fresh Install (from scratch)

### 1. Prerequisites

```bash
# Install Docker
curl -fsSL https://get.docker.com | sh
sudo usermod -aG docker $USER
# Log out and back in

# Install Tailscale
curl -fsSL https://tailscale.com/install.sh | sh
```

### 2. Clone the repo

```bash
git clone <your-repo-url> ~/rpi-homeserver
cd ~/rpi-homeserver
```

### 3. Configure environment

```bash
cp .env.example .env
# Edit .env — fill in STATIC_IP, TAILSCALE_IP, PUID/PGID, DATA_ROOT, API keys, etc.
nano .env
```

Run `id -u` and `id -g` to get your PUID and PGID values.

### 4. Mount external storage

```bash
lsblk -f                          # find UUID and fstype of your disk
sudo mkdir -p /mnt/data
# Add to /etc/fstab:
UUID=your-uuid  /mnt/data  ext4  defaults,noatime,nofail  0  2
sudo mount -a
```

Set `DATA_ROOT=/mnt/data` (and `DATA_DB_ROOT=/mnt/data/db`) in `.env`.

### 5. Configure Docker log limits

Prevents the SD card from filling up. Create/edit `/etc/docker/daemon.json`:

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

### 6. Start services

```bash
docker compose up -d
```

### 7. Set up Tailscale

```bash
sudo tailscale up --advertise-exit-node --accept-dns=false
```

Then approve the exit node in the Tailscale admin console and set Pi-hole as the DNS server.
→ See [docs/tailscale.md](docs/tailscale.md)

### 8. Configure auto-deployment

Install the crontab from the repo (never with `crontab -e`, the deploy overwrites the live one):

```bash
bash ~/rpi-homeserver/scripts/deploy/install-crontab.sh
```

That merges this repo's `scripts/crontab` with a companion repo's fragment if there is one, and
`scripts/deploy/apply.sh` re-runs it on every deploy, so committed cron changes apply themselves.

For push-triggered deploys instead of waiting for the 30-minute cron, see
[docs/deploy-webhook.md](docs/deploy-webhook.md).

### 9. Configure Pi-hole DNS records

For each HTTPS subdomain, add a DNS record in Pi-hole → Local DNS → DNS Records:

| Domain | IP |
| :--- | :--- |
| `raspi.platanosverdes.com` | `<TAILSCALE_IP>` |
| `jellyfin.platanosverdes.com` | `<TAILSCALE_IP>` |
| `grafana.platanosverdes.com` | `<TAILSCALE_IP>` |
| *(all other subdomains)* | `<TAILSCALE_IP>` |

### 10. Add entries to `/etc/hosts` on client devices

For HTTP short names to work on your laptop/desktop:

```
# LAN access
<STATIC_IP>    raspi homepage jellyfin overseerr plex grafana prometheus push prowlarr radarr sonarr flare torrent speedtest pihole

# Tailscale access (add same names pointing to Tailscale IP if not using Pi-hole DNS)
<TAILSCALE_IP> raspi homepage jellyfin overseerr plex grafana prometheus push prowlarr radarr sonarr flare torrent speedtest pihole
```

### 11. Check what still needs a human

```bash
bash scripts/deploy/recovery-status.sh
```

The deploy converges everything derivable from `.env`: Radarr and Sonarr's custom formats and
quality profiles, Pi-hole's DNS records, qBittorrent's limits, the Overseerr and Bazarr links. A
few things it cannot, because each is a one-time interactive step with an outside provider — a Plex
claim token, a setup wizard, an Apple 2FA code.

This prints which of those are still outstanding, so they get found now rather than the evening
something does not work.

---

## Profiles — selecting which services to run

Every service belongs to one or more profiles. Set `COMPOSE_PROFILES` in `.env` to control what starts — no need to touch the compose files.

| Profile | Services |
| :--- | :--- |
| `essential` | Caddy, Homepage, Pi-hole, Speedtest-tracker |
| `moni` | Prometheus, Grafana, Pushgateway, node-exporter, cAdvisor, Pihole-exporter, Speedtest-tracker |
| `acestream` | Aceserve, Acestream-updater, Jellyfin + Grafana/Prometheus/Pushgateway |
| `media` | Plex, Overseerr, Prowlarr, Radarr, Sonarr, qBittorrent, FlareSolverr, Unpackerr |
| `bot` | Pol Academy Offers Bot |
| `all` | Everything |

```bash
# .env — main Pi (everything)
COMPOSE_PROFILES=all

# .env — secondary Pi (only essentials + monitoring)
COMPOSE_PROFILES=essential,moni

# .env — secondary Pi (essentials + acestream only)
COMPOSE_PROFILES=essential,acestream
```

---

## Project Structure

```
config/      Static config files (Caddyfile, Prometheus, Grafana dashboards, Homepage)
services/    Custom services source, one Docker image each
scripts/     Operational scripts, grouped by what they do:
  deploy/      makes the host match the repos (apply.sh and the installers)
  sync/        pushes config that lives only in an app's appdata, every deploy
  metrics/     numbers no exporter provides
  trackers/    the private-tracker economy: measure, decide, reclaim
  ops/         everything else on a schedule (backup, heartbeat, searches)
appdata/     Persistent container data (databases, app state) — not in git
docs/        Setup guides, starting with architecture.md
```

---

## Module Guides

### Networking (Tailscale + Pi-hole)
Tailscale provides secure remote access. Pi-hole handles DNS and ad-blocking for all Tailscale devices.

**Access pattern:**
- **At home (LAN):** use the Pi's local IP (`STATIC_IP`) or HTTP short names (`http://jellyfin`, `http://grafana`…)
- **Outside (remote):** activate Tailscale on your device → use the Pi's Tailscale IP (`TAILSCALE_IP`) or HTTPS subdomains (`https://jellyfin.platanosverdes.com`)

**Start Tailscale on the Pi:**

Only exit node (access Pi services remotely, no other LAN devices exposed):
```bash
sudo tailscale up --advertise-exit-node --accept-dns=false
```

Exit node + subnet routing (also reach other LAN devices like a NAS or printer remotely):
```bash
sudo tailscale up \
  --advertise-exit-node \
  --advertise-routes=192.168.1.0/24 \
  --accept-dns=false
```

- `--advertise-exit-node` — lets Tailscale devices route internet traffic through the Pi
- `--advertise-routes=192.168.1.0/24` — exposes the full local LAN (`192.168.1.x`) to Tailscale devices
- `--accept-dns=false` — critical: prevents Tailscale from overwriting `/etc/resolv.conf` and breaking Pi-hole + Docker DNS

Then in the [Tailscale admin console](https://login.tailscale.com/admin):
1. **Machines → your Pi → Edit route settings** — enable "Use as exit node" (and approve the subnet if you used `--advertise-routes`)
2. **DNS tab** → Global Nameservers → Add custom nameserver → enter your `TAILSCALE_IP` → enable "Override local DNS"

Step 2 makes Pi-hole the DNS server for every device in your tailnet, so `*.platanosverdes.com` resolves correctly from anywhere.

Reference: [Tailscale — Block ads on all devices using Raspberry Pi](https://tailscale.com/docs/solutions/block-ads-all-devices-anywhere-using-raspberry-pi)
→ See [docs/tailscale.md](docs/tailscale.md) for the full setup guide
→ See [docs/plex-remote-access.md](docs/plex-remote-access.md) for why Plex needs subnet routes on
top of the exit node, and why it must never be put behind the reverse proxy

### Reverse Proxy (Caddy)
Caddy provides short HTTP names on LAN (`http://jellyfin`, `http://raspi`, etc.) and HTTPS via Cloudflare DNS challenge for remote access (`https://*.platanosverdes.com`).

Caddy uses a custom Docker image that includes the Cloudflare DNS plugin (see `config/caddy/Dockerfile`). Certificates are issued automatically on first request and renewed by Caddy.

**Extending Caddy from a companion repo (e.g. rpi-services):**

Caddy auto-imports `*.caddy` files from two directories: `config/caddy/services/` (this repo) and `/etc/caddy/ext-services/` (mounted via override). To add routes from `rpi-services` without touching this repo, create a `docker-compose.override.yml` (gitignored) on the Pi:

```yaml
# ~/rpi-homeserver/docker-compose.override.yml  (not committed to git)
services:
  caddy:
    volumes:
      - /home/raspi/rpi-services/config/caddy:/etc/caddy/ext-services:ro
```

Then in `rpi-services`, add a file like `config/caddy/myservice.caddy`:

```caddy
https://myservice.yourdomain.com {
    import cf_tls
    reverse_proxy myservice:8080
}
```

Caddy picks it up on next restart — no changes needed in this repo.

→ See [docs/architecture.md](docs/architecture.md) for what talks to what, and what may overwrite which app's config
→ See [docs/add-service.md](docs/add-service.md) to add a new service with HTTPS

### Media Automation (The *arrs Suite)
- **Prowlarr → FlareSolverr:** Settings → Indexers → Add Proxy → `http://flaresolverr:8191`
- **Prowlarr → Radarr/Sonarr:** Settings → Apps → add each with their API keys
- **Download client:** In Radarr/Sonarr → Settings → Download Clients → qBittorrent → host `qbittorrent`, port `8080`

### Acestream Live TV
The `acestream-updater` (Go service in `services/acestream-updater/`) fetches IPFS channel lists, deduplicates them by acestream hash, writes a `.m3u` for Jellyfin, and runs concurrent health checks to verify each channel is actually serving bytes. It refreshes Jellyfin automatically when the playlist changes.

Jellyfin setup: Dashboard → Live TV → Add Tuner (M3U) → path `/data/channels_ace.m3u`

**After changing Go source code** (rebuild required):
```bash
docker compose -f compose-media.yml up -d --build acestream-updater
```

**Trigger a run immediately** (container restart runs before the first sleep):
```bash
docker compose -f compose-media.yml restart acestream-updater
```

**View live logs:**
```bash
docker logs -f acestream-updater
```

### Watch-next (auto-fetch on watch)
`services/watch-next/` (Go, Dockerized) monitors and searches the next episode(s) in Sonarr when
Tautulli (Plex) or Jellyfin reports one watched, so a season fills in progressively instead of all
at once. See [docs/watch-next.md](docs/watch-next.md) for the full setup (Tautulli/Jellyfin
webhook configuration).

### Tailscale Metrics
`services/tailscale-metrics/` is a Dockerized Go exporter with no published port. Prometheus
scrapes it at `tailscale-metrics:9736/metrics` over `media-network`.

It mounts `/var/run/tailscale/tailscaled.sock` read-only to read peer status from tailscaled's
LocalAPI, and optionally calls the Tailscale control API with `TAILSCALE_API_KEY` to mark which
peers are approved exit nodes. Nothing to build by hand: compose builds it like every other
service in `services/`.

### Auto-Deployment (`scripts/deploy/apply.sh`)
Pulls latest git changes every 15 minutes and rebuilds only when something changed.

```bash
# Cron entry (set up during install)
*/15 * * * * /home/raspi/rpi-homeserver/scripts/deploy/apply.sh >> /home/raspi/rpi-homeserver/apply.log 2>&1
```

Metrics are pushed to Pushgateway and visible in the **Deploy Monitor** dashboard in Grafana.

### Monitoring (Prometheus & Grafana)
- **Grafana:** `http://<IP>:3000` — default credentials: `admin / admin`
- **Auto-provisioned dashboards** (in `config/grafana/dashboards_json/`):
  - Container Health — host stats (CPU temp, load, RAM, disk), per-container CPU/mem/network, and a Storage section (usage-by-folder pie, folder filter, largest-files table)
  - Service probes — which service the Home dashboard's "Services down" number is about: per-service status, availability history and probe times. Every stat on Home is clickable and lands on the dashboard that answers it.
  - Retention policy — the film lifecycle end to end: which films are queued for deletion right now and since when, how many are watched and in the grace period, how many are gone from Plex but still paying a private tracker, how far along its goal each one is and the hours it still owes, what each tracker asks for, and the movements both halves have made, from the cleanup's own log and Maintainerr's
  - Deploy Monitor — deploy runs, changes, errors, per-repo status
  - Backup Monitor — last backup status/age/size and appdata growth over time
  - Acestream Monitor — channel sync, changes, errors, source URL status, per-channel health
  - One Pace Downloader / One Pace Arc Status — episode/arc KPIs and per-arc breakdown
- **Import community dashboards** (Grafana → Dashboards → Import):
  - `1860` — Node Exporter Full (CPU, RAM, disk, network)
  - `193` — cAdvisor (per-container resource usage)
- **No data on a dashboard?** Trigger a run manually:
  ```bash
  docker compose -f compose-media.yml restart acestream-updater
  bash /home/raspi/rpi-homeserver/scripts/deploy/apply.sh
  ```

### Logs (VictoriaLogs)
Prometheus stores numbers, not text. `vector` forwards what every container prints into
`victorialogs`, which keeps 30 days of it on the data disk and answers queries from Grafana
(Explore → VictoriaLogs) or its own UI at `https://logs.platanosverdes.com/select/vmui/`.
Full guide, including what is deliberately not collected: [docs/logging.md](docs/logging.md).

### Acestream in VLC (Farnsworth)
Browsers cannot play the MPEG-TS these channels arrive as, and the Pi cannot re-encode 20 Mbps of
1080p in real time (measured at 0.32x). Farnsworth lists the channels and hands one to VLC, which
plays them natively. Full guide: [docs/farnsworth.md](docs/farnsworth.md).

### Backups & Recovery
`scripts/ops/backup.sh` (daily cron at 04:00) snapshots `appdata/` to a compressed, rotated
archive and pushes health metrics to the **Backup Monitor** Grafana dashboard. Full guide:
[docs/backups.md](docs/backups.md).

- **In git (survives a disk loss):** all config in this repo, plus point-in-time exports of
  the Radarr/Sonarr custom formats + quality profiles under
  [config/arr-exports/](config/arr-exports/) — the scoring rules that are painful to rebuild.
- **In the backup archive:** the full app state under `appdata/` (databases, indexers, Plex
  library, etc.).
- **Offsite:** local archives live on the data disk, so a disk failure would lose them too.
  Set `BACKUP_RCLONE_REMOTE` (and install rclone) for an automatic offsite copy.

---

## Host-Level Configuration

See [SYSTEM_NOTES.md](SYSTEM_NOTES.md) for all OS-level settings (Docker log limits, cron jobs, sysctl, etc.).