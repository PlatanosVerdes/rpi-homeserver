# Nodes

Two machines run this repo, both from `main`, with the same code and the same `versions.env`. What
differs lives in each one's `.env`: which services it runs, and a few addresses.

| | raspi (main) | streamhalla (second node) |
| :--- | :--- | :--- |
| Runs | everything | the AceStream + Jellyfin subset, plus its own metrics |
| `COMPOSE_PROFILES` | `all` plus `tunnel` | a list of services (below) |
| `COMPOSE_FILE` | unset | `docker-compose.yml:overrides/secondary-node.yml` |
| Deploys on | the webhook and its cron | its cron only |
| Pi-hole, Caddy, Grafana, VictoriaLogs | yes | no |
| Companion repo (rpi-services) | yes | only if checked out; `apply.sh` skips it otherwise |
| Backups | to its data disk | to the raspi's data disk, over Tailscale |

## Profiles

Every service has a profile of its own name, plus a group, plus `all`:

| Group | Services |
| :--- | :--- |
| `essential` | caddy, homepage, pihole, speedtest |
| `moni` | prometheus, grafana, pushgateway, node-exporter, cadvisor, blackbox-exporter, pi-metrics, qbittorrent-exporter, pihole-exporter, tailscale-metrics, victorialogs, vector, speedtest |
| `acestream` | jellyfin, gluetun, aceserve, kubo, bifrost, farnsworth, baird, plus prometheus, grafana, pushgateway |
| `media` | plex, seerr, bazarr, maintainerr, tautulli, watch-next, subtitle-links, audio-convert, prowlarr, radarr, sonarr, qbittorrent, flaresolverr, unpackerr, qbit-manage, autobrr, tracker-control, cross-seed, torrent-drop |
| `vpn` | sleipnir, docker-socket-proxy, gluetun-test, huginn |
| `bots` | muninn, docker-socket-proxy |
| `tunnel` | cloudflared |
| `all` | everything except cloudflared: with an empty token it would crash-loop, so it is opt-in |

The compose files are the source; this table is a summary. A node running a subset lists services
rather than groups, so it gets exactly what it names:

```bash
COMPOSE_PROFILES=jellyfin,gluetun,aceserve,kubo,bifrost,sleipnir,docker-socket-proxy,speedtest,prometheus,pushgateway,node-exporter,cadvisor
COMPOSE_FILE=docker-compose.yml:overrides/secondary-node.yml
```

Profiles are not part of a container's config hash, so adding one recreates nothing.

## The override file

`overrides/secondary-node.yml` holds what only a second node needs, so the main node's containers
are untouched. Lists such as `ports` are appended to the base file; `command` replaces it.

| Service | What changes | Why |
| :--- | :--- | :--- |
| jellyfin | `JELLYFIN_PublishedServerUrl` from `JELLYFIN_PUBLISHED_SERVER_URL` | the address the TV app is handed on its LAN |
| bifrost | publishes `:6879` on `TAILSCALE_IP`; `ACESERVE_BASE_URL` via `host.docker.internal`; `BIFROST_NAME` | the raspi reads its status page over Tailscale; the bridge gateway is not stable there |
| prometheus | publishes `:9090` on `TAILSCALE_IP`; retention 7 d / 3 GB | the raspi's Grafana reads it; it is an SD card, so retention is bounded |

## Steps and jobs for services a node does not run

- **`apply.sh`**: `runs <service>` asks compose which services this node's `COMPOSE_PROFILES` enables.
  Rendering Grafana alerting, copying qbit-manage's config and the config-drift check are gated on
  it; the other sync scripts skip themselves when their app is absent, and the webhook unit is only
  restarted where it is installed. See [deploy.md](deploy.md).
- **cron**: a `# when: <service>` line above a job in `scripts/crontab` keeps it only where that
  service runs; `# when: file <path>` keeps it only where the file exists. The deploy itself, the
  backup and the heartbeat have no `# when:` and run on every node.

Never assume a service runs everywhere: a new step or cron job for one service needs its `runs` or
`# when:` guard.

## How the main node reads the second

Nothing in this repo names the second node; the wiring is in the companion repo and in `.env`:

| What | How |
| :--- | :--- |
| Its Prometheus in Grafana | a datasource file mounted through `EXT_GRAFANA_DATASOURCES`; dashboards with a Node selector (AceStream, Container Health) switch between them. `GRAFANA_PROMETHEUS_NAME` names the local one |
| Its pages behind the raspi's Caddy | a `.caddy` file in the companion repo's `config/caddy/`, imported through `EXT_CADDY_PATH`, proxying to the node's Tailscale address |
| sleipnir | `SLEIPNIR_PEERS` on the raspi, `SLEIPNIR_TOKEN` shared; peers answer on `:8092` on the Tailscale address ([sleipnir.md](sleipnir.md)) |

## Secrets and backups

Each node has its own directory in the private secrets repo, named after its Tailscale name, and
can decrypt only that directory ([secrets.md](secrets.md)).

A second node whose `appdata` is on its SD card sets `BACKUP_RCLONE_REMOTE` to an SFTP remote on the
main node, so its nightly archive lands on a different device ([backups.md](backups.md)).

## Adding a node

1. [host-setup.md](host-setup.md), with Docker's log limits and `tailscale set --accept-dns=false`.
2. Its `.env` in the secrets repo, with `COMPOSE_PROFILES`, `COMPOSE_FILE` and the override's
   variables (see the "Second node" block at the end of `.env.example`).
3. `bash scripts/setup/install-crontab.sh` once; from then on the cron deploys it.
4. On the main node, add its datasource, Caddy routes and sleipnir peer in the companion repo.
