# RPi HomeServer

A modular, Docker-based home server for Raspberry Pi. Uses Docker Compose's `include` feature to keep each concern in a separate, maintainable file.

---

## Infrastructure Overview

| Module | Purpose | Key Services |
| :--- | :--- | :--- |
| **Core** | Entry point & Networking | Caddy, Homepage, Pi-hole, Speedtest-tracker |
| **Media** | Streaming & Live TV | Plex, Jellyfin, Aceserve (Acestream) |
| **Arrs** | Automation & Downloads | Radarr, Sonarr, Prowlarr, qBittorrent, FlareSolverr |
| **Monitoring** | System Health | Prometheus, Grafana, Pushgateway, node-exporter, cAdvisor |
| **Apps** | Custom Services | Vaultwarden, Pol Academy Offers Bot, Acestream Updater |

---

## Installation

```bash
git clone <your-repo-url> rpi-homeserver
cd rpi-homeserver
cp .env.example .env   # fill .env in your values
docker compose up -d
```

### Project Structure

```
config/      Static config files (Caddyfile, Prometheus, Grafana dashboards)
appdata/     Persistent container data (databases, app state)
scripts/     Custom scripts (acestream updater, deploy control)
docs/        Detailed setup guides
```

### External Storage (`DATA_ROOT`)

Mount your external drive permanently by UUID:

```bash
# Find UUID and filesystem type
lsblk -f

# Create mount point
sudo mkdir -p /mnt/data

# Add to /etc/fstab (replace UUID and fstype)
UUID=your-uuid  /mnt/data  ext4  defaults,noatime,nofail  0  2

# Apply
sudo mount -a
```

---

## Module Guides

### Networking (Tailscale + Pi-hole)
Tailscale provides secure remote access. Pi-hole handles DNS and ad-blocking.
→ See [docs/tailscale.md](docs/tailscale.md)

### Reverse Proxy (Caddy)
Caddy provides short HTTP names on LAN (`http://jellyfin`, `http://raspi`, etc.) and HTTPS via Cloudflare DNS challenge for remote access.

Add to your `hosts` file on each client device:
```
# LAN access
<STATIC_IP>    raspi homepage jellyfin overseerr plex grafana prometheus push prowlarr radarr sonarr flare torrent speedtest pihole vault

# Tailscale access
<TAILSCALE_IP> raspi homepage jellyfin overseerr plex grafana prometheus push prowlarr radarr sonarr flare torrent speedtest pihole vault
```

### Secrets Management (Vaultwarden)
Self-hosted Bitwarden-compatible vault. **Requires HTTPS** for the mobile app.
→ See [docs/vaultwarden-https.md](docs/vaultwarden-https.md)

### Media Automation (The *arrs Suite)
- **Prowlarr → FlareSolverr:** Settings → Indexers → Add Proxy → `http://flaresolverr:8191`
- **Prowlarr → Radarr/Sonarr:** Settings → Apps → add each with their API keys
- **Download client:** In Radarr/Sonarr → Settings → Download Clients → qBittorrent → host `qbittorrent`, port `8080`

### Acestream Live TV
The `acestream-updater` (Go binary) fetches IPFS channel lists, deduplicates them by acestream hash, writes a `.m3u` for Jellyfin, and runs concurrent health checks to verify each channel is actually serving bytes. It refreshes Jellyfin automatically when the playlist changes.

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

### Auto-Deployment (`deploy_control.sh`)
Pulls latest git changes every 15 minutes and rebuilds only when something changed.

```bash
# Cron entry (already configured)
*/15 * * * * /home/raspi/rpi-homeserver/scripts/deploy_control.sh > /home/raspi/rpi-homeserver/deploy_control.log 2>&1
```

Metrics are pushed to Pushgateway and visible in the **Deploy Monitor** dashboard in Grafana.

### Monitoring (Prometheus & Grafana)
- **Grafana:** `http://<IP>:3000` — default credentials: `admin / admin`
- **Auto-provisioned dashboards** (Scripts folder in Grafana):
  - Acestream Monitor — channel sync, changes, errors, source URL status, per-channel health (state timeline)
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

## Pending / Future Work

See [PENDING.md](PENDING.md) for the full task list. Summary:

| Task | Notes |
| :--- | :--- |
| **Bitwarden Secrets Manager** | Replace `.env` secrets with BSM; only `BWS_ACCESS_TOKEN` stays on disk |
| **Pi-hole monitoring** | Add `pihole-exporter` to `compose-mon.yml` (commented out, ready to enable) |
| **Tailscale monitoring** | Add `tailscale-exporter` to `compose-mon.yml` (commented out, ready to enable) |

---

## Host-Level Configuration

### Docker Log Limits
Prevents the SD card from filling up. Add to `/etc/docker/daemon.json`:

```json
{
  "log-driver": "json-file",
  "log-opts": {
    "max-size": "10m",
    "max-file": "3"
  }
}
```
