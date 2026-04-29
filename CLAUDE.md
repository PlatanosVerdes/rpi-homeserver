# RPi HomeServer — Claude Code Context

## What this project is

A modular Docker-based home server running on a Raspberry Pi. All services run as Docker containers managed by Docker Compose. The repo is the source of truth: a cron job (`deploy_control.sh`) pulls from git every 15 minutes and applies changes automatically.

---

## Repository layout

```
docker-compose.yml          Entry point — uses `include` to load all modules
compose-core.yml            Caddy, Homepage, Pi-hole, Speedtest-tracker
compose-media.yml           Plex, Jellyfin, Overseerr, Acestream
compose-arrs.yml            Prowlarr, Radarr, Sonarr, qBittorrent, FlareSolverr
compose-mon.yml             Prometheus, Grafana, Pushgateway, node-exporter, cAdvisor
compose-apps.yml            Custom apps (Telegram bot)

config/                     Static config files committed to git
  caddy/Caddyfile           Reverse proxy rules (HTTPS + HTTP short names)
  caddy/Dockerfile          Custom Caddy image with Cloudflare DNS plugin
  prometheus/prometheus.yml Scrape targets
  grafana/                  Provisioned datasources + dashboard JSONs
  homepage/                 Dashboard YAML configs

services/                   Source code for custom services built in this repo
  acestream-updater/        Go service — fetches IPFS channel lists, writes .m3u for Jellyfin
  tailscale-metrics/        Go binary (cron) — exports Tailscale peer metrics to node_exporter

scripts/                    Operational scripts
  deploy_control.sh         Auto-deploy cron job (runs every 15 min via cron)
  mount_setup.sh            One-time external disk mount setup
  rebuild-service.sh        Manual single-service rebuild helper
  bws-run.py                Bitwarden SM wrapper (PENDING — not active, see below)

appdata/                    Persistent container data (NOT in git, lives on disk)
docs/                       Setup guides
```

---

## Secrets — current state

Secrets currently live in **`.env`** as plain variables. Copy `.env.example` to `.env` and fill in values.

```bash
docker compose up -d
```

> **Planned improvement:** `scripts/bws-run.py` exists and is ready to move secrets to
> Bitwarden Secrets Manager (BWS), but that migration is paused. See [docs/secrets-manager.md](docs/secrets-manager.md).
> Do not assume BWS is active — deploy_control.sh uses plain `docker compose`.

---

## How auto-deploy works

`scripts/deploy_control.sh` runs every 15 minutes via cron:
1. `git pull origin main`
2. If HEAD changed → `docker compose up -d --build --remove-orphans`
3. If no change → `docker compose up -d --remove-orphans` (ensures containers are running)
4. Pushes metrics to Pushgateway (visible in Grafana "Deploy Monitor" dashboard)

**Cron entry on the host:**
```
*/15 * * * * /home/raspi/rpi-homeserver/scripts/deploy_control.sh >> /home/raspi/rpi-homeserver/deploy_control.log 2>&1
```

---

## Networking

- **Local LAN:** Caddy listens for HTTP short names (`http://jellyfin`, `http://grafana`, etc.). Clients need an entry in `/etc/hosts` pointing `STATIC_IP` to those names.
- **Remote (Tailscale):** All HTTPS subdomains (`*.platanosverdes.com`) resolve to the Pi's Tailscale IP (`TAILSCALE_IP`). Certificates issued automatically via Cloudflare DNS challenge.
- **Pi-hole:** DNS for the whole tailnet. All `*.platanosverdes.com` subdomains point to `TAILSCALE_IP` in Pi-hole's custom DNS.
- **Docker network:** All services share `media-network` (bridge).

---

## Profiles — selecting which services to run

Controlled via `COMPOSE_PROFILES` in `.env`. No need to touch compose files.

| Profile | Services |
| :--- | :--- |
| `essential` | Caddy, Homepage, Pi-hole, Speedtest-tracker |
| `moni` | Prometheus, Grafana, Pushgateway, node-exporter, cAdvisor, Pihole-exporter, Speedtest-tracker |
| `acestream` | Aceserve, Acestream-updater, Jellyfin + Grafana/Prometheus/Pushgateway |
| `media` | Plex, Overseerr, Prowlarr, Radarr, Sonarr, qBittorrent, FlareSolverr |
| `bot` | Pol Academy Offers Bot |
| `cal` | Google Calendar Bridge (cal-bridge) |
| `all` | Everything |

Main Pi: `COMPOSE_PROFILES=all`. Secondary Pi: e.g. `COMPOSE_PROFILES=essential,moni`.

---

## Adding a new service

See [docs/add-service.md](docs/add-service.md) for the step-by-step guide. Short version:
1. Add service to the appropriate `compose-*.yml`
2. Add HTTPS route to `config/caddy/Caddyfile`
3. Add DNS record in Pi-hole pointing to `TAILSCALE_IP`
4. Add to Homepage `config/homepage/services.yaml` (optional)

---

## Custom services in `services/`

### acestream-updater
Go service, Dockerized. Built at compose time from `services/acestream-updater/`. Runs as a long-lived container, periodically fetches IPFS channel lists and updates Jellyfin's `.m3u` playlist.

Rebuild after Go source changes:
```bash
docker compose -f compose-media.yml up -d --build acestream-updater
```

### tailscale-metrics
Go binary that runs as a **host cron job** (not a Docker container). Calls `tailscale status --json` and the Tailscale API, writes a `.prom` file for node_exporter's textfile collector.

Build:
```bash
cd services/tailscale-metrics && make build
```

The compiled binary is NOT committed to git. Run `make build` after cloning.

Cron entry:
```
* * * * * /home/raspi/rpi-homeserver/services/tailscale-metrics/tailscale-metrics >> /home/raspi/rpi-homeserver/tailscale-metrics.log 2>&1
```

---

## Key `.env` variables

```bash
TZ, STATIC_IP, TAILSCALE_IP   # Host config
PUID, PGID                     # File ownership (run: id -u && id -g)
DATA_ROOT                      # External disk mount (e.g. /mnt/data)
DATA_DB_ROOT                   # DB subdirectory
CONFIG_ROOT=./config
APP_CONFIG_PATH=./appdata
CF_API_TOKEN                   # Cloudflare DNS token for HTTPS certs
TAILSCALE_API_KEY              # Read directly by tailscale-metrics binary
```

---

## Grafana dashboards

All dashboards are provisioned from JSON files in `config/grafana/dashboards_json/`. Changes to dashboards must be exported from Grafana and committed here — they are NOT persisted in the Grafana container's volume.

Community dashboards to import manually:
- `1860` — Node Exporter Full
- `193` — cAdvisor

---

## Common operations

```bash
# Restart a service after config change
docker compose -f compose-core.yml restart caddy

# View live logs for a service
docker logs -f <container-name>

# Trigger deploy manually
bash scripts/deploy_control.sh

# Rebuild from scratch (single service)
bash scripts/rebuild-service.sh <service-name>
```