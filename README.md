# RPi HomeServer

A Docker Compose home server for Raspberry Pi: media (Plex, Jellyfin, the *arrs, qBittorrent with
private-tracker automation), live TV over AceStream, DNS and ad-blocking, monitoring, logs and
alerts. One `docker-compose.yml` includes four modules, every image version is pinned in
`versions.env`, and a push to `main` is the only way anything is deployed.

> **Companion repo:** personal services (Telegram bots, the Fantasy panel, a public flight board)
> live in [rpi-services](https://github.com/PlatanosVerdes/rpi-services). This repo stays generic
> enough to clone. Both share `media-network` and one Caddy, and are deployed by the same script.

## Two nodes, three doors

<!-- diagram: two-nodes-three-doors -->

| Node | Runs | Deploys on |
| :--- | :--- | :--- |
| raspi (main) | everything: `COMPOSE_PROFILES=all` plus the tunnel | the GitHub webhook and a 30-minute cron |
| streamhalla | the AceStream + Jellyfin subset and its own metrics, with `overrides/secondary-node.yml` | its own 30-minute cron |

Both run `main`. Differences live in each node's `.env`, which the deploy decrypts from a private
repo. Details: [docs/nodes.md](docs/nodes.md).

| Door | Who gets in |
| :--- | :--- |
| Tailnet (Tailscale + Pi-hole + Caddy) | your devices; most apps ask nothing more |
| Public, behind Cloudflare Access | anyone who passes Access's login, through an outbound tunnel |
| Public, no login | only the deploy webhook (HMAC-signed) and apps public by design |

Plex is never proxied: it is reached on its LAN address, over Tailscale subnet routes from outside.
Details: [docs/access.md](docs/access.md).

## What runs

| Module | Services |
| :--- | :--- |
| `compose-core.yml` | Caddy, Homepage, Pi-hole, Speedtest-tracker, cloudflared (opt-in) |
| `compose-media.yml` | Plex, Jellyfin, Seerr, Bazarr, Maintainerr, Tautulli, watch-next, subtitle-links, audio-convert; AceStream: gluetun, aceserve, kubo, bifrost, farnsworth; sleipnir, muninn |
| `compose-arrs.yml` | Prowlarr, Radarr, Sonarr, qBittorrent, FlareSolverr, Unpackerr, autobrr, qbit-manage, tracker-control, cross-seed, torrent-drop |
| `compose-mon.yml` | Prometheus, Grafana, Pushgateway, node-exporter, cAdvisor, blackbox-exporter, pi-metrics, qBittorrent and Pi-hole exporters, tailscale-metrics, VictoriaLogs, Vector |

Every service has a profile of its own name plus a group, so a node picks exactly what it runs.
Images for the services in `services/` are built by GitHub Actions and pulled, not compiled on the
Pi. bifrost, sleipnir and muninn are apps in their own repos, pinned here.

## How a change deploys

<!-- diagram: push-to-container -->

1. A push to `main` reaches the Pi through a GitHub webhook and a Cloudflare tunnel; a cron every
   30 minutes on each node catches anything missed.
2. `scripts/deploy/apply.sh` decrypts `.env`, pulls both repos, recreates only the containers whose
   image changed, and pushes the config that lives in apps' databases back into them.
3. A change to a `services/` image deploys when CI commits its new pin, minutes after the source push.

Details: [docs/deploy.md](docs/deploy.md).

## What gets deleted

Nothing is deleted before it is watched. A watched film goes two days later; its torrent goes when
its tracker is paid, never before. [docs/lifecycle.md](docs/lifecycle.md) has the rules and every
number.

## Documentation

| | Doc | What it covers |
| :--- | :--- | :--- |
| **Run it** | [host-setup.md](docs/host-setup.md) | A fresh Pi to the first deploy: disks, Docker, journald, cron |
| | [nodes.md](docs/nodes.md) | The two nodes, profiles, the override file, what one node reads from the other |
| | [deploy.md](docs/deploy.md) | The deploy pass, concurrency and retries, the webhook and tunnel |
| | [secrets.md](docs/secrets.md) | `.env` encrypted per node with SOPS + age |
| | [backups.md](docs/backups.md) | Nightly appdata archive, restore, offsite |
| **Reach it** | [access.md](docs/access.md) | The three doors, adding a public app |
| | [tailscale.md](docs/tailscale.md) | Exit node, subnet routes, Pi-hole as tailnet DNS, guests |
| | [plex-remote-access.md](docs/plex-remote-access.md) | Why Plex counts as local from outside, and how to keep it so |
| **Watch it** | [alerting.md](docs/alerting.md) | Metrics, dashboards, alert rules, Telegram, the dead man's switch |
| | [logging.md](docs/logging.md) | VictoriaLogs and Vector, LogsQL |
| **Media** | [media-policy.md](docs/media-policy.md) | Playback targets, formats, scores, qBittorrent limits |
| | [lifecycle.md](docs/lifecycle.md) | One torrent from indexer to recycle bin, Maintainerr, orphans |
| | [private-trackers.md](docs/private-trackers.md) | Per-site rules, the ratio loop, cross-seed, inbound, indexers |
| | [audio-convert.md](docs/audio-convert.md) | Adding an AC3 track to files a client re-encodes |
| | [watch-next.md](docs/watch-next.md) | Fetching the next episodes as you watch |
| **Services** | [farnsworth.md](docs/farnsworth.md) | AceStream live TV, and handing a channel to VLC |
| | [sleipnir.md](docs/sleipnir.md) | The VPN tunnel panel |
| | [service-images.md](docs/service-images.md) | How `services/` images are built, pinned and pulled |
| | [add-service.md](docs/add-service.md) | Checklist for a new service |
| **Background** | [architecture.md](docs/architecture.md) | What talks to what, who may overwrite which app's config |
| | [decisions.md](docs/decisions.md) | Ideas tried or weighed and turned down |

Open work: [PENDING.md](PENDING.md). Conventions for changes: [CLAUDE.md](CLAUDE.md).

## Quick start

Follow [docs/host-setup.md](docs/host-setup.md): Docker, the data disk, `.env` from `.env.example`,
then `docker compose up -d` with `COMPOSE_ENV_FILES=versions.env,.env`. From there the cron (and,
once set up, the webhook) keeps the Pi in step with `main`.

## Everyday commands

```bash
bash scripts/deploy/apply.sh                         # deploy now, instead of waiting
tail -f ~/rpi-homeserver/apply.log                   # what the last deploys did
docker logs -f <container>                           # one service's output
bash scripts/recovery/rebuild-service.sh <service>   # rebuild one service from scratch
bash scripts/recovery/recovery-status.sh             # after a rebuild: what still needs a human
bash scripts/ops/backup.sh                           # a backup now
```

Run compose by hand with `export COMPOSE_ENV_FILES=versions.env,.env` first, or image versions do
not resolve.
