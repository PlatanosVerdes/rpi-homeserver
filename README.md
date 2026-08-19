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
                    ║  apply.sh  ║ ◄──── cron every 30 min
                    ╚═════════════════════╝        (self-heal fallback)
```

The cron is not redundant: it restarts containers that died on their own and picks up pushes that
landed while the Pi or the tunnel was down.

### What a deploy actually does

```
apply.sh
   │
   ├─ flock ................... another deploy already running? exit
   ├─ git pull rpi-homeserver
   ├─ render alerting ......... template (git) + secrets (.env)
   │                            └─► appdata/grafana-alerting/
   ├─ docker compose up -d --build
   ├─ git pull rpi-services  ──► docker compose up -d --build
   ├─ install-crontab.sh ...... homeserver fragment + services fragment
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
- **Language preference:** Castilian Spanish > VOSE (original audio with Spanish subtitles) > English > Latin-American Spanish (avoided).
- **Rule of thumb for smooth playback:** 1080p, x264/H.264, AAC or EAC3/AC3, text subtitles (SRT / mov_text).

**Supporting automation:**
- **Bazarr** auto-downloads Spanish subtitles for the whole library.
- **Maintainerr** auto-cleans watched movies (movies library only) to keep the data pool from filling
  up: two days after you watch one it unmonitors it in Radarr and deletes the library copy.
- **seed-cleanup.py** finishes that job on the torrent side, hourly. Radarr imports by hardlink, so
  every download has two names: the one qBittorrent seeds and the one Plex plays. Maintainerr removes
  the second, and this removes the first once the tracker is paid:

  | Link count | Tracker | What happens |
  | :--- | :--- | :--- |
  | 2 (still in the library) | any | nothing, it keeps seeding |
  | 1 (watched, gone from Plex) | public | torrent and data removed on the next pass |
  | 1 | private, seed goal met | torrent and data removed on the next pass |
  | 1 | private, goal pending | keeps seeding, tagged `waiting-seed`, checked again next hour |

  Goals live in `config/qbittorrent/seed-rules.json` (240 h seeding or ratio 1.0 for private, nothing
  for public). Whether a tracker is private comes from qBittorrent, so there is no list to maintain.
  qBittorrent's own share limits stay **off on purpose**: they cannot see the library, so they would
  delete torrents for films you have not watched yet. One deleter, one policy. `DRY_RUN=1` prints
  what it would remove and touches nothing; the "Seed cleanup" row of the Disk usage dashboard shows
  the queue.
- **Unpackerr** unrars scene releases. Radarr and Sonarr cannot read a `.rar` set, so those
  releases sit in the queue as `importPending` forever and the disk fills up with parts that
  never become a movie. The original archives are left alone so the torrent keeps seeding;
  only the extracted copy is removed once the *arr has imported it.

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

# Install Go (needed to build tailscale-metrics)
# https://go.dev/dl/ — download linux/arm64 tarball
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

### 6. Build tailscale-metrics

```bash
cd ~/rpi-homeserver/services/tailscale-metrics && make build
cd ~/rpi-homeserver
```

### 7. Start services

```bash
docker compose up -d
```

### 8. Set up Tailscale

```bash
sudo tailscale up --advertise-exit-node --accept-dns=false
```

Then approve the exit node in the Tailscale admin console and set Pi-hole as the DNS server.
→ See [docs/tailscale.md](docs/tailscale.md)

### 9. Configure auto-deployment

Install the crontab from the repo (never with `crontab -e`, the deploy overwrites the live one):

```bash
bash ~/rpi-homeserver/scripts/install-crontab.sh
```

That merges this repo's `scripts/crontab` with a companion repo's fragment if there is one, and
`apply.sh` re-runs it on every deploy, so committed cron changes apply themselves.

For push-triggered deploys instead of waiting for the 30-minute cron, see
[docs/deploy-webhook.md](docs/deploy-webhook.md).

### 10. Configure Pi-hole DNS records

For each HTTPS subdomain, add a DNS record in Pi-hole → Local DNS → DNS Records:

| Domain | IP |
| :--- | :--- |
| `raspi.platanosverdes.com` | `<TAILSCALE_IP>` |
| `jellyfin.platanosverdes.com` | `<TAILSCALE_IP>` |
| `grafana.platanosverdes.com` | `<TAILSCALE_IP>` |
| *(all other subdomains)* | `<TAILSCALE_IP>` |

### 11. Add entries to `/etc/hosts` on client devices

For HTTP short names to work on your laptop/desktop:

```
# LAN access
<STATIC_IP>    raspi homepage jellyfin overseerr plex grafana prometheus push prowlarr radarr sonarr flare torrent speedtest pihole

# Tailscale access (add same names pointing to Tailscale IP if not using Pi-hole DNS)
<TAILSCALE_IP> raspi homepage jellyfin overseerr plex grafana prometheus push prowlarr radarr sonarr flare torrent speedtest pihole
```

### 12. Check what still needs a human

```bash
bash scripts/recovery-status.sh
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
services/    Custom services source (acestream-updater, tailscale-metrics)
scripts/     Operational scripts (deploy, mount, rebuild)
appdata/     Persistent container data (databases, app state) — not in git
docs/        Setup guides
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

→ See [docs/add-service.md](docs/add-service.md) to add a new service with HTTPS

### Secrets Management (Bitwarden Secrets Manager)
`scripts/bws-run.py` is a wrapper that injects secrets from Bitwarden SM before running docker compose. **Currently paused** — secrets live in `.env` for now.
→ See [docs/secrets-manager.md](docs/secrets-manager.md)

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
`services/tailscale-metrics/` is a Go binary that runs as a **host cron job** (not a Docker container). It exports Tailscale peer status to Prometheus via node_exporter's textfile collector.

**Build:**
```bash
cd services/tailscale-metrics && make build
```

**Cron entry:**
```
* * * * * /home/raspi/rpi-homeserver/services/tailscale-metrics/tailscale-metrics >> /home/raspi/rpi-homeserver/tailscale-metrics.log 2>&1
```

### Auto-Deployment (`apply.sh`)
Pulls latest git changes every 15 minutes and rebuilds only when something changed.

```bash
# Cron entry (set up during install)
*/15 * * * * /home/raspi/rpi-homeserver/scripts/apply.sh >> /home/raspi/rpi-homeserver/apply.log 2>&1
```

Metrics are pushed to Pushgateway and visible in the **Deploy Monitor** dashboard in Grafana.

### Monitoring (Prometheus & Grafana)
- **Grafana:** `http://<IP>:3000` — default credentials: `admin / admin`
- **Auto-provisioned dashboards** (in `config/grafana/dashboards_json/`):
  - Container Health — host stats (CPU temp, load, RAM, disk), per-container CPU/mem/network, and a Storage section (usage-by-folder pie, folder filter, largest-files table)
  - Service probes — which service the Home dashboard's "Services down" number is about: per-service status, availability history and probe times. Every stat on Home is clickable and lands on the dashboard that answers it.
  - Retention policy — the film lifecycle end to end: how many are watched and in the grace period, how many are gone from Plex but still paying a private tracker (with the hours each one owes), what each tracker asks for, and the movements both halves have made, from the cleanup's own log and Maintainerr's
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
  bash /home/raspi/rpi-homeserver/scripts/apply.sh
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
`scripts/backup.sh` (daily cron at 04:00) snapshots `appdata/` to a compressed, rotated
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