# RPi HomeServer

A Docker Compose home server for Raspberry Pi: media (Plex, Jellyfin, the *arrs, qBittorrent with
private-tracker automation), live TV over AceStream, DNS and ad-blocking, monitoring, logs and
alerts. One `docker-compose.yml` includes four modules, every image version is pinned in
`versions.env`, and a push to `main` is the only way anything is deployed.

> **Companion repo:** personal services (Telegram bots, the Fantasy panel, a public flight board)
> live in [rpi-services](https://github.com/PlatanosVerdes/rpi-services). This repo stays generic
> enough to clone. Both share `media-network` and one Caddy, and are deployed by the same script.

## Quick start

Follow [docs/host-setup.md](docs/host-setup.md): Docker, the data disk, `.env` from `.env.example`,
then `docker compose up -d` with `COMPOSE_ENV_FILES=versions.env,.env`. From there the cron (and,
once set up, the webhook) keeps the Pi in step with `main`.

Everything else (the two nodes, the three doors, what runs, how a change deploys, what gets deleted
and the everyday commands) is in [docs/overview.md](docs/overview.md).

## Docs

| | Doc | What it covers |
| :--- | :--- | :--- |
| **Start** | [overview.md](docs/overview.md) | Two nodes and three doors, what runs, how a change deploys, what gets deleted, everyday commands |
| **Run it** | [host-setup.md](docs/host-setup.md) | A fresh Pi to the first deploy: disks, Docker, journald, cron |
| | [nodes.md](docs/nodes.md) | The two nodes, profiles, the override file, what one node reads from the other |
| | [deploy.md](docs/deploy.md) | The deploy pass, concurrency and retries, the webhook and tunnel |
| | [secrets.md](docs/secrets.md) | `.env` encrypted per node with SOPS + age |
| | [backups.md](docs/backups.md) | Nightly appdata archive, restore, offsite |
| | [updates.md](docs/updates.md) | Renovate's image update PRs, and which ones merge on their own |
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
