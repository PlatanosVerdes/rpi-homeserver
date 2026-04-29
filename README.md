# RPi HomeServer

A modular, Docker-based home server for Raspberry Pi. Uses Docker Compose's `include` feature to keep each concern in a separate, maintainable file.

---

## Infrastructure Overview

| Module | Purpose | Key Services |
| :--- | :--- | :--- |
| **Core** | Entry point & Networking | Caddy, Homepage, Pi-hole, Speedtest-tracker |
| **Media** | Streaming & Live TV | Plex, Jellyfin, Overseerr, Acestream |
| **Arrs** | Automation & Downloads | Radarr, Sonarr, Prowlarr, qBittorrent, FlareSolverr |
| **Monitoring** | System Health | Prometheus, Grafana, Pushgateway, node-exporter, cAdvisor |
| **Apps** | Custom Services | Pol Academy Offers Bot |

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

```bash
crontab -e
# Add:
*/15 * * * * /home/raspi/rpi-homeserver/scripts/deploy_control.sh >> /home/raspi/rpi-homeserver/deploy_control.log 2>&1
```

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

---

## Profiles — selecting which services to run

Every service belongs to one or more profiles. Set `COMPOSE_PROFILES` in `.env` to control what starts — no need to touch the compose files.

| Profile | Services |
| :--- | :--- |
| `essential` | Caddy, Homepage, Pi-hole, Speedtest-tracker |
| `moni` | Prometheus, Grafana, Pushgateway, node-exporter, cAdvisor, Pihole-exporter, Speedtest-tracker |
| `acestream` | Aceserve, Acestream-updater, Jellyfin + Grafana/Prometheus/Pushgateway |
| `media` | Plex, Overseerr, Prowlarr, Radarr, Sonarr, qBittorrent, FlareSolverr |
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

### Reverse Proxy (Caddy)
Caddy provides short HTTP names on LAN (`http://jellyfin`, `http://raspi`, etc.) and HTTPS via Cloudflare DNS challenge for remote access (`https://*.platanosverdes.com`).

Caddy uses a custom Docker image that includes the Cloudflare DNS plugin (see `config/caddy/Dockerfile`). Certificates are issued automatically on first request and renewed by Caddy.

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

### Auto-Deployment (`deploy_control.sh`)
Pulls latest git changes every 15 minutes and rebuilds only when something changed.

```bash
# Cron entry (set up during install)
*/15 * * * * /home/raspi/rpi-homeserver/scripts/deploy_control.sh >> /home/raspi/rpi-homeserver/deploy_control.log 2>&1
```

Metrics are pushed to Pushgateway and visible in the **Deploy Monitor** dashboard in Grafana.

### Monitoring (Prometheus & Grafana)
- **Grafana:** `http://<IP>:3000` — default credentials: `admin / admin`
- **Auto-provisioned dashboards** (Scripts folder in Grafana):
  - Acestream Monitor — channel sync, changes, errors, source URL status, per-channel health
  - Deploy Monitor — deploy runs, changes, errors
- **Import community dashboards** (Grafana → Dashboards → Import):
  - `1860` — Node Exporter Full (CPU, RAM, disk, network)
  - `193` — cAdvisor (per-container resource usage)
- **No data on a dashboard?** Trigger a run manually:
  ```bash
  docker compose -f compose-media.yml restart acestream-updater
  bash /home/raspi/rpi-homeserver/scripts/deploy_control.sh
  ```

---

## Host-Level Configuration

See [SYSTEM_NOTES.md](SYSTEM_NOTES.md) for all OS-level settings (Docker log limits, cron jobs, sysctl, etc.).